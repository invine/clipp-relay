package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

// ReadViews is the PostgreSQL implementation of the consumer-facing read contract.
type ReadViews struct{ runtime *database.Runtime }

func NewReadViews(runtime *database.Runtime) *ReadViews { return &ReadViews{runtime: runtime} }

func (r *ReadViews) OwnerProfile(ctx context.Context, id string, digest []byte, version uint64) (persistence.OwnerProfile, error) {
	var p persistence.OwnerProfile
	err := r.runtime.QueryRow(ctx, `WITH current_week AS MATERIALIZED (SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date AS monday)
SELECT a.status,a.email,a.email_verified,a.hosted_domain,a.recovery_review_hold,(SELECT monday::timestamp AT TIME ZONE 'UTC' FROM current_week),COALESCE(a.weekly_bytes_override,q.weekly_bytes),COALESCE(a.sessions_override,q.sessions),
COALESCE((SELECT u.recorded_bytes FROM public.weekly_relay_usage u WHERE u.account_id=a.id AND u.week_start=(SELECT monday FROM current_week)),0),
(SELECT jsonb_agg(jsonb_build_object('week',w.week_start,'recorded',COALESCE(u.recorded_bytes,0),'observed',u.account_id IS NOT NULL) ORDER BY w.week_start DESC)
 FROM (SELECT ((SELECT monday FROM current_week)-7*n)::date AS week_start FROM generate_series(0,12) n) w
 LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id AND u.week_start=w.week_start)
FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id LEFT JOIN public.quota_plans q ON q.id=a.plan_id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()
AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`, id, digest[:], version).
		Scan(&p.Status, &p.Email, &p.Verified, &p.HostedDomain, &p.RecoveryHeld, &p.WeekStart, &p.WeeklyBytes, &p.Sessions, &p.Usage, &p.HistoryJSON)
	return p, readError(err)
}

func (r *ReadViews) AdminAccount(ctx context.Context, id string) (persistence.AdminAccount, error) {
	var a persistence.AdminAccount
	err := r.runtime.QueryRow(ctx, `WITH cw AS MATERIALIZED (SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date AS monday)
SELECT a.id::text,a.email,a.status,COALESCE(a.plan_id::text,''),COALESCE(q.name,''),COALESCE(a.weekly_bytes_override::text,''),COALESCE(a.sessions_override::text,''),a.revision,a.recovery_review_hold,q.archived_at IS NOT NULL,
COALESCE(a.weekly_bytes_override,q.weekly_bytes,0),COALESCE(a.sessions_override,q.sessions,0),
COALESCE(u.recorded_bytes,0),GREATEST(0,COALESCE(a.weekly_bytes_override,q.weekly_bytes,0)-COALESCE(u.recorded_bytes,0)),
(SELECT jsonb_agg(jsonb_build_object('week',w.week_start,'recorded',COALESCE(h.recorded_bytes,0)) ORDER BY w.week_start DESC) FROM (SELECT ((SELECT monday FROM cw)-7*n)::date AS week_start FROM generate_series(0,12) n) w LEFT JOIN public.weekly_relay_usage h ON h.account_id=a.id AND h.week_start=w.week_start)
FROM public.accounts a LEFT JOIN public.quota_plans q ON q.id=a.plan_id LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id AND u.week_start=(SELECT monday FROM cw)
WHERE a.id=$1 AND a.deletion_started_at IS NULL`, id).Scan(&a.ID, &a.Email, &a.Status, &a.PlanID, &a.PlanName, &a.WeeklyOverride, &a.SessionOverride, &a.Revision, &a.RecoveryHeld, &a.PlanArchived, &a.EffectiveBytes, &a.EffectiveSessions, &a.Recorded, &a.Remaining, &a.HistoryJSON)
	return a, readError(err)
}

