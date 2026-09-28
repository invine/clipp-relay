package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeletionJournal is the serving identity's complete storage authority. Its
// implementation must create immutable events and replace only the head with
// an If-Match precondition. It may list bounded event pages for verification,
// but has no delete, overwrite, or backup operation.
type DeletionJournal interface {
	ReadHead(context.Context) (JournalHead, string, error)
	CreateEvent(context.Context, string, []byte) error
	ReadEvent(context.Context, string) ([]byte, error)
	ReplaceHead(context.Context, string, JournalHead) error
	ListEvents(context.Context, string) ([]string, string, error)
}

// SetDeletionFinalizeCommitHookForTest simulates lost finalization responses
// after PostgreSQL has committed; serving code leaves this unset.
func (s *Server) SetDeletionFinalizeCommitHookForTest(commit func(context.Context, pgx.Tx) error) {
	s.commitDeletion = commit
}

type JournalHead struct {
	RepositoryID          string `json:"repository_id"`
	CoverageFloor         int64  `json:"coverage_floor"`
	CoverageHash          string `json:"coverage_hash"`
	Sequence              int64  `json:"sequence"`
	Hash                  string `json:"hash"`
	MaintenanceGeneration int64  `json:"maintenance_generation"`
	Format                int    `json:"format"`
}

type deletionEvent struct {
	Format       int       `json:"format"`
	AccountID    string    `json:"account_id"`
	EventID      string    `json:"event_id"`
	Sequence     int64     `json:"sequence"`
	PreviousHash string    `json:"previous_hash"`
	AdmittedAt   time.Time `json:"admitted_at"`
	Hash         string    `json:"hash"`
}

type uncertainDeletion struct{ operationID string }

var errJournalTargetUncommitted = errors.New("journal target event not committed")

// SetDeletionJournal requires an operator-supplied repository identity and
// coverage floor. A nil/unqualified journal leaves deletion unavailable.
func (s *Server) SetDeletionJournal(j DeletionJournal, repository string, floor int64, floorHash string) {
	s.deletionJournal = j
	s.deletionRepository = repository
	s.deletionFloor = floor
	s.deletionFloorHash = floorHash
}

func (s *Server) qualifiedHead(h JournalHead) bool {
	if s.deletionJournal == nil || s.deletionRepository == "" || s.deletionFloor < 0 || h.RepositoryID != s.deletionRepository || h.CoverageFloor != s.deletionFloor || h.CoverageHash != s.deletionFloorHash || h.Sequence < h.CoverageFloor || h.MaintenanceGeneration < 0 || h.Format != 1 {
		return false
	}
	b, e := hex.DecodeString(h.Hash)
	floor, floorErr := hex.DecodeString(h.CoverageHash)
	return e == nil && len(b) == 32 && floorErr == nil && len(floor) == 32 && (h.Sequence != h.CoverageFloor || h.Hash == h.CoverageHash)
}

