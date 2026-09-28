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
	BreachEpisodes            uint64
}

func (s *Server) SampleCleanupSignals(ctx context.Context) (CleanupSignals, error) {
	var seconds float64
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-LEAST(
		COALESCE((SELECT min(LEAST(COALESCE(consumed_at,expires_at),expires_at)) FROM public.authorization_codes WHERE LEAST(COALESCE(consumed_at,expires_at),expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(t.expires_at,COALESCE(g.terminated_at,t.expires_at))) FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id WHERE LEAST(t.expires_at,COALESCE(g.terminated_at,t.expires_at))<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(COALESCE(claimed_at,expires_at),expires_at)) FROM public.authorization_transactions WHERE LEAST(COALESCE(claimed_at,expires_at),expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(COALESCE(terminated_at,idle_expires_at),idle_expires_at,absolute_expires_at)) FROM public.login_grants WHERE LEAST(COALESCE(terminated_at,idle_expires_at),idle_expires_at,absolute_expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(COALESCE(g.terminated_at,g.idle_expires_at),g.idle_expires_at,g.absolute_expires_at)) FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE LEAST(COALESCE(g.terminated_at,g.idle_expires_at),g.idle_expires_at,g.absolute_expires_at)<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(idle_expires_at) FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()),clock_timestamp()),
	COALESCE((SELECT min(absolute_expires_at) FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(deleted_at) FROM public.accounts WHERE deleted_at IS NOT NULL),clock_timestamp()),
		COALESCE((SELECT min(last_portal_login_at+interval '90 days') FROM public.accounts WHERE status='Pending' AND deletion_started_at IS NULL AND last_portal_login_at<clock_timestamp()-interval '90 days'),clock_timestamp()),
		COALESCE((SELECT min((week_start::timestamp AT TIME ZONE 'UTC')+interval '91 days') FROM public.weekly_quota_usage WHERE week_start<(date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-84)),clock_timestamp()),
	COALESCE((SELECT min(occurred_at+interval '180 days') FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days'),clock_timestamp()),
		COALESCE((SELECT min((week_start::timestamp AT TIME ZONE 'UTC')+interval '7 days') FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date),clock_timestamp())
	)),0)`).Scan(&seconds)
	if err != nil {
		return CleanupSignals{}, err
	}
	out := CleanupSignals{OldestAge: time.Duration(seconds * float64(time.Second)), Completed: s.cleanupCompleted.Load(), Failed: s.cleanupFailed.Load()}
	out.Warning = out.OldestAge > time.Hour
	out.Critical = out.OldestAge >= 12*time.Hour
	out.Breach = out.OldestAge >= 24*time.Hour
	if err := s.Pool.QueryRow(ctx, `SELECT episodes FROM public.cleanup_breach_record WHERE singleton=true`).Scan(&out.BreachEpisodes); err != nil {
		return CleanupSignals{}, err
	}
	return out, nil
}

// The aggregate episode counter and timestamps survive process restarts. No
// account, credential, or arbitrary error text is written to this record.
func (s *Server) recordCleanupBreach(ctx context.Context) error {
	signals, err := s.SampleCleanupSignals(ctx)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE public.cleanup_breach_record SET
active=$1,
episodes=episodes+CASE WHEN $1 AND NOT active THEN 1 ELSE 0 END,
first_observed_at=CASE WHEN $1 AND NOT active THEN clock_timestamp() ELSE first_observed_at END,
last_observed_at=CASE WHEN $1 THEN clock_timestamp() ELSE last_observed_at END,
worst_age_seconds=GREATEST(worst_age_seconds,$2)
WHERE singleton=true`, signals.Breach, int64(signals.OldestAge/time.Second))
	return err
}

const cleanupBatchSize = 250

// A hidden account has already lost its identity, or its Pending login clock
// passed 90 days. Account deletion checks every child table before removing a
// parent; FK locks prevent a concurrent writer from leaving a partial purge.
const hiddenAccount = `(a.deleted_at<clock_timestamp()-interval '1 hour' OR (a.status='Pending' AND a.deletion_started_at IS NULL AND a.last_portal_login_at<clock_timestamp()-interval '90 days'))`

type cleanupQuery struct {
	sql  string
	full int64
}

