package auth

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
)

// CleanupSignals expose age and outcomes without account or credential labels.
type CleanupSignals struct {
	OldestAge                 time.Duration
	Warning, Critical, Breach bool
	Completed, Failed         uint64
	BreachEpisodes            uint64
}

func (s *Server) SampleCleanupSignals(ctx context.Context) (CleanupSignals, error) {
	health, err := s.maintenance.CleanupHealth(ctx)
	if err != nil {
		return CleanupSignals{}, err
	}
	out := CleanupSignals{OldestAge: health.OldestAge, Completed: s.cleanupCompleted.Load(), Failed: s.cleanupFailed.Load(), BreachEpisodes: health.BreachEpisodes}
	out.Warning = out.OldestAge > time.Hour
	out.Critical = out.OldestAge >= 12*time.Hour
	out.Breach = out.OldestAge >= 24*time.Hour
	return out, nil
}

// The aggregate episode counter and timestamps survive process restarts. No
// account, credential, or arbitrary error text is written to this record.
func (s *Server) recordCleanupBreach(ctx context.Context) error {
	signals, err := s.SampleCleanupSignals(ctx)
	if err != nil {
		return err
	}
	return s.maintenance.RecordCleanupBreach(ctx, signals.Breach, int64(signals.OldestAge/time.Second))
}

const cleanupBatchSize = 250
const cleanupOperationCount = 19
const cleanupParentStart = cleanupOperationCount - 2

type cleanupStep struct {
	operation persistence.CleanupOperation
	limit     int64
	fair      bool
}

var cleanupPlan = []cleanupStep{
	{operation: persistence.CleanupRetainedUsage, limit: cleanupBatchSize},
	{operation: persistence.CleanupWeeklyUsage, limit: cleanupBatchSize},
	{operation: persistence.CleanupAuthorizationCodes, limit: cleanupBatchSize},
	{operation: persistence.CleanupAccessTokens, limit: cleanupBatchSize},
	{operation: persistence.CleanupTerminatedAccessTokens, limit: cleanupBatchSize},
	{operation: persistence.CleanupRefreshGenerations, limit: cleanupBatchSize},
	{operation: persistence.CleanupLoginGrants, limit: cleanupBatchSize},
	{operation: persistence.CleanupIdlePortalSessions, limit: cleanupBatchSize},
	{operation: persistence.CleanupAbsolutePortalSessions, limit: cleanupBatchSize},
	{operation: persistence.CleanupHiddenAuthorizationCodes, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupHiddenAccessTokens, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupHiddenRefreshGenerations, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupHiddenLoginGrants, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupHiddenPortalSessions, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupHiddenWeeklyUsage, limit: cleanupBatchSize, fair: true},
	{operation: persistence.CleanupDeletionOperations, limit: cleanupBatchSize},
	{operation: persistence.CleanupAuditEvents, limit: cleanupBatchSize},
	{operation: persistence.CleanupDeletedAccounts, limit: cleanupBatchSize},
	{operation: persistence.CleanupExpiredPendingAccounts, limit: cleanupBatchSize / 2},
}

func (s *Server) cleanupPass(ctx context.Context) (bool, error) {
	more := false
	var firstError error
	process := func(index int, step cleanupStep, deadline time.Duration) {
		work, cancel := context.WithTimeout(ctx, deadline)
		cursor := ""
		if step.fair {
			cursor = s.cleanupAccountCursors[index]
			if cursor == "" {
				cursor = "00000000-0000-0000-0000-000000000000"
			}
		}
		result, err := s.maintenance.CleanupBatch(work, persistence.CleanupBatch{Operation: step.operation, Limit: step.limit, Cursor: cursor})
		cancel()
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			return
		}
		if step.fair {
			s.cleanupAccountCursors[index] = result.NextCursor
			if result.Removed == step.limit || result.Candidates == 16 || (result.Removed > 0 && result.Removed >= result.Take) {
				more = true
			}
			return
		}
		if result.Removed == step.limit {
			more = true
		}
	}
	// Rotate independent and child operations; parent deletion always follows.
	// Each adapter call remains a separately bounded set-based batch.
	children := cleanupParentStart
	start := int(s.cleanupCursor.Add(1)-1) % children
	for offset := 0; offset < children; offset++ {
		if err := ctx.Err(); err != nil {
			return more, err
		}
		index := (start + offset) % children
		process(index, cleanupPlan[index], 1250*time.Millisecond)
	}
	for index := cleanupParentStart; index < len(cleanupPlan); index++ {
		if err := ctx.Err(); err != nil {
			return more, err
		}
		process(index, cleanupPlan[index], 2*time.Second)
	}
	return more, firstError
}
func (s *Server) Maintain(ctx context.Context) {
	if !s.cleanupWorker.CompareAndSwap(false, true) {
		return
	}
	defer s.cleanupWorker.Store(false)
	if s.deletionJournal != nil {
		go s.MaintainDeletions(ctx)
	}
	failedPreviously := false
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		observation, cancelObservation := context.WithTimeout(ctx, 3*time.Second)
		observationError := s.recordCleanupBreach(observation)
		cancelObservation()
		pass, cancel := context.WithTimeout(ctx, 30*time.Second)
		more, err := s.cleanupPass(pass)
		cancel()
		observation, cancelObservation = context.WithTimeout(ctx, 3*time.Second)
		if afterError := s.recordCleanupBreach(observation); observationError == nil {
			observationError = afterError
		}
		cancelObservation()
		if err == nil && observationError == nil {
			s.cleanupCompleted.Add(1)
			if failedPreviously && s.diagnostics != nil {
				s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.cleanup", Outcome: "success", Reason: "none", Duration: time.Since(started)})
			}
			failedPreviously = false
		} else if ctx.Err() == nil {
			s.cleanupFailed.Add(1)
			if s.diagnostics != nil {
				reason := "database_unavailable"
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(observationError, context.DeadlineExceeded) {
					reason = "deadline"
				}
				s.diagnostics.Emit(diagnostic.Event{Event: "maintenance.cleanup", Outcome: "failed", Reason: reason, Duration: time.Since(started)})
			}
			failedPreviously = true
		}
		if ctx.Err() != nil {
			return
		}
		delay := time.Minute
		if err != nil || observationError != nil {
			delay = time.Minute
		} else if more {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