func (s *Server) deleteOwner(w http.ResponseWriter, r *http.Request) {
	if s.deletionJournal == nil || s.deletionRepository == "" || r.Header.Get("Origin") != s.Origin {
		fail(w, 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		fail(w, 400)
		return
	}
	id, version, _, _, csrf, e := s.session(r)
	if e != nil {
		fail(w, 401)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		fail(w, 403)
		return
	}
	if _, _, err := s.readHead(r.Context()); err != nil {
		fail(w, 503)
		return
	}
	var subject string
	if s.db.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1 AND deletion_started_at IS NULL`, id).Scan(&subject) != nil {
		fail(w, 409)
		return
	}
	operation, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	var admitted, uncertain bool
	var closeConnections func()
	guardEntered := false
	guardErr := s.withIdentityGuard(r.Context(), subject, func(unitCtx context.Context) error {
		guardEntered = true
		if !s.WithAccountGuards(unitCtx, []string{id}, func(ctx context.Context) {
			tx, err := s.db.Begin(ctx)
			if err != nil {
				fail(w, 503)
				return
			}
			defer tx.Rollback(ctx)
			var live bool
			var authenticatedAt time.Time
			err = tx.QueryRow(ctx, `SELECT ps.google_authenticated_at,ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp() AND ps.credential_generation=a.credential_generation FROM public.accounts a JOIN public.portal_sessions ps ON ps.account_id=a.id WHERE a.id=$1 AND a.subject=$2 AND a.deletion_started_at IS NULL AND ps.credential_digest=$3 AND ps.pepper_version=$4 FOR UPDATE OF a`, id, subject, s.sessionDigest(r, version), version).Scan(&authenticatedAt, &live)
			if err != nil || !live {
				fail(w, 401)
				return
			}
			var recent bool
			if tx.QueryRow(ctx, `SELECT $1::timestamptz >= clock_timestamp()-interval '10 minutes' AND $1::timestamptz <= clock_timestamp()`, authenticatedAt).Scan(&recent) != nil || !recent {
				fail(w, 403)
				return
			}
			var pending int
			if tx.QueryRow(ctx, `SELECT pending FROM public.deletion_capacity WHERE singleton=true FOR UPDATE`).Scan(&pending) != nil {
				fail(w, 503)
				return
			}
			if pending >= 1024 {
				fail(w, 503)
				return
			}
			if _, err = tx.Exec(ctx, `INSERT INTO public.deletion_operations(id,account_id,admitted_at,next_attempt_at) VALUES($1,$2,clock_timestamp(),clock_timestamp())`, operation, id); err != nil {
				fail(w, 503)
				return
			}
			if _, err = tx.Exec(ctx, `UPDATE public.accounts SET deletion_started_at=clock_timestamp() WHERE id=$1`, id); err != nil {
				fail(w, 503)
				return
			}
			if err = revokeAccountCredentials(ctx, tx, id); err != nil {
				fail(w, 503)
				return
			}
			if _, err = tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=pending+1 WHERE singleton=true`); err != nil {
				fail(w, 503)
				return
			}
			if s.commitAccount != nil {
				err = s.commitAccount(ctx, tx)
			} else {
				err = tx.Commit(ctx)
			}
			s.installIdentityFence(subject)
			if err != nil {
				s.uncertainAccounts.Store(id, uncertainDeletion{operationID: operation})
			}
			if s.accountChanged != nil {
				closeConnections = s.accountChanged(AccountChange{AccountID: id, CloseAll: true, DiscardCredit: true})
			}
			admitted, uncertain = true, err != nil
		}) {
			fail(w, 503)
		}
		return nil
	})
	if guardErr != nil && !guardEntered {
		fail(w, 503)
	}
	if !admitted {
		return
	}
	if closeConnections != nil {
		closeConnections()
	}
	cookie(w, sessionCookie, "", -1)
	if uncertain {
		uncertainResponse(w)
		return
	}
	w.Header().Set("Location", "/auth/deletion/"+operation)
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Deletion pending; retrying independently."))
}

func (s *Server) deletionStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) {
		fail(w, 404)
		return
	}
	var completed *time.Time
	err := s.db.QueryRow(r.Context(), `SELECT completed_at FROM public.deletion_operations WHERE id=$1`, id).Scan(&completed)
	if err == pgx.ErrNoRows {
		fail(w, 404)
		return
	}
	if err != nil {
		fail(w, 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if completed == nil {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("Deletion pending; retrying independently."))
		return
	}
	_, _ = w.Write([]byte("Deletion complete."))
}

type pendingDeletion struct {
	ID, Account string
	Admitted    time.Time
	Sequence    *int64
	Hash, Data  []byte
	Generation  *int64
	Attempts    int
}

func (s *Server) nextDeletion(ctx context.Context) (pendingDeletion, error) {
	var x pendingDeletion
	err := s.db.QueryRow(ctx, `SELECT id,account_id,admitted_at,event_sequence,event_hash,event_data,journal_generation,attempts FROM public.deletion_operations WHERE completed_at IS NULL AND next_attempt_at<=clock_timestamp() ORDER BY next_attempt_at,admitted_at,id LIMIT 1`).Scan(&x.ID, &x.Account, &x.Admitted, &x.Sequence, &x.Hash, &x.Data, &x.Generation, &x.Attempts)
	return x, err
}

