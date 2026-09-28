package auth

import (
	"context"
	"time"
)

// CleanupSignals expose age and outcomes without account or credential labels.
type CleanupSignals struct {
	OldestAge                 time.Duration
	Warning, Critical, Breach bool
	Completed, Failed         uint64
}

func (s *Server) SampleCleanupSignals(ctx context.Context) (CleanupSignals, error) {
	var seconds float64
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-LEAST(
	COALESCE((SELECT min(expires_at) FROM public.authorization_codes WHERE expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(expires_at) FROM public.relay_access_tokens WHERE expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(expires_at) FROM public.authorization_transactions WHERE expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(idle_expires_at) FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(absolute_expires_at) FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(deleted_at) FROM public.accounts WHERE deleted_at IS NOT NULL),clock_timestamp()),
	COALESCE((SELECT min(occurred_at+interval '180 days') FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days'),clock_timestamp()),
	COALESCE((SELECT min(week_start+interval '7 days') FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date),clock_timestamp())
	)),0)`).Scan(&seconds)
	if err != nil {
		return CleanupSignals{}, err
	}
	out := CleanupSignals{OldestAge: time.Duration(seconds * float64(time.Second)), Completed: s.cleanupCompleted.Load(), Failed: s.cleanupFailed.Load()}
	out.Warning = out.OldestAge > time.Hour
	out.Critical = out.OldestAge >= 12*time.Hour
	out.Breach = out.OldestAge >= 24*time.Hour
	return out, nil
}

const cleanupBatchSize = 250

// Each query uses a matching expiry index and removes at most one bounded batch.
// A full batch schedules another pass shortly, so a backlog cannot persist just
// because a single minute pass reached its batch limit.
var cleanupQueries = []string{
	`DELETE FROM public.authorization_codes WHERE credential_digest IN (SELECT credential_digest FROM public.authorization_codes WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 250)`,
	`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT credential_digest FROM public.relay_access_tokens WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 250)`,
	`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT t.credential_digest FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' ORDER BY g.terminated_at LIMIT 250)`,
	`DELETE FROM public.refresh_generations WHERE credential_digest IN (SELECT f.credential_digest FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' OR (g.terminated_at IS NULL AND (g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour')) LIMIT 250)`,
	`DELETE FROM public.login_grants WHERE id IN (SELECT g.id FROM public.login_grants g WHERE (g.terminated_at<clock_timestamp()-interval '1 hour' OR g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour') AND NOT EXISTS(SELECT 1 FROM public.refresh_generations f WHERE f.grant_id=g.id) AND NOT EXISTS(SELECT 1 FROM public.relay_access_tokens t WHERE t.grant_id=g.id) LIMIT 250)`,
	`DELETE FROM public.authorization_transactions WHERE id IN (SELECT id FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 250)`,
	`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT 250)`,
	`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY absolute_expires_at LIMIT 250)`,
	`DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT 250)`,
}

func (s *Server) cleanupPass(ctx context.Context) (bool, error) {
	more := false
	for _, statement := range cleanupQueries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		work, cancel := context.WithTimeout(ctx, 2*time.Second)
		tag, err := s.Pool.Exec(work, statement)
		cancel()
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == cleanupBatchSize {
			more = true
		}
	}
	return more, nil
}

// Maintain removes expired authentication state in bounded, cancellable passes.
// Every read enforces expiry even if physical cleanup is delayed by an outage.
func (s *Server) Maintain(ctx context.Context) {
	if s.deletionJournal != nil {
		go s.MaintainDeletions(ctx)
	}
	for {
		if ctx.Err() != nil {
			return
		}
		pass, cancel := context.WithTimeout(ctx, 30*time.Second)
		more, err := s.cleanupPass(pass)
		cancel()
		if err == nil {
			s.cleanupCompleted.Add(1)
		} else if ctx.Err() == nil {
			s.cleanupFailed.Add(1)
		}
		if ctx.Err() != nil {
			return
		}
		delay := time.Minute
		if err != nil {
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
