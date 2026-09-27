package auth

import (
	"context"
	"time"
)

const cleanupBatchSize = 500

// Each query uses a matching expiry index and removes at most one bounded batch.
// A full batch schedules another pass shortly, so a backlog cannot persist just
// because a single hourly pass reached its batch limit.
var cleanupQueries = []string{
	`DELETE FROM public.authorization_transactions WHERE id IN (SELECT id FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 500)`,
	`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT 500)`,
	`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY absolute_expires_at LIMIT 500)`,
	`DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT 500)`,
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
	for {
		if ctx.Err() != nil {
			return
		}
		more, err := s.cleanupPass(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := time.Hour
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
