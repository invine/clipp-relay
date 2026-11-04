package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"clipp-relay/internal/persistence"
)

// RecoveryRequest identifies one isolated restore. Target must be the actual
// database name; Source is immutable backup identity, distinct from Target.
type RecoveryRequest struct{ RunID, Source, Target string }

type recoveryQuota interface {
	ValidateRetainedUsageKeyCoverage([]uint64) error
	RetainRecoveredUsage(context.Context, persistence.RecoveryAccountingTransaction, string, string, string) error
}

func (s *Server) SetRecoveryCommitHookForTest(commit func(context.Context, persistence.RecoveryTransaction) error) {
	s.commitRecovery = commit
}

// RecoverOffline proves the independent journal before opening a repository
// transaction. Recovery Journal I/O never occurs inside SQL locks.
func (s *Server) RecoverOffline(ctx context.Context, request RecoveryRequest) error {
	if !idPattern.MatchString(request.RunID) || strings.TrimSpace(request.Source) == "" || len(request.Source) > 255 || request.Source == request.Target {
		return errors.New("invalid recovery identity")
	}
	target, err := s.recovery.CurrentDatabase(ctx)
	if err != nil {
		return err
	}
	if target != request.Target {
		return errors.New("recovery target mismatch")
	}
	completed, err := s.recoveryCompleted(ctx, request)
	if err != nil || completed {
		return err
	}
	head, etag, err := s.readHead(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.committedDeletionAccounts(ctx, head, 0, "", true)
	if err != nil {
		return err
	}
	current, currentTag, err := s.readHead(ctx)
	if err != nil || current != head || currentTag != etag {
		return errors.New("recovery journal snapshot changed")
	}
	// The independent writer fence is an operator prerequisite. SQL work uses
	// the ordinary persistence runtime budget regardless of command envelope.
	tx, err := s.recovery.BeginRecovery(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = tx.LockRecovery(ctx); err != nil {
		return err
	}
	run, exists, err := tx.FindRecoveryRun(ctx, request.RunID)
	if err != nil {
		return err
	}
	if exists {
		return s.validateRecoveryRun(run, request)
	}
	if err = tx.LockRecoveryAccounts(ctx); err != nil {
		return err
	}
	if err = tx.InvalidateRestoredCredentials(ctx); err != nil {
		return err
	}
	versions, err := tx.RetainedPepperVersions(ctx)
	if err != nil {
		return err
	}
	quotaOwner, ok := s.deletionAccounting.(recoveryQuota)
	if !ok {
		return ErrRetainedPepperUnavailable
	}
	if err = quotaOwner.ValidateRetainedUsageKeyCoverage(versions); err != nil {
		return ErrRetainedPepperUnavailable
	}
	for _, id := range accounts {
		if err = s.recoverDeletedAccount(ctx, tx, quotaOwner, id); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(head)
	if err != nil {
		return err
	}
	if err = tx.RecordRecoveryRun(ctx, persistence.RecoveryRun{RunID: request.RunID, Source: request.Source, Target: request.Target, SchemaRevision: s.recovery.SchemaRevision(), JournalHead: encoded}); err != nil {
		return err
	}
	if s.commitRecovery != nil {
		err = s.commitRecovery(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if done, checkErr := s.recoveryCompleted(checkCtx, request); checkErr == nil && done {
		return nil
	}
	return fmt.Errorf("recovery completion uncertain: %w", err)
}

func (s *Server) validateRecoveryRun(run persistence.RecoveryRun, r RecoveryRequest) error {
	if run.Source != r.Source || run.Target != r.Target || run.SchemaRevision != s.recovery.SchemaRevision() {
		return errors.New("recovery run identity conflict")
	}
	return nil
}
func (s *Server) recoveryCompleted(ctx context.Context, r RecoveryRequest) (bool, error) {
	run, found, err := s.recovery.FindRecoveryRun(ctx, r.RunID)
	if err != nil || !found {
		return false, err
	}
	if err = s.validateRecoveryRun(run, r); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) recoverDeletedAccount(ctx context.Context, tx persistence.RecoveryTransaction, quotaOwner recoveryQuota, id string) error {
	account, found, err := tx.RecoveredAccount(ctx, id)
	if err != nil {
		return err
	}
	if !found || account.Deleted {
		return nil
	}
	if account.Subject == "" {
		return errors.New("restored deletion identity missing")
	}
	if err = quotaOwner.RetainRecoveredUsage(ctx, tx, id, googleIssuer, account.Subject); err != nil {
		return err
	}
	auditID, err := uuid()
	if err != nil {
		return err
	}
	return tx.CompleteRecoveredAccount(ctx, id, auditID)
}
