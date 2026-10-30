package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
	"clipp-relay/internal/recoveryjournal"
)

// DeletionJournal and JournalHead remain compatibility names for the shared
// Recovery Journal storage contract.
type DeletionJournal = recoveryjournal.Store
type JournalHead = recoveryjournal.Head

// SetDeletionFinalizeCommitHookForTest simulates lost finalization responses
// after PostgreSQL has committed; serving code leaves this unset.
func (s *Server) SetDeletionFinalizeCommitHookForTest(commit func(context.Context, persistence.DeletionTransaction) error) {
	s.commitDeletionRepository = commit
}

func (s *Server) SetDeletionAdmissionCommitHookForTest(commit func(context.Context, persistence.DeletionTransaction) error) {
	s.commitDeletionAdmission = commit
}

type uncertainDeletion struct{ operationID string }

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

func (s *Server) deleteOwner(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin {
		s.renderAccountDeletion(w, r, refusedAccountDeletion(http.StatusServiceUnavailable, "origin_rejected"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		s.renderAccountDeletion(w, r, refusedAccountDeletion(http.StatusBadRequest, "csrf_rejected"))
		return
	}
	outcome := s.admitAccountDeletion(r)
	s.renderAccountDeletion(w, r, outcome)
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

func (s *Server) journal() *recoveryjournal.Journal {
	return recoveryjournal.New(s.deletionJournal, s.deletionRepository, s.deletionFloor, s.deletionFloorHash)
}

func (s *Server) readHead(ctx context.Context) (result JournalHead, resultETag string, resultErr error) {
	started := time.Now()
	defer func() { s.transitions.Dependency("dependency.deletion_journal", resultErr, ctx, time.Since(started)) }()
	return s.journal().ReadHead(ctx)
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

func (s *Server) committedDeletionAccounts(ctx context.Context, head JournalHead, targetSeq int64, targetHash string, collect bool) ([]string, error) {
	return s.journal().Verify(ctx, head, targetSeq, targetHash, collect)
}

func recordedDeletionEvent(x pendingDeletion, generation int64) (recoveryjournal.PreparedEvent, error) {
	if x.Sequence == nil || x.Generation == nil || *x.Generation != generation || len(x.Data) > 4<<10 {
		return recoveryjournal.PreparedEvent{}, errors.New("prepared deletion event inconsistent")
	}
	var event recoveryjournal.Event
	if err := json.Unmarshal(x.Data, &event); err != nil || event.Format != 1 || event.AccountID != x.Account || event.EventID != x.ID || event.Sequence != *x.Sequence || event.Hash != hex.EncodeToString(x.Hash) {
		return recoveryjournal.PreparedEvent{}, errors.New("prepared deletion event inconsistent")
	}
	prepared := recoveryjournal.PreparedEvent{Event: event, Data: x.Data}
	if err := recoveryjournal.ValidatePrepared(prepared); err != nil {
		return recoveryjournal.PreparedEvent{}, err
	}
	return prepared, nil
}

// prepareDeletionEvent persists the immutable operation preparation before
// Append begins upload or head I/O. On a proven lost sequence it conditionally
// replaces that preparation in the same existing deletion-operation row.
func (s *Server) prepareDeletionEvent(ctx context.Context, x pendingDeletion, head JournalHead, prior *recoveryjournal.PreparedEvent) (recoveryjournal.PreparedEvent, error) {
	if prior != nil {
		recorded, err := recordedDeletionEvent(x, head.MaintenanceGeneration)
		if err != nil || recorded.Event != prior.Event || !bytes.Equal(recorded.Data, prior.Data) {
			return recoveryjournal.PreparedEvent{}, errors.New("deletion event preparation changed")
		}
		prepared, err := recoveryjournal.NewEvent(x.Account, x.ID, x.Admitted, head)
		if err != nil {
			return recoveryjournal.PreparedEvent{}, err
		}
		digest, _ := hex.DecodeString(prepared.Event.Hash)
		updated, err := s.deletions.PrepareDeletionEvent(ctx, persistence.DeletionEventPreparation{OperationID: x.ID, Sequence: prepared.Event.Sequence, Hash: digest, Data: prepared.Data, Generation: head.MaintenanceGeneration, ExpectedSequence: x.Sequence, ExpectedHash: x.Hash, ExpectedGeneration: x.Generation})
		if err != nil {
			return recoveryjournal.PreparedEvent{}, deletionPrepareError(err)
		}
		if !updated {
			return recoveryjournal.PreparedEvent{}, errors.New("deletion event rebase uncertain")
		}
		return prepared, nil
	}
	if x.Sequence != nil {
		return recordedDeletionEvent(x, head.MaintenanceGeneration)
	}
	prepared, err := recoveryjournal.NewEvent(x.Account, x.ID, x.Admitted, head)
	if err != nil {
		return recoveryjournal.PreparedEvent{}, err
	}
	digest, _ := hex.DecodeString(prepared.Event.Hash)
	updated, err := s.deletions.PrepareDeletionEvent(ctx, persistence.DeletionEventPreparation{OperationID: x.ID, Sequence: prepared.Event.Sequence, Hash: digest, Data: prepared.Data, Generation: head.MaintenanceGeneration})
	if err != nil {
		return recoveryjournal.PreparedEvent{}, deletionPrepareError(err)
	}
	if !updated {
		return recoveryjournal.PreparedEvent{}, errors.New("deletion event preparation uncertain")
	}
	return prepared, nil
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
	_, err := s.journal().Append(ctx, nil, func(prepareCtx context.Context, head JournalHead, prior *recoveryjournal.PreparedEvent) (recoveryjournal.PreparedEvent, error) {
		return s.prepareDeletionEvent(prepareCtx, x, head, prior)
	})
	if err != nil {
		if errors.Is(err, errDeletionPreparationUnconfirmed) || errors.Is(err, errDeletionDatabaseDiagnostic) {
			return err
		}
		return fmt.Errorf("%w: %v", errDeletionJournalDiagnostic, err)
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
