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

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
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
func (s *Server) SetDeletionFinalizeCommitHookForTest(commit func(context.Context, persistence.DeletionTransaction) error) {
	s.commitDeletionRepository = commit
}

func (s *Server) SetDeletionAdmissionCommitHookForTest(commit func(context.Context, persistence.DeletionTransaction) error) {
	s.commitDeletionAdmission = commit
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
var errDeletionJournalDiagnostic = errors.New("deletion journal failed")
var errDeletionFinalizationUnconfirmed = errors.New("deletion finalization uncertain")
var errDeletionPreparationUnconfirmed = errors.New("deletion preparation uncertain")
var errDeletionDatabaseDiagnostic = errors.New("deletion database failed")

func deletionDatabaseError(err error) error {
	return fmt.Errorf("%w: %w", errDeletionDatabaseDiagnostic, err)
}

func deletionPrepareError(err error) error {
	if errors.Is(err, persistence.ErrUncertainCommit) {
		return fmt.Errorf("%w: %w", errDeletionPreparationUnconfirmed, err)
	}
	return deletionDatabaseError(err)
}

// SetDeletionJournal requires an operator-supplied repository identity and
// coverage floor. A nil/unqualified journal leaves deletion unavailable.
func (s *Server) SetDeletionJournal(j DeletionJournal, repository string, floor int64, floorHash string) {
	s.deletionJournal = j
	if j != nil && s.transitions != nil {
		s.deletionJournal = observedJournal{DeletionJournal: j, transitions: s.transitions}
	}
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
	if r.Header.Get("Origin") != s.Origin {
		markDiagnostic(r, "refused", "origin_rejected")
		fail(w, 503)
		return
	}
	if s.deletionJournal == nil || s.deletionRepository == "" {
		markDiagnostic(r, "failed", "journal_unavailable")
		fail(w, 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		markDiagnostic(r, "refused", "csrf_rejected")
		fail(w, 400)
		return
	}
	id, version, _, _, csrf, e := s.session(r)
	if e != nil {
		switch {
		case errors.Is(e, errRateLimit):
			markAction(r, "")
		case errors.Is(e, errInvalidSession):
			markDiagnostic(r, "refused", "invalid_credentials")
		default:
			markDiagnostic(r, "failed", "database_unavailable")
		}
		fail(w, 401)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		markDiagnostic(r, "refused", "csrf_rejected")
		fail(w, 403)
		return
	}
	if _, _, err := s.readHead(r.Context()); err != nil {
		markDiagnostic(r, "failed", "journal_unavailable")
		fail(w, 503)
		return
	}
	if s.beforeOwnerSubjectRead != nil {
		s.beforeOwnerSubjectRead()
	}
	readTx, err := s.deletions.BeginDeletion(r.Context())
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return
	}
	owner, err := readTx.DeletionOwner(r.Context(), id)
	_ = readTx.Rollback(r.Context())
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrInvalidCredential) {
			markDiagnostic(r, "refused", "invalid_credentials")
		} else {
			markDiagnostic(r, "failed", "database_unavailable")
		}
		fail(w, 409)
		return
	}
	subject := owner.Subject
	operation, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	var admitted, uncertain bool
	var invalidationFailed bool
	var completeInvalidation bool
	if s.beforeAccountActionGuard != nil {
		s.beforeAccountActionGuard()
	}
	guardEntered := false
	guardErr := s.withIdentityGuard(r.Context(), subject, func(unitCtx context.Context) error {
		guardEntered = true
		if accountGuardErr := s.withAccountGuards(unitCtx, []string{id}, func(ctx context.Context) {
			tx, err := s.deletions.BeginDeletion(ctx)
			if err != nil {
				fail(w, 503)
				return
			}
			defer tx.Rollback(ctx)
			if s.beforeOwnerDeletionRecheck != nil {
				if err = s.beforeOwnerDeletionRecheck(ctx); err != nil {
					markDiagnostic(r, "failed", "database_unavailable")
					fail(w, 503)
					return
				}
			}
			admission, err := tx.AdmitDeletion(ctx, persistence.DeletionAdmissionRequest{AccountID: id, OperationID: operation, ExpectedSubject: subject, SessionDigest: s.sessionDigest(r, version), PepperVersion: version})
			if errors.Is(err, persistence.ErrInvalidCredential) {
				markDiagnostic(r, "refused", "invalid_credentials")
				fail(w, 401)
				return
			}
			if errors.Is(err, persistence.ErrRecentAuthentication) {
				markDiagnostic(r, "refused", "recent_auth_required")
				portal.RecentAuthenticationRequired(w)
				return
			}
			if errors.Is(err, persistence.ErrCapacityExceeded) {
				markDiagnostic(r, "refused", "deletion_capacity")
				fail(w, 503)
				return
			}
			if err != nil {
				fail(w, 503)
				return
			}
			if s.commitDeletionAdmission != nil {
				err = s.commitDeletionAdmission(ctx, tx)
			} else {
				err = tx.Commit(ctx)
			}
			s.installIdentityFence(subject)
			if err != nil {
				s.uncertainAccounts.Store(id, uncertainDeletion{operationID: operation})
			}
			change := AccountChange{AccountID: id, WeeklyBytes: admission.WeeklyBytes, SessionLimit: admission.Sessions, CloseAll: true}
			reason := s.accountChanged(change)
			if reason == "" {
				reason = "account_access_changed"
			}
			invalidationFailed = s.deliverAccountInvalidation(id, reason) != nil
			completeInvalidation = true
			admitted, uncertain = true, err != nil
		}); accountGuardErr != nil {
			markGuardDiagnostic(r, accountGuardErr)
			fail(w, 503)
		}
		return nil
	})
	if guardErr != nil && !guardEntered {
		markGuardDiagnostic(r, guardErr)
		fail(w, 503)
	}
	if !admitted {
		return
	}
	if s.deletionAccounting != nil {
		s.deletionAccounting.BeginAccountingDeletion(id)
	}
	if completeInvalidation && s.accountInvalidationComplete(id) != nil {
		invalidationFailed = true
	}
	cookie(w, sessionCookie, "", -1)
	if uncertain {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		uncertainResponse(w)
		return
	}
	if invalidationFailed {
		markDiagnostic(r, "failed", "invalidation_incomplete")
		http.Error(w, "Deletion admission committed, but relay session invalidation could not be confirmed.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Location", "/auth/deletion/"+operation)
	w.Header().Set("Retry-After", "5")
	renderDeletion(w, true, "/auth/deletion/"+operation)
}