func (r *ReadViews) AdminHome(ctx context.Context, accountAfter, planAfter, status, email string, limit int) (persistence.AdminHome, error) {
	var out persistence.AdminHome
	tx, err := r.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, readError(err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT status,count(*) FROM public.accounts WHERE deletion_started_at IS NULL GROUP BY status`)
	if err != nil {
		return out, readError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if err = rows.Scan(&state, &count); err != nil {
			return out, readError(err)
		}
		switch state {
		case "Pending":
			out.Counts.Pending = count
		case "Active":
			out.Counts.Active = count
		case "Suspended":
			out.Counts.Suspended = count
		case "Denied":
			out.Counts.Denied = count
		}
	}
	if err = rows.Err(); err != nil {
		return out, readError(err)
	}
	rows.Close()
	var errCount error
	errCount = tx.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE deletion_started_at IS NULL AND ($1='' OR status=$1) AND ($2='' OR lower(email) LIKE '%'||lower($2)||'%' ESCAPE E'\\')`, status, escapeLike(email)).Scan(&out.FilteredCount)
	if errCount != nil {
		return out, readError(errCount)
	}
	rows, err = tx.Query(ctx, `SELECT a.id::text,a.email,a.status,COALESCE(a.plan_id::text,''),COALESCE(q.name,''),COALESCE(a.weekly_bytes_override::text,''),COALESCE(a.sessions_override::text,''),a.revision,a.recovery_review_hold,q.archived_at IS NOT NULL,COALESCE(a.weekly_bytes_override,q.weekly_bytes,0),COALESCE(a.sessions_override,q.sessions,0) FROM public.accounts a LEFT JOIN public.quota_plans q ON q.id=a.plan_id WHERE a.deletion_started_at IS NULL AND ($1='' OR a.status=$1) AND ($2='' OR lower(a.email) LIKE '%'||lower($2)||'%' ESCAPE E'\\') AND a.id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY a.id LIMIT $4`, status, escapeLike(email), accountAfter, limit+1)
	if err != nil {
		return out, readError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a persistence.AdminAccount
		if err = rows.Scan(&a.ID, &a.Email, &a.Status, &a.PlanID, &a.PlanName, &a.WeeklyOverride, &a.SessionOverride, &a.Revision, &a.RecoveryHeld, &a.PlanArchived, &a.EffectiveBytes, &a.EffectiveSessions); err != nil {
			return out, readError(err)
		}
		out.Accounts = append(out.Accounts, a)
	}
	if err = rows.Err(); err != nil {
		return out, readError(err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,name,weekly_bytes,sessions,revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY id LIMIT $2`, planAfter, limit+1)
	if err != nil {
		return out, readError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p persistence.AdminPlan
		if err = rows.Scan(&p.ID, &p.Name, &p.Bytes, &p.Sessions, &p.Revision, &p.Archived); err != nil {
			return out, readError(err)
		}
		out.Plans = append(out.Plans, p)
	}
	if err = rows.Err(); err != nil {
		return out, readError(err)
	}
	return out, readError(tx.Commit(ctx))
}

func (r *ReadViews) AdminPlans(ctx context.Context, after string, limit int) ([]persistence.AdminPlan, error) {
	rows, err := r.runtime.Query(ctx, `SELECT id::text,name,weekly_bytes,sessions,revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, readError(err)
	}
	defer rows.Close()
	plans := make([]persistence.AdminPlan, 0, limit)
	for rows.Next() {
		var plan persistence.AdminPlan
		if err := rows.Scan(&plan.ID, &plan.Name, &plan.Bytes, &plan.Sessions, &plan.Revision, &plan.Archived); err != nil {
			return nil, readError(err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, readError(err)
	}
	return plans, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (r *ReadViews) AdminPlan(ctx context.Context, id string) (persistence.AdminPlan, error) {
	var p persistence.AdminPlan
	err := r.runtime.QueryRow(ctx, `SELECT id::text,name,weekly_bytes,sessions,revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id=$1`, id).Scan(&p.ID, &p.Name, &p.Bytes, &p.Sessions, &p.Revision, &p.Archived)
	return p, readError(err)
}

func (r *ReadViews) ActiveLoginGrantCount(ctx context.Context, accountID string) (int, error) {
	var count int
	err := r.runtime.QueryRow(ctx, `SELECT count(*) FROM public.native_login_grants g JOIN public.accounts a ON a.id=g.account_id
		WHERE g.account_id=$1 AND g.credential_generation=a.credential_generation AND g.terminated_at IS NULL
		AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, accountID).Scan(&count)
	return count, readError(err)
}

func readError(err error) error {
	if err == nil {
		return nil
	}
	if err == pgx.ErrNoRows {
		return persistence.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(persistence.ErrUnavailable, err)
	}
	return persistence.ErrUnavailable
}
func (r *ReadViews) DeletionSignals(ctx context.Context) (persistence.DeletionReadSignals, error) {
	var v persistence.DeletionReadSignals
	var seconds float64
	err := r.runtime.QueryRow(ctx, `SELECT count(*),COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(admitted_at)),0) FROM public.deletion_operations WHERE completed_at IS NULL`).Scan(&v.Pending, &seconds)
	v.OldestAge = time.Duration(seconds * float64(time.Second))
	return v, readError(err)
}