func (s *Server) readHead(ctx context.Context) (JournalHead, string, error) {
	if s.deletionJournal == nil {
		return JournalHead{}, "", errors.New("journal unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h, etag, err := s.deletionJournal.ReadHead(bounded)
	if err != nil || !s.qualifiedHead(h) || etag == "" {
		return JournalHead{}, "", errors.New("journal head unavailable or unqualified")
	}
	if b, _ := json.Marshal(h); len(b) > 16<<10 {
		return JournalHead{}, "", errors.New("journal head too large")
	}
	return h, etag, nil
}

// ValidateDeletionJournal is a startup gate for an explicitly configured
// repository. Missing, empty, wrong, or unqualified heads fail closed.
func (s *Server) ValidateDeletionJournal(ctx context.Context) error {
	head, _, err := s.readHead(ctx)
	if err != nil {
		return err
	}
	return s.verifyCommittedChain(ctx, head, 0, "")
}

// verifyCommittedChain checks a fixed head snapshot from the operator's
// attested floor. An immutable upload is not committed merely because its
// sequence is below the head: failed head comparisons can leave orphan events
// at the same sequence. Only a hash-linked path ending at the head is proof.
// OCI pages and the reachable set are bounded; the caller bounds total work.
func (s *Server) verifyCommittedChain(ctx context.Context, head JournalHead, targetSeq int64, targetHash string) error {
	if head.Sequence == head.CoverageFloor {
		if targetSeq != 0 {
			return errJournalTargetUncommitted
		}
		return nil
	}
	expected := head.CoverageFloor + 1
	reachable := map[string]bool{head.CoverageHash: false}
	current := map[string]bool{}
	cursor := ""
	lastName := ""
scan:
	for {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		names, next, err := s.deletionJournal.ListEvents(bounded, cursor)
		stop()
		if err != nil || len(names) > 250 {
			return errors.New("journal coverage unavailable")
		}
		for _, name := range names {
			if !strings.HasPrefix(name, "journal/events/") || name <= lastName {
				return errors.New("journal event name invalid")
			}
			lastName = name
			key := strings.TrimPrefix(name, "journal/")
			var sequence int64
			if _, err := fmt.Sscanf(strings.TrimPrefix(key, "events/"), "%020d-", &sequence); err != nil {
				return errors.New("journal event sequence invalid")
			}
			if sequence <= head.CoverageFloor {
				continue
			}
			if sequence > head.Sequence {
				break scan
			}
			if sequence > expected {
				if sequence != expected+1 || len(current) == 0 {
					return errors.New("journal coverage gap or conflict")
				}
				reachable, current = current, map[string]bool{}
				expected = sequence
			}
			if sequence != expected {
				return errors.New("journal coverage gap or conflict")
			}
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			data, readErr := s.deletionJournal.ReadEvent(bounded, key)
			stop()
			if readErr != nil || len(data) > 4<<10 {
				continue
			}
			var event deletionEvent
			if json.Unmarshal(data, &event) != nil || event.Format != 1 || event.Sequence != sequence || key != fmt.Sprintf("events/%020d-%s", sequence, event.EventID) {
				continue
			}
			bare := event
			bare.Hash = ""
			encoded, _ := json.Marshal(bare)
			digest := sha256.Sum256(encoded)
			if event.Hash != hex.EncodeToString(digest[:]) {
				continue
			}
			if included, ok := reachable[event.PreviousHash]; ok {
				current[event.Hash] = included || sequence == targetSeq && event.Hash == targetHash
				if len(current) > 2048 {
					return errors.New("journal event fanout exceeds bound")
				}
			}
		}
		if next == "" {
			break
		}
		if next <= cursor {
			return errors.New("journal cursor did not advance")
		}
		cursor = next
	}
	included, ok := current[head.Hash]
	if !ok || expected != head.Sequence {
		return errors.New("journal head hash not covered")
	}
	if targetSeq != 0 && !included {
		return errJournalTargetUncommitted
	}
	return nil
}

func (s *Server) eventFor(ctx context.Context, x pendingDeletion, h JournalHead) (deletionEvent, []byte, error) {
	if x.Sequence != nil {
		var event deletionEvent
		if json.Unmarshal(x.Data, &event) != nil || len(x.Data) > 4<<10 || event.Format != 1 || event.AccountID != x.Account || event.EventID != x.ID || event.Sequence != *x.Sequence || event.Hash != hex.EncodeToString(x.Hash) || x.Generation == nil || h.MaintenanceGeneration != *x.Generation {
			return event, nil, errors.New("prepared deletion event inconsistent")
		}
		unhashed := event
		unhashed.Hash = ""
		encoded, _ := json.Marshal(unhashed)
		sum := sha256.Sum256(encoded)
		if event.Hash != hex.EncodeToString(sum[:]) {
			return event, nil, errors.New("prepared deletion event hash invalid")
		}
		return event, x.Data, nil
	}
	if h.Sequence == int64(^uint64(0)>>1) {
		return deletionEvent{}, nil, errors.New("journal sequence exhausted")
	}
	event, data, digest := buildDeletionEvent(x, h)
	if len(data) > 4<<10 {
		return event, nil, errors.New("deletion event too large")
	}
	tag, err := s.db.Exec(ctx, `UPDATE public.deletion_operations SET event_sequence=$2,event_hash=$3,event_data=$4,journal_generation=$5 WHERE id=$1 AND event_sequence IS NULL AND completed_at IS NULL`, x.ID, event.Sequence, digest[:], data, h.MaintenanceGeneration)
	if err != nil || tag.RowsAffected() != 1 {
		return event, nil, errors.New("deletion event preparation uncertain")
	}
	return event, data, nil
}

func buildDeletionEvent(x pendingDeletion, h JournalHead) (deletionEvent, []byte, [32]byte) {
	event := deletionEvent{Format: 1, AccountID: x.Account, EventID: x.ID, Sequence: h.Sequence + 1, PreviousHash: h.Hash, AdmittedAt: x.Admitted.UTC()}
	bare, _ := json.Marshal(event)
	digest := sha256.Sum256(bare)
	event.Hash = hex.EncodeToString(digest[:])
	data, _ := json.Marshal(event)
	return event, data, digest
}

// A failed conditional head replacement may leave an immutable orphan upload.
// Once a later committed head proves that sequence belongs to another event,
// the same durable deletion operation can prepare a new event on that head.
func (s *Server) rebaseDeletionEvent(ctx context.Context, x pendingDeletion, h JournalHead) (deletionEvent, []byte, error) {
	if x.Sequence == nil || x.Generation == nil || *x.Generation != h.MaintenanceGeneration || h.Sequence == int64(^uint64(0)>>1) {
		return deletionEvent{}, nil, errors.New("deletion event cannot rebase")
	}
	event, data, digest := buildDeletionEvent(x, h)
	if len(data) > 4<<10 {
		return deletionEvent{}, nil, errors.New("deletion event too large")
	}
	tag, err := s.db.Exec(ctx, `UPDATE public.deletion_operations SET event_sequence=$2,event_hash=$3,event_data=$4,journal_generation=$5 WHERE id=$1 AND event_sequence=$6 AND event_hash=$7 AND journal_generation=$8 AND completed_at IS NULL`, x.ID, event.Sequence, digest[:], data, h.MaintenanceGeneration, *x.Sequence, x.Hash, *x.Generation)
	if err != nil || tag.RowsAffected() != 1 {
		return deletionEvent{}, nil, errors.New("deletion event rebase uncertain")
	}
	return event, data, nil
}

func (s *Server) proveEvent(ctx context.Context, key string, data []byte) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	existing, err := s.deletionJournal.ReadEvent(bounded, key)
	if err != nil || len(existing) > 4<<10 || !bytes.Equal(existing, data) {
		return errors.New("deletion event absent or conflicting")
	}
	return nil
}

// ReconcileOneDeletion handles one due operation. The mutex is the single
// append worker; every object call occurs outside a database transaction.
func (s *Server) ReconcileOneDeletion(parent context.Context) error {
	if s.deletionJournal == nil {
		return errors.New("journal unavailable")
	}
	s.deletionWorker.Lock()
	defer s.deletionWorker.Unlock()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	x, err := s.nextDeletion(ctx)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	err = s.reconcileDeletion(ctx, x)
	if err != nil {
		s.deletionRetried.Add(1)
		// Durable backoff is fair across pending operations and cannot be bypassed
		// by manual calls to this same entry point.
		retryCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_, _ = s.db.Exec(retryCtx, `UPDATE public.deletion_operations SET attempts=attempts+1,next_attempt_at=clock_timestamp()+make_interval(secs=>$2) WHERE id=$1 AND completed_at IS NULL`, x.ID, retrySeconds(x.ID, x.Attempts))
		stop()
	} else {
		s.deletionCompleted.Add(1)
	}
	return err
}

// MaintainDeletions is the one process-local journal worker. The durable due
// timestamp drives retries across restarts; errors do not stop later accounts.
func (s *Server) MaintainDeletions(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.ReconcileOneDeletion(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func retrySeconds(id string, attempt int) float64 {
	// Stable half-to-full jitter avoids process-global RNG and identifier logs.
	n := sha256.Sum256([]byte(id + strconv.Itoa(attempt)))
	choices := []int{1, 2, 4, 8, 16, 30, 60}
	if attempt >= len(choices) {
		attempt = len(choices) - 1
	}
	return float64(choices[attempt]) * (0.5 + float64(n[1])/510)
}

func (s *Server) reconcileDeletion(ctx context.Context, x pendingDeletion) error {
	head, etag, err := s.readHead(ctx)
	if err != nil {
		return err
	}
	event, data, err := s.eventFor(ctx, x, head)
	if err != nil {
		return err
	}
	if head.Sequence >= event.Sequence {
		err = s.verifyCommittedChain(ctx, head, event.Sequence, event.Hash)
		if err == nil {
			if err = s.proveEvent(ctx, fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID), data); err != nil {
				return err
			}
			return s.finalizeDeletion(ctx, x)
		}
		if !errors.Is(err, errJournalTargetUncommitted) {
			return err
		}
		event, data, err = s.rebaseDeletionEvent(ctx, x, head)
		if err != nil {
			return err
		}
	}
	if head.Sequence != event.Sequence-1 || head.Hash != event.PreviousHash || (x.Generation != nil && head.MaintenanceGeneration != *x.Generation) {
		return errors.New("journal chain or maintenance generation changed")
	}
	key := fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID)
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	_ = s.deletionJournal.CreateEvent(bounded, key, data)
	stop()
	if err = s.proveEvent(ctx, key, data); err != nil {
		return err
	}
	next := head
	next.Sequence = event.Sequence
	next.Hash = event.Hash
	bounded, stop = context.WithTimeout(ctx, 5*time.Second)
	replaceErr := s.deletionJournal.ReplaceHead(bounded, etag, next)
	stop()
	actual, _, readErr := s.readHead(ctx)
	if readErr != nil || actual.Sequence != next.Sequence || actual.Hash != next.Hash || actual.MaintenanceGeneration != next.MaintenanceGeneration || actual.RepositoryID != next.RepositoryID || actual.CoverageFloor != next.CoverageFloor {
		if replaceErr != nil {
			return errors.New("journal head acknowledgement unresolved")
		}
		return errors.New("journal head proof failed")
	}
	return s.finalizeDeletion(ctx, x)
}

func (s *Server) finalizeDeletion(ctx context.Context, x pendingDeletion) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var subject *string
	var deleted *time.Time
	if err = tx.QueryRow(ctx, `SELECT subject,deleted_at FROM public.accounts WHERE id=$1 FOR UPDATE`, x.Account).Scan(&subject, &deleted); err != nil {
		return err
	}
	if deleted != nil {
		return errors.New("deletion operation/account state inconsistent")
	}
	if subject == nil {
		return errors.New("deleting account identity missing")
	}
	var completed *time.Time
	if err = tx.QueryRow(ctx, `SELECT completed_at FROM public.deletion_operations WHERE id=$1 AND account_id=$2 FOR UPDATE`, x.ID, x.Account).Scan(&completed); err != nil || completed != nil {
		return errors.New("deletion operation changed")
	}
	var week time.Time
	var committed int64
	if err = tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1 AND week_start=$2`, x.Account, week).Scan(&committed)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == nil {
		digest := s.digest(s.CurrentPepper, "retained-quota", googleIssuer+"\x00"+*subject)
		if _, err = tx.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes) VALUES($1,$2,$3,$4)`, s.CurrentPepper, digest, week, committed); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE public.accounts SET issuer=NULL,subject=NULL,email=NULL,email_verified=NULL,hosted_domain=NULL,plan_id=NULL,weekly_bytes_override=NULL,sessions_override=NULL,status='Pending',deleted_at=clock_timestamp(),credential_generation=credential_generation+1 WHERE id=$1`, x.Account); err != nil {
		return err
	}
	auditID, err := uuid()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'account_deleted',$2)`, auditID, x.Account); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.deletion_operations SET completed_at=clock_timestamp() WHERE id=$1`, x.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=pending-1 WHERE singleton=true`); err != nil {
		return err
	}
	if s.commitDeletion != nil {
		err = s.commitDeletion(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err == nil {
		return nil
	}
	checkCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	var complete bool
	checkErr := s.db.QueryRow(checkCtx, `SELECT d.completed_at IS NOT NULL AND a.deleted_at IS NOT NULL AND EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=a.id AND e.event='account_deleted') FROM public.deletion_operations d JOIN public.accounts a ON a.id=d.account_id WHERE d.id=$1`, x.ID).Scan(&complete)
	if checkErr == nil && complete {
		return nil
	}
	return errors.New("deletion finalization uncertain")
}

// importRetainedUsage runs inside the fresh Pending account's creation
// transaction. Every configured key is checked, and absent historical keys
// block registration while any current-week retained record needs one.
func (s *Server) importRetainedUsage(ctx context.Context, tx pgx.Tx, accountID, subject string) error {
	var week time.Time
	if err := tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage WHERE week_start=$1`, week)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version uint64
		if rows.Scan(&version) != nil || len(s.Peppers[version]) == 0 {
			rows.Close()
			return errors.New("retained usage key unavailable")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var matches int
	var committed int64
	var matchVersion uint64
	var matchDigest []byte
	for version := range s.Peppers {
		digest := s.digest(version, "retained-quota", googleIssuer+"\x00"+subject)
		var amount int64
		err = tx.QueryRow(ctx, `SELECT committed_bytes FROM public.retained_quota_usage WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3 FOR UPDATE`, version, digest, week).Scan(&amount)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		matches++
		committed = amount
		matchVersion = version
		matchDigest = digest
	}
	if matches > 1 {
		return errors.New("duplicate logical retained usage")
	}
	if matches == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes) VALUES($1,$2,$3,0,NULL,0)`, accountID, week, committed); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3`, matchVersion, matchDigest, week)
	return err
}

// DeletionSignals are bounded aggregate observations with no identity labels.
type DeletionSignals struct {
	Pending            int
	OldestAge          time.Duration
	Warning, Critical  bool
	Completed, Retried uint64
}

func (s *Server) SampleDeletionSignals(ctx context.Context) (DeletionSignals, error) {
	var count int
	var ageSeconds float64
	if err := s.db.QueryRow(ctx, `SELECT count(*),COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(admitted_at)),0) FROM public.deletion_operations WHERE completed_at IS NULL`).Scan(&count, &ageSeconds); err != nil {
		return DeletionSignals{}, err
	}
	out := DeletionSignals{Pending: count, Completed: s.deletionCompleted.Load(), Retried: s.deletionRetried.Load()}
	if ageSeconds > 0 {
		out.OldestAge = time.Duration(ageSeconds * float64(time.Second))
	}
	out.Warning = out.OldestAge >= 15*time.Minute
	out.Critical = out.OldestAge >= time.Hour || count >= 820
	return out, nil
}