func (s *Server) deletionStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) {
		fail(w, 404)
		return
	}
	completed, err := s.deletions.DeletionStatus(r.Context(), id)
	if errors.Is(err, persistence.ErrNotFound) {
		fail(w, 404)
		return
	}
	if err != nil {
		fail(w, 503)
		return
	}
	renderDeletion(w, !completed, r.URL.Path)
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
	row, err := s.deletions.NextDeletion(ctx)
	if err != nil {
		return pendingDeletion{}, err
	}
	return pendingDeletion{ID: row.ID, Account: row.AccountID, Admitted: row.AdmittedAt, Sequence: row.EventSequence, Hash: row.EventHash, Data: row.EventData, Generation: row.JournalGeneration, Attempts: row.Attempts}, nil
}

func (s *Server) readHead(ctx context.Context) (result JournalHead, resultETag string, resultErr error) {
	started := time.Now()
	defer func() { s.transitions.Dependency("dependency.deletion_journal", resultErr, ctx, time.Since(started)) }()
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
	return s.verifyRecordedChain(ctx, head, 0, "")
}

// verifyRecordedChain checks a fixed head snapshot from the operator's
// attested floor. An immutable upload is not committed merely because its
// sequence is below the head: failed head comparisons can leave orphan events
// at the same sequence. Only a hash-linked path ending at the head is proof.
// OCI pages and the reachable set are bounded; the caller bounds total work.
func (s *Server) verifyRecordedChain(ctx context.Context, head JournalHead, targetSeq int64, targetHash string) error {
	_, err := s.committedDeletionAccounts(ctx, head, targetSeq, targetHash, false)
	return err
}

type deletionProof struct {
	account  string
	parent   *deletionProof
	included bool
}

