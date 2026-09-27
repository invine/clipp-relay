package auth

import (
	"context"
	"time"
)

// Maintain removes expired authentication state in bounded batches. Expiry is
// enforced by every read, independently of this physical cleanup.
func (s *Server) Maintain(ctx context.Context) {
	run := func() {
		for _, statement := range []string{
			`DELETE FROM public.authorization_transactions WHERE id IN (SELECT id FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT 500)`,
			`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' OR absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT 500)`,
			`DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT 500)`,
		} {
			work, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = s.Pool.Exec(work, statement)
			cancel()
		}
	}
	run()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
