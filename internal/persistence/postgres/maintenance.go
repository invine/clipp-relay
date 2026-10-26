package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
)

type Maintenance struct{ runtime *database.Runtime }

func NewMaintenance(runtime *database.Runtime) *Maintenance {
	return &Maintenance{runtime: runtime}
}

func (m *Maintenance) CleanupHealth(ctx context.Context) (persistence.CleanupHealth, error) {
	var seconds float64
	if err := m.runtime.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-LEAST(
		COALESCE((SELECT min(LEAST(COALESCE(consumed_at,expires_at),expires_at)) FROM public.native_authorization_codes WHERE LEAST(COALESCE(consumed_at,expires_at),expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(t.expires_at,COALESCE(g.terminated_at,t.expires_at))) FROM public.native_access_tokens t JOIN public.native_login_grants g ON g.id=t.grant_id WHERE LEAST(t.expires_at,COALESCE(g.terminated_at,t.expires_at))<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(COALESCE(terminated_at,idle_expires_at),idle_expires_at,absolute_expires_at)) FROM public.native_login_grants WHERE LEAST(COALESCE(terminated_at,idle_expires_at),idle_expires_at,absolute_expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(LEAST(COALESCE(g.terminated_at,g.idle_expires_at),g.idle_expires_at,g.absolute_expires_at)) FROM public.native_refresh_generations f JOIN public.native_login_grants g ON g.id=f.grant_id WHERE LEAST(COALESCE(g.terminated_at,g.idle_expires_at),g.idle_expires_at,g.absolute_expires_at)<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(idle_expires_at) FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(absolute_expires_at) FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()),clock_timestamp()),
		COALESCE((SELECT min(deleted_at) FROM public.accounts WHERE deleted_at IS NOT NULL),clock_timestamp()),
		COALESCE((SELECT min(last_portal_login_at+interval '90 days') FROM public.accounts WHERE status='Pending' AND deletion_started_at IS NULL AND last_portal_login_at<clock_timestamp()-interval '90 days'),clock_timestamp()),
		COALESCE((SELECT min((week_start::timestamp AT TIME ZONE 'UTC')+interval '91 days') FROM public.weekly_relay_usage WHERE week_start<(date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-84)),clock_timestamp()),
		COALESCE((SELECT min(occurred_at+interval '180 days') FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days'),clock_timestamp()),
		COALESCE((SELECT min((week_start::timestamp AT TIME ZONE 'UTC')+interval '7 days') FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date),clock_timestamp())
	)),0)`).Scan(&seconds); err != nil {
		return persistence.CleanupHealth{}, persistenceUnavailable(err)
	}
	var episodes uint64
	if err := m.runtime.QueryRow(ctx, `SELECT episodes FROM public.cleanup_breach_record WHERE singleton=true`).Scan(&episodes); err != nil {
		return persistence.CleanupHealth{}, persistenceUnavailable(err)
	}
	return persistence.CleanupHealth{OldestAge: time.Duration(seconds * float64(time.Second)), BreachEpisodes: episodes}, nil
}

func (m *Maintenance) RecordCleanupBreach(ctx context.Context, active bool, ageSeconds int64) error {
	_, err := m.runtime.Exec(ctx, `UPDATE public.cleanup_breach_record SET
active=$1,
episodes=episodes+CASE WHEN $1 AND NOT active THEN 1 ELSE 0 END,
first_observed_at=CASE WHEN $1 AND NOT active THEN clock_timestamp() ELSE first_observed_at END,
last_observed_at=CASE WHEN $1 THEN clock_timestamp() ELSE last_observed_at END,
worst_age_seconds=GREATEST(worst_age_seconds,$2)
WHERE singleton=true`, active, ageSeconds)
	if err != nil {
		return persistenceUnavailable(err)
	}
	return nil
}

type cleanupQuery struct {
	sql  string
	fair bool
}

const hiddenAccount = `(a.deleted_at<clock_timestamp()-interval '1 hour' OR (a.status='Pending' AND a.deletion_started_at IS NULL AND a.last_portal_login_at<clock_timestamp()-interval '90 days'))`

type fairChildSpec struct{ table, key, columns, source, accountRef, eligible string }

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
	return cleanupQuery{sql: fmt.Sprintf(statement, spec.table, spec.key, spec.columns, spec.source, hiddenAccount, spec.accountRef, spec.eligible), fair: true}
}

var cleanupQueries = map[persistence.CleanupOperation]cleanupQuery{
	persistence.CleanupRetainedUsage:            {sql: `DELETE FROM public.retained_quota_usage WHERE (pepper_version,identity_digest,week_start) IN (SELECT pepper_version,identity_digest,week_start FROM public.retained_quota_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date ORDER BY week_start LIMIT $1)`},
	persistence.CleanupWeeklyUsage:              {sql: `DELETE FROM public.weekly_relay_usage WHERE (account_id,week_start) IN (SELECT account_id,week_start FROM public.weekly_relay_usage WHERE week_start<date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-84 ORDER BY week_start,account_id LIMIT $1)`},
	persistence.CleanupAuthorizationCodes:       {sql: `DELETE FROM public.native_authorization_codes WHERE credential_digest IN (SELECT credential_digest FROM public.native_authorization_codes WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`},
	persistence.CleanupAccessTokens:             {sql: `DELETE FROM public.native_access_tokens WHERE credential_digest IN (SELECT credential_digest FROM public.native_access_tokens WHERE expires_at<clock_timestamp()-interval '1 hour' ORDER BY expires_at LIMIT $1)`},
	persistence.CleanupTerminatedAccessTokens:   {sql: `DELETE FROM public.native_access_tokens WHERE credential_digest IN (SELECT t.credential_digest FROM public.native_access_tokens t JOIN public.native_login_grants g ON g.id=t.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' ORDER BY g.terminated_at LIMIT $1)`},
	persistence.CleanupRefreshGenerations:       {sql: `DELETE FROM public.native_refresh_generations WHERE credential_digest IN (SELECT f.credential_digest FROM public.native_refresh_generations f JOIN public.native_login_grants g ON g.id=f.grant_id WHERE g.terminated_at<clock_timestamp()-interval '1 hour' OR (g.terminated_at IS NULL AND (g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour')) LIMIT $1)`},
	persistence.CleanupLoginGrants:              {sql: `DELETE FROM public.native_login_grants WHERE id IN (SELECT g.id FROM public.native_login_grants g WHERE (g.terminated_at<clock_timestamp()-interval '1 hour' OR g.idle_expires_at<clock_timestamp()-interval '1 hour' OR g.absolute_expires_at<clock_timestamp()-interval '1 hour') AND NOT EXISTS(SELECT 1 FROM public.native_refresh_generations f WHERE f.grant_id=g.id) AND NOT EXISTS(SELECT 1 FROM public.native_access_tokens t WHERE t.grant_id=g.id) LIMIT $1)`},
	persistence.CleanupIdlePortalSessions:       {sql: `DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour' ORDER BY idle_expires_at LIMIT $1)`},
	persistence.CleanupAbsolutePortalSessions:   {sql: `DELETE FROM public.portal_sessions WHERE credential_digest IN (SELECT credential_digest FROM public.portal_sessions WHERE absolute_expires_at<clock_timestamp()-interval '1 hour' ORDER BY absolute_expires_at LIMIT $1)`},
	persistence.CleanupHiddenAuthorizationCodes: fairHiddenChild(fairChildSpec{table: "public.native_authorization_codes", key: "credential_digest", columns: "c.credential_digest", source: "public.native_authorization_codes c", accountRef: "c.account_id"}),
	persistence.CleanupHiddenAccessTokens:       fairHiddenChild(fairChildSpec{table: "public.native_access_tokens", key: "credential_digest", columns: "c.credential_digest", source: "public.native_access_tokens c JOIN public.native_login_grants g ON g.id=c.grant_id", accountRef: "g.account_id"}),
	persistence.CleanupHiddenRefreshGenerations: fairHiddenChild(fairChildSpec{table: "public.native_refresh_generations", key: "credential_digest", columns: "c.credential_digest", source: "public.native_refresh_generations c JOIN public.native_login_grants g ON g.id=c.grant_id", accountRef: "g.account_id"}),
	persistence.CleanupHiddenLoginGrants:        fairHiddenChild(fairChildSpec{table: "public.native_login_grants", key: "id", columns: "c.id", source: "public.native_login_grants c", accountRef: "c.account_id", eligible: "AND NOT EXISTS(SELECT 1 FROM public.native_refresh_generations f WHERE f.grant_id=c.id) AND NOT EXISTS(SELECT 1 FROM public.native_access_tokens t WHERE t.grant_id=c.id)"}),
	persistence.CleanupHiddenPortalSessions:     fairHiddenChild(fairChildSpec{table: "public.portal_sessions", key: "credential_digest", columns: "c.credential_digest", source: "public.portal_sessions c", accountRef: "c.account_id"}),
	persistence.CleanupHiddenWeeklyUsage:        fairHiddenChild(fairChildSpec{table: "public.weekly_relay_usage", key: "(account_id,week_start)", columns: "c.account_id,c.week_start", source: "public.weekly_relay_usage c", accountRef: "c.account_id"}),
	persistence.CleanupDeletionOperations:       {sql: `DELETE FROM public.deletion_operations d WHERE d.id IN (SELECT d2.id FROM public.deletion_operations d2 JOIN public.accounts a ON a.id=d2.account_id WHERE a.deleted_at<clock_timestamp()-interval '1 hour' AND d2.completed_at IS NOT NULL ORDER BY a.deleted_at,d2.id LIMIT $1)`},
	persistence.CleanupAuditEvents:              {sql: `DELETE FROM public.audit_events WHERE id IN (SELECT id FROM public.audit_events WHERE occurred_at<clock_timestamp()-interval '180 days' ORDER BY occurred_at LIMIT $1)`},
	persistence.CleanupDeletedAccounts:          {sql: `DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.deleted_at<clock_timestamp()-interval '1 hour' AND NOT EXISTS(SELECT 1 FROM public.native_authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.native_login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_relay_usage u WHERE u.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.deletion_operations d WHERE d.account_id=a2.id) ORDER BY a2.deleted_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED)`},
	persistence.CleanupExpiredPendingAccounts:   {sql: `WITH removed AS (DELETE FROM public.accounts a WHERE a.id IN (SELECT a2.id FROM public.accounts a2 WHERE a2.status='Pending' AND a2.deletion_started_at IS NULL AND a2.last_portal_login_at<clock_timestamp()-interval '90 days' AND NOT EXISTS(SELECT 1 FROM public.native_authorization_codes c WHERE c.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.portal_sessions p WHERE p.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.native_login_grants g WHERE g.account_id=a2.id) AND NOT EXISTS(SELECT 1 FROM public.weekly_relay_usage u WHERE u.account_id=a2.id) ORDER BY a2.last_portal_login_at,a2.id LIMIT $1 FOR UPDATE OF a2 SKIP LOCKED) RETURNING a.id) INSERT INTO public.audit_events(id,occurred_at,event,account_id) SELECT gen_random_uuid(),clock_timestamp(),'pending_expired',id FROM removed)`},
}

func (m *Maintenance) CleanupBatch(ctx context.Context, batch persistence.CleanupBatch) (persistence.CleanupBatchResult, error) {
	query, ok := cleanupQueries[batch.Operation]
	if !ok || batch.Limit < 1 || query.fair && batch.Cursor == "" {
		return persistence.CleanupBatchResult{}, persistence.ErrInvalidCleanupOperation
	}
	if query.fair {
		var result persistence.CleanupBatchResult
		err := m.runtime.QueryRow(ctx, query.sql, batch.Limit, batch.Cursor).Scan(&result.Removed, &result.Candidates, &result.Take, &result.NextCursor)
		if err != nil {
			return persistence.CleanupBatchResult{}, persistenceUnavailable(err)
		}
		return result, nil
	}
	tag, err := m.runtime.Exec(ctx, query.sql, batch.Limit)
	if err != nil {
		return persistence.CleanupBatchResult{}, persistenceUnavailable(err)
	}
	return persistence.CleanupBatchResult{Removed: tag.RowsAffected()}, nil
}

func (m *Maintenance) PepperCoverage(ctx context.Context) (persistence.PepperCoverage, error) {
	var coverage persistence.PepperCoverage
	rows, err := m.runtime.Query(ctx, `SELECT DISTINCT pepper_version FROM public.portal_sessions WHERE LEAST(idle_expires_at,absolute_expires_at)>=clock_timestamp()-interval '24 hours'`)
	if err != nil {
		return persistence.PepperCoverage{}, persistenceUnavailable(err)
	}
	for rows.Next() {
		var version uint64
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return persistence.PepperCoverage{}, persistenceUnavailable(err)
		}
		coverage.PortalSessions = append(coverage.PortalSessions, version)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return persistence.PepperCoverage{}, persistenceUnavailable(err)
	}
	retained, err := m.runtime.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage WHERE week_start>=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		return persistence.PepperCoverage{}, persistenceUnavailable(err)
	}
	for retained.Next() {
		var version uint64
		if err := retained.Scan(&version); err != nil {
			retained.Close()
			return persistence.PepperCoverage{}, persistenceUnavailable(err)
		}
		coverage.RetainedUsage = append(coverage.RetainedUsage, version)
	}
	err = retained.Err()
	retained.Close()
	if err != nil {
		return persistence.PepperCoverage{}, persistenceUnavailable(err)
	}
	return coverage, nil
}

func persistenceUnavailable(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(persistence.ErrUnavailable, err)
	}
	return persistence.ErrUnavailable
}

var _ persistence.Maintenance = (*Maintenance)(nil)