func (s *Server) committedDeletionAccounts(ctx context.Context, head JournalHead, targetSeq int64, targetHash string, collect bool) ([]string, error) {
	if head.Sequence == head.CoverageFloor {
		if targetSeq != 0 {
			return nil, errJournalTargetUncommitted
		}
		return nil, nil
	}
	expected := head.CoverageFloor + 1
	reachable := map[string]*deletionProof{head.CoverageHash: {}}
	current := map[string]*deletionProof{}
	cursor := ""
	lastName := ""
scan:
	for {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		names, next, err := s.deletionJournal.ListEvents(bounded, cursor)
		stop()
		if err != nil || len(names) > 250 {
			return nil, errors.New("journal coverage unavailable")
		}
		for _, name := range names {
			if !strings.HasPrefix(name, "journal/events/") || name <= lastName {
				return nil, errors.New("journal event name invalid")
			}
			lastName = name
			key := strings.TrimPrefix(name, "journal/")
			var sequence int64
			if _, err := fmt.Sscanf(strings.TrimPrefix(key, "events/"), "%020d-", &sequence); err != nil {
				return nil, errors.New("journal event sequence invalid")
			}
			if sequence <= head.CoverageFloor {
				continue
			}
			if sequence > head.Sequence {
				break scan
			}
			if sequence > expected {
				if sequence != expected+1 || len(current) == 0 {
					return nil, errors.New("journal coverage gap or conflict")
				}
				reachable, current = current, map[string]*deletionProof{}
				expected = sequence
			}
			if sequence != expected {
				return nil, errors.New("journal coverage gap or conflict")
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
				node := &deletionProof{included: included.included || sequence == targetSeq && event.Hash == targetHash}
				if collect {
					node.account = event.AccountID
					node.parent = included
				}
				current[event.Hash] = node
				if len(current) > 2048 {
					return nil, errors.New("journal event fanout exceeds bound")
				}
			}
		}
		if next == "" {
			break
		}
		if next <= cursor {
			return nil, errors.New("journal cursor did not advance")
		}
		cursor = next
	}
	included, ok := current[head.Hash]
	if !ok || expected != head.Sequence {
		return nil, errors.New("journal head hash not covered")
	}
	if targetSeq != 0 && !included.included {
		return nil, errJournalTargetUncommitted
	}
	accounts := []string{}
	for node := included; node != nil; node = node.parent {
		if node.account != "" {
			accounts = append(accounts, node.account)
		}
	}
	return accounts, nil
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
	updated, err := s.deletions.PrepareDeletionEvent(ctx, persistence.DeletionEventPreparation{OperationID: x.ID, Sequence: event.Sequence, Hash: digest[:], Data: data, Generation: h.MaintenanceGeneration})
	if err != nil {
		return event, nil, deletionPrepareError(err)
	}
	if !updated {
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
	updated, err := s.deletions.PrepareDeletionEvent(ctx, persistence.DeletionEventPreparation{OperationID: x.ID, Sequence: event.Sequence, Hash: digest[:], Data: data, Generation: h.MaintenanceGeneration, ExpectedSequence: x.Sequence, ExpectedHash: x.Hash, ExpectedGeneration: x.Generation})
	if err != nil {
		return deletionEvent{}, nil, deletionPrepareError(err)
	}
	if !updated {
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
	if errors.Is(err, persistence.ErrNotFound) {
		return nil
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		if s.diagnostics != nil {
			reason := "database_unavailable"
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "deadline"
			}
			s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.deletion", Outcome: "failed", Reason: reason})
		}
		return err
	}
	started := time.Now()
	if s.diagnostics != nil {
		s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.deletion", Outcome: "started", Reason: "none"})
	}
	err = s.reconcileDeletion(ctx, x)
	if err != nil {
		if s.diagnostics != nil {
			outcome, reason := "", ""
			switch {
			case errors.Is(ctx.Err(), context.Canceled):
				// Ordinary worker shutdown leaves the durable operation pending.
			case errors.Is(err, errDeletionFinalizationUnconfirmed), errors.Is(err, errDeletionPreparationUnconfirmed):
				outcome, reason = "uncertain", "commit_unconfirmed"
			case errors.Is(err, context.DeadlineExceeded):
				outcome, reason = "failed", "deadline"
			case errors.Is(err, errDeletionJournalDiagnostic):
				outcome, reason = "failed", "journal_unavailable"
			case errors.Is(err, errDeletionDatabaseDiagnostic):
				outcome, reason = "failed", "database_unavailable"
			}
			if outcome != "" {
				s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.deletion", Outcome: outcome, Reason: reason, Duration: time.Since(started)})
			}
		}
		s.deletionRetried.Add(1)
		// Durable backoff is fair across pending operations and cannot be bypassed
		// by manual calls to this same entry point.
		retryCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = s.deletions.RecordDeletionRetry(retryCtx, x.ID, x.Attempts, retrySeconds(x.ID, x.Attempts))
		stop()
	} else {
		s.deletionCompleted.Add(1)
		// This is the proven finalization boundary, including a lost commit
		// acknowledgement that finalizeDeletion independently verified.
		if s.diagnostics != nil {
			s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.deletion", Outcome: "success", Reason: "none", Duration: time.Since(started)})
		}
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
		return fmt.Errorf("%w: %v", errDeletionJournalDiagnostic, err)
	}
	event, data, err := s.eventFor(ctx, x, head)
	if err != nil {
		return err
	}
	if head.Sequence >= event.Sequence {
		err = s.verifyRecordedChain(ctx, head, event.Sequence, event.Hash)
		if err == nil {
			if err = s.proveEvent(ctx, fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID), data); err != nil {
				return fmt.Errorf("%w: %v", errDeletionJournalDiagnostic, err)
			}
			return s.finalizeDeletion(ctx, x)
		}
		if !errors.Is(err, errJournalTargetUncommitted) {
			return fmt.Errorf("%w: %v", errDeletionJournalDiagnostic, err)
		}
		event, data, err = s.rebaseDeletionEvent(ctx, x, head)
		if err != nil {
			return err
		}
	}
	if head.Sequence != event.Sequence-1 || head.Hash != event.PreviousHash || (x.Generation != nil && head.MaintenanceGeneration != *x.Generation) {
		return fmt.Errorf("%w: journal chain or maintenance generation changed", errDeletionJournalDiagnostic)
	}
	key := fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID)
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	_ = s.deletionJournal.CreateEvent(bounded, key, data)
	stop()
	if err = s.proveEvent(ctx, key, data); err != nil {
		return fmt.Errorf("%w: %v", errDeletionJournalDiagnostic, err)
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
			return fmt.Errorf("%w: journal head acknowledgement unresolved", errDeletionJournalDiagnostic)
		}
		return fmt.Errorf("%w: journal head proof failed", errDeletionJournalDiagnostic)
	}
	return s.finalizeDeletion(ctx, x)
}

