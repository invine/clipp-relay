package auth

import (
	"context"
	"fmt"
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
	err := s.db.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-LEAST(
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
	if err := s.db.QueryRow(ctx, `SELECT episodes FROM public.cleanup_breach_record WHERE singleton=true`).Scan(&out.BreachEpisodes); err != nil {
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
	_, err = s.db.Exec(ctx, `UPDATE public.cleanup_breach_record SET
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
	fair bool
}

// fairHiddenChild uses a keyset cursor over at most 16 hidden accounts and an
// indexed, per-account LATERAL lookup. One large account gets the full batch;
// several accounts share it. The last candidate ID becomes the next cursor.
type fairChildSpec struct {
	table, key, columns, source, accountRef, eligible string
}

func fairHiddenChild(spec fairChildSpec) cleanupQuery {
	const statement = `WITH after_cursor AS MATERIALIZED (
 SELECT a.id,0 AS phase FROM public.accounts a WHERE %[5]s AND a.id>$2::uuid
 AND EXISTS(SELECT 1 FROM %[4]s WHERE %[6]s=a.id %[7]s) ORDER BY a.id LIMIT 16
), before_cursor AS MATERIALIZED (
 SELECT a.id,1 AS phase FROM public.accounts a WHERE %[5]s AND a.id<=$2::uuid
 AND EXISTS(SELECT 1 FROM %[4]s WHERE %[6]s=a.id %[7]s) ORDER BY a.id LIMIT 16
), candidates AS MATERIALIZED (
 SELECT id,phase FROM after_cursor UNION ALL SELECT id,phase FROM before_cursor
 ORDER BY phase,id LIMIT 16
), budget AS MATERIALIZED (
 SELECT GREATEST(1,($1::bigint+GREATEST(count(*),1)-1)/GREATEST(count(*),1)) AS take FROM candidates
), victims AS MATERIALIZED (
 SELECT child.* FROM candidates e CROSS JOIN budget b
 JOIN LATERAL (SELECT %[3]s FROM %[4]s WHERE %[6]s=e.id %[7]s LIMIT b.take) child ON true
 LIMIT $1
), removed AS (
 DELETE FROM %[1]s WHERE %[2]s IN (SELECT * FROM victims) RETURNING 1
)
SELECT (SELECT count(*) FROM removed),(SELECT count(*) FROM candidates),
 (SELECT take FROM budget),
 COALESCE((SELECT id::text FROM candidates ORDER BY phase DESC,id DESC LIMIT 1),'')`
	return cleanupQuery{sql: fmt.Sprintf(statement, spec.table, spec.key, spec.columns, spec.source, hiddenAccount, spec.accountRef, spec.eligible), full: cleanupBatchSize, fair: true}
}

// Each statement is one transaction and changes at most 250 rows. A full
// batch schedules another pass shortly instead of waiting for the next minute.
// The cursor rotates the first query so a slow class cannot always consume the
// pass deadline before later retention classes are attempted.
var cleanupQueries = []cleanupQuery{
	{`DELETE FROM public.retained_quota_usage WHERE (pepper_version,identity_digest,week_start) IN (SELECT pepper_version,identity_digest,week_start FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date ORDER BY week_start LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.weekly_quota_usage WHERE (account_id,week_start) IN (SELECT account_id,week_start FROM public.weekly_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-84 ORDER BY week_start,account_id LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.authorization_codes WHERE credential_digest IN (SELECT credential_digest FROM public.authorization_codes WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT credential_digest FROM public.relay_access_tokens WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.relay_access_tokens WHERE credential_digest IN (SELECT t.credential_digest FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' ORDER BY g.terminated_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.refresh_generations WHERE credential_digest IN (SELECT f.credential_digest FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' OR (g.terminated_at IS NULL AND (g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour')) LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.login_grants WHERE id IN (SELECT g.id FROM public.login_grants g WHERE (g.terminated_at<clock_timestamp()-interval '1 hour' OR g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour') AND NOT EXISTS(SELECT 1 FROM public.refresh_generations f WHERE f.grant_id=g.id) AND NOT EXISTS(SELECT 1 FROM public.relay_access_tokens t WHERE t.grant_id=g.id) LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.authorization_transactions WHERE id IN (SELECT id FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY absolute_expires_at LIMIT $1)`, cleanupBatchSize, false},
	fairHiddenChild(fairChildSpec{table: "public.authorization_codes", key: "credential_digest", columns: "c.credential_digest", source: "public.authorization_codes c", accountRef: "c.account_id"}),
	fairHiddenChild(fairChildSpec{table: "public.relay_access_tokens", key: "credential_digest", columns: "c.credential_digest", source: "public.relay_access_tokens c JOIN public.login_grants g ON g.id=c.grant_id", accountRef: "g.account_id"}),
	fairHiddenChild(fairChildSpec{table: "public.refresh_generations", key: "credential_digest", columns: "c.credential_digest", source: "public.refresh_generations c JOIN public.login_grants g ON g.id=c.grant_id", accountRef: "g.account_id"}),
	fairHiddenChild(fairChildSpec{table: "public.login_grants", key: "id", columns: "c.id", source: "public.login_grants c", accountRef: "c.account_id", eligible: "AND NOT EXISTS(SELECT 1 FROM public.refresh_generations f WHERE f.grant_id=c.id) AND NOT EXISTS(SELECT 1 FROM public.relay_access_tokens t WHERE t.grant_id=c.id)"}),
	fairHiddenChild(fairChildSpec{table: "public.authorization_transactions", key: "id", columns: "c.id", source: "public.authorization_transactions c", accountRef: "c.account_id"}),
	fairHiddenChild(fairChildSpec{table: "public.portal_sessions", key: "credential_digest", columns: "c.credential_digest", source: "public.portal_sessions c", accountRef: "c.account_id"}),
	fairHiddenChild(fairChildSpec{table: "public.weekly_quota_usage", key: "(account_id,week_start)", columns: "c.account_id,c.week_start", source: "public.weekly_quota_usage c", accountRef: "c.account_id"}),
	{`DELETE FROM public.deletion_operations d WHERE d.id IN (SELECT d2.id FROM public.deletion_operations d2 JOIN public.accounts a ON a.id=d2.account_id WHERE a.deleted_at<clock_timestamp()-interval '1 hour' AND d2.completed_at IS NOT NULL ORDER BY a.deleted_at,d2.id LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT $1)`, cleanupBatchSize, false},
	{`DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.deleted_at<clock_timestamp()-interval '1 hour' AND NOT EXISTS(SELECT 1 FROM public.authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.authorization_transactions x WHERE x.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_quota_usage u WHERE u.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.deletion_operations d WHERE d.account_id=a2.id) ORDER BY a2.deleted_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED)`, cleanupBatchSize, false},
	{`WITH removed AS (DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.status='Pending' AND a2.deletion_started_at IS NULL AND a2.last_portal_login_at<clock_timestamp()-interval '90 days' AND NOT EXISTS(SELECT 1 FROM public.authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.authorization_transactions x WHERE x.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_quota_usage u WHERE u.account_id=a2.id) ORDER BY a2.last_portal_login_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED) RETURNING a.id) INSERT INTO public.audit_events(id,occurred_at,event,account_id) SELECT gen_random_uuid(),clock_timestamp(),'pending_expired',id FROM removed`, cleanupBatchSize / 2, false},
}

func (s *Server) cleanupPass(ctx context.Context) (bool, error) {
	more := false
	var firstError error
	process := func(index int, query cleanupQuery, deadline time.Duration) {
		work, cancel := context.WithTimeout(ctx, deadline)
		if query.fair {
			cursor := s.cleanupAccountCursors[index]
			if cursor == "" {
				cursor = "00000000-0000-0000-0000-000000000000"
			}
			var removed, candidates, take int64
			var next string
			err := s.db.QueryRow(work, query.sql, query.full, cursor).Scan(&removed, &candidates, &take, &next)
			cancel()
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				return
			}
			s.cleanupAccountCursors[index] = next
			if removed == query.full || candidates == 16 || (removed > 0 && removed >= take) {
				more = true
			}
			return
		}
		tag, err := s.db.Exec(work, query.sql, query.full)
		cancel()
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			return
		}
		if tag.RowsAffected() == query.full {
			more = true
		}
	}
	// The last two statements remove parents. Rotate only the independent and
	// child statements, then always attempt parent deletion after them. A child
	// failure cannot starve another class or consume the entire 30-second pass.
	children := len(cleanupQueries) - 2
	start := int(s.cleanupCursor.Add(1)-1) % children
	for offset := 0; offset < children; offset++ {
		if err := ctx.Err(); err != nil {
			return more, err
		}
		index := (start + offset) % children
		process(index, cleanupQueries[index], 1250*time.Millisecond)
	}
	for index, query := range cleanupQueries[children:] {
		if err := ctx.Err(); err != nil {
			return more, err
		}
		process(children+index, query, 2*time.Second)
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