// Each statement is one transaction and changes at most 250 rows. A full
// batch schedules another pass shortly instead of waiting for the next minute.
// The cursor rotates the first query so a slow class cannot always consume the
// pass deadline before later retention classes are attempted.
var cleanupQueries = []cleanupQuery{
	{`DELETE FROM public.retained_quota_usage WHERE (pepper_version,identity_digest,week_start) IN (SELECT pepper_version,identity_digest,week_start FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date ORDER BY week_start LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.weekly_quota_usage WHERE (account_id,week_start) IN (SELECT account_id,week_start FROM public.weekly_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-84 ORDER BY week_start,account_id LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.authorization_codes WHERE credential_digest IN (SELECT credential_digest FROM public.authorization_codes WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT credential_digest FROM public.relay_access_tokens WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT t.credential_digest FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' ORDER BY g.terminated_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.refresh_generations WHERE credential_digest IN (SELECT f.credential_digest FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' OR (g.terminated_at IS NULL AND (g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour')) LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.login_grants WHERE id IN (SELECT g.id FROM public.login_grants g WHERE (g.terminated_at<clock_timestamp()-interval '1 hour' OR g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour') AND NOT EXISTS(SELECT 1 FROM public.refresh_generations f WHERE f.grant_id=g.id) AND NOT EXISTS(SELECT 1 FROM public.relay_access_tokens t WHERE t.grant_id=g.id) LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.authorization_transactions WHERE id IN (SELECT id FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY absolute_expires_at LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.authorization_codes c WHERE c.credential_digest IN (SELECT c2.credential_digest FROM public.authorization_codes c2 JOIN public.accounts a ON a.id=c2.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,c2.credential_digest LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.relay_access_tokens t WHERE t.credential_digest IN (SELECT t2.credential_digest FROM public.relay_access_tokens t2 JOIN public.login_grants g ON g.id=t2.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,t2.credential_digest LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.refresh_generations f WHERE f.credential_digest IN (SELECT f2.credential_digest FROM public.refresh_generations f2 JOIN public.login_grants g ON g.id=f2.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,f2.credential_digest LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.login_grants g WHERE g.id IN (SELECT g2.id FROM public.login_grants g2 JOIN public.accounts a ON a.id=g2.account_id WHERE ` + hiddenAccount + ` AND NOT EXISTS(SELECT 1 FROM public.refresh_generations f WHERE f.grant_id=g2.id) AND NOT EXISTS(SELECT 1 FROM public.relay_access_tokens t WHERE t.grant_id=g2.id) ORDER BY a.last_portal_login_at,g2.id LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.authorization_transactions x WHERE x.id IN (SELECT x2.id FROM public.authorization_transactions x2 JOIN public.accounts a ON a.id=x2.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,x2.id LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.portal_sessions p WHERE p.credential_digest IN (SELECT p2.credential_digest FROM public.portal_sessions p2 JOIN public.accounts a ON a.id=p2.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,p2.credential_digest LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.weekly_quota_usage u WHERE (u.account_id,u.week_start) IN (SELECT u2.account_id,u2.week_start FROM public.weekly_quota_usage u2 JOIN public.accounts a ON a.id=u2.account_id WHERE ` + hiddenAccount + ` ORDER BY a.last_portal_login_at,u2.account_id,u2.week_start LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.deletion_operations d WHERE d.id IN (SELECT d2.id FROM public.deletion_operations d2 JOIN public.accounts a ON a.id=d2.account_id WHERE a.deleted_at<clock_timestamp()-interval '1 hour' AND d2.completed_at IS NOT NULL ORDER BY a.deleted_at,d2.id LIMIT $1)`, cleanupBatchSize},
	{`DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.deleted_at<clock_timestamp()-interval '1 hour' AND NOT EXISTS(SELECT 1 FROM public.authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.authorization_transactions x WHERE x.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_quota_usage u WHERE u.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.deletion_operations d WHERE d.account_id=a2.id) ORDER BY a2.deleted_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED)`, cleanupBatchSize},
	{`WITH removed AS (DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.status='Pending' AND a2.deletion_started_at IS NULL AND a2.last_portal_login_at<clock_timestamp()-interval '90 days' AND NOT EXISTS(SELECT 1 FROM public.authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.authorization_transactions x WHERE x.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_quota_usage u WHERE u.account_id=a2.id) ORDER BY a2.last_portal_login_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED) RETURNING a.id) INSERT INTO public.audit_events(id,occurred_at,event,account_id) SELECT gen_random_uuid(),clock_timestamp(),'pending_expired',id FROM removed`, cleanupBatchSize / 2},
	{`DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT $1)`, cleanupBatchSize},
}

func (s *Server) cleanupPass(ctx context.Context) (bool, error) {
	more := false
	var firstError error
	start := int(s.cleanupCursor.Add(1)-1) % len(cleanupQueries)
	for offset := range cleanupQueries {
		if err := ctx.Err(); err != nil {
			return more, err
		}
		query := cleanupQueries[(start+offset)%len(cleanupQueries)]
		work, cancel := context.WithTimeout(ctx, 2*time.Second)
		tag, err := s.Pool.Exec(work, query.sql, query.full)
		cancel()
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if tag.RowsAffected() == query.full {
			more = true
		}
	}
	return more, firstError
}

// Maintain removes expired authentication state in bounded, cancellable passes.
// Every read enforces expiry even if physical cleanup is delayed by an outage.
func (s *Server) Maintain(ctx context.Context) {
	if !s.cleanupWorker.CompareAndSwap(false, true) {
		return
	}
	defer s.cleanupWorker.Store(false)
	if s.deletionJournal != nil {
		go s.MaintainDeletions(ctx)
	}
	for {
		if ctx.Err() != nil {
			return
		}
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
		} else if ctx.Err() == nil {
			s.cleanupFailed.Add(1)
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