func (s *Server) finalizeDeletion(ctx context.Context, x pendingDeletion) error {
	// Settlement acquires Auth account protection itself. Attempt it before any
	// finalization row lock, and reserve the remaining budget for deletion even
	// when pending usage cannot be preserved. Journal proof precedes this call.
	if s.deletionAccounting != nil {
		flushCtx, stop := context.WithTimeout(ctx, time.Second)
		_ = s.deletionAccounting.FlushDeletionAccounting(flushCtx, x.Account)
		stop()
	}
	subject, err := s.deletions.DeletionIdentity(ctx, x.Account)
	if err != nil {
		return deletionDatabaseError(err)
	}
	auditID, err := uuid()
	if err != nil {
		return err
	}
	digest := s.digest(s.CurrentPepper, "retained-quota", googleIssuer+"\x00"+subject)
	tx, err := s.deletions.BeginDeletion(ctx)
	if err != nil {
		return deletionDatabaseError(err)
	}
	defer tx.Rollback(ctx)
	err = tx.FinalizeDeletion(ctx, x.ID, x.Account, subject, s.CurrentPepper, digest, auditID)
	if err != nil {
		return deletionDatabaseError(err)
	}
	if s.commitDeletionRepository != nil {
		err = s.commitDeletionRepository(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err == nil {
		s.forgetDeletedAccounting(x.Account)
		return nil
	}
	checkCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	complete, checkErr := s.deletions.DeletionCommitted(checkCtx, x.ID)
	if checkErr == nil && complete {
		s.forgetDeletedAccounting(x.Account)
		return nil
	}
	return errDeletionFinalizationUnconfirmed
}

func (s *Server) forgetDeletedAccounting(account string) {
	if s.deletionAccounting != nil {
		s.deletionAccounting.ForgetAccounting(account)
	}
}

// DeletionSignals are bounded aggregate observations with no identity labels.
type DeletionSignals struct {
	Pending            int
	OldestAge          time.Duration
	Warning, Critical  bool
	Completed, Retried uint64
}

func (s *Server) SampleDeletionSignals(ctx context.Context) (DeletionSignals, error) {
	read, err := s.deletions.DeletionSignals(ctx)
	if err != nil {
		return DeletionSignals{}, err
	}
	out := DeletionSignals{Pending: read.Pending, OldestAge: read.OldestAge, Completed: s.deletionCompleted.Load(), Retried: s.deletionRetried.Load()}
	out.Warning = out.OldestAge >= 15*time.Minute
	out.Critical = out.OldestAge >= time.Hour || out.Pending >= 820
	return out, nil
}

func renderDeletion(w http.ResponseWriter, pending bool, statusURL string) {
	status := http.StatusOK
	if pending {
		w.Header().Set("Retry-After", "5")
		status = http.StatusAccepted
	}
	portal.Render(w, status, "deletion", struct {
		Pending   bool
		StatusURL string
	}{pending, statusURL})
}
