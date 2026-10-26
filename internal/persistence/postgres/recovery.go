package postgres

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

type Recovery struct{ runtime *database.Runtime }

func NewRecovery(runtime *database.Runtime) *Recovery { return &Recovery{runtime: runtime} }

func (r *Recovery) SchemaRevision() int { return database.ExpectedRevision }

func (r *Recovery) CurrentDatabase(ctx context.Context) (string, error) {
	var name string
	err := r.runtime.QueryRow(ctx, `SELECT current_database()`).Scan(&name)
	if err != nil {
		return "", persistence.ErrUnavailable
	}
	return name, nil
}
func (r *Recovery) FindRecoveryRun(ctx context.Context, id string) (persistence.RecoveryRun, bool, error) {
	var run persistence.RecoveryRun
	err := r.runtime.QueryRow(ctx, `SELECT run_id,source,target,schema_revision,journal_head FROM public.recovery_runs WHERE run_id=$1`, id).Scan(&run.RunID, &run.Source, &run.Target, &run.SchemaRevision, &run.JournalHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, false, nil
	}
	if err != nil {
		return run, false, persistence.ErrUnavailable
	}
	return run, true, nil
}
func (r *Recovery) BeginRecovery(ctx context.Context) (persistence.RecoveryTransaction, error) {
	op, err := New(r.runtime).Start(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := op.Begin(op.Context())
	if err != nil {
		op.Close()
		return nil, err
	}
	recovery, ok := tx.(persistence.RecoveryTransaction)
	if !ok {
		_ = tx.Rollback(op.Context())
		op.Close()
		return nil, persistence.ErrUnavailable
	}
	return &recoveryTx{RecoveryTransaction: recovery, operation: op}, nil
}

type recoveryTx struct {
	persistence.RecoveryTransaction
	operation persistence.Operation
}

func (t *recoveryTx) Commit(ctx context.Context) error {
	defer t.operation.Close()
	return t.RecoveryTransaction.Commit(ctx)
}
func (t *recoveryTx) Rollback(ctx context.Context) error {
	defer t.operation.Close()
	return t.RecoveryTransaction.Rollback(ctx)
}

func (t *transaction) LockRecovery(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(491847602137)`); err != nil {
		return unavailable(err)
	}
	return nil
}
func (t *transaction) FindRecoveryRun(ctx context.Context, id string) (persistence.RecoveryRun, bool, error) {
	var run persistence.RecoveryRun
	err := t.tx.QueryRow(ctx, `SELECT run_id,source,target,schema_revision,journal_head FROM public.recovery_runs WHERE run_id=$1`, id).Scan(&run.RunID, &run.Source, &run.Target, &run.SchemaRevision, &run.JournalHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, false, nil
	}
	if err != nil {
		return run, false, unavailable(err)
	}
	return run, true, nil
}
func (t *transaction) LockRecoveryAccounts(ctx context.Context) error {
	rows, err := t.tx.Query(ctx, `SELECT id FROM public.accounts ORDER BY id FOR UPDATE`)
	if err != nil {
		return unavailable(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return unavailable(err)
		}
	}
	if err = rows.Err(); err != nil {
		return unavailable(err)
	}
	return nil
}
func (t *transaction) InvalidateRestoredCredentials(ctx context.Context) error {
	for _, table := range []string{"native_access_tokens", "native_refresh_generations", "native_authorization_codes", "native_login_grants", "portal_sessions"} {
		if _, err := t.tx.Exec(ctx, "DELETE FROM public."+table); err != nil {
			return unavailable(err)
		}
	}
	if _, err := t.tx.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1,recovery_review_hold=true,revision=revision+1 WHERE deleted_at IS NULL`); err != nil {
		return unavailable(err)
	}
	return nil
}
func (t *transaction) RetainedPepperVersions(ctx context.Context) ([]uint64, error) {
	rows, err := t.tx.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage WHERE week_start>=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var n uint64
		if err = rows.Scan(&n); err != nil {
			return nil, unavailable(err)
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
func (t *transaction) RecoveredAccount(ctx context.Context, id string) (persistence.RecoveredAccount, bool, error) {
	var a persistence.RecoveredAccount
	var subject *string
	var deletedAt *time.Time
	err := t.tx.QueryRow(ctx, `SELECT subject,deleted_at FROM public.accounts WHERE id=$1`, id).Scan(&subject, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, unavailable(err)
	}
	if subject != nil {
		a.Subject = *subject
	}
	a.Deleted = deletedAt != nil
	return a, true, nil
}
func (t *transaction) CurrentQuotaWeek(ctx context.Context) (time.Time, error) {
	var week time.Time
	err := t.tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week)
	if err != nil {
		return time.Time{}, unavailable(err)
	}
	return week, nil
}
func (t *transaction) RecordedUsage(ctx context.Context, id string, week time.Time) (int64, bool, error) {
	var recorded int64
	err := t.tx.QueryRow(ctx, `SELECT recorded_bytes FROM public.weekly_relay_usage WHERE account_id=$1 AND week_start=$2`, id, week).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, unavailable(err)
	}
	return recorded, true, nil
}
func (t *transaction) RecoveredRetainedUsage(ctx context.Context, version uint64, digest []byte, week time.Time) (int64, bool, error) {
	var recorded int64
	err := t.tx.QueryRow(ctx, `SELECT recorded_bytes FROM public.retained_quota_usage WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3 FOR UPDATE`, version, digest, week).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, unavailable(err)
	}
	return recorded, true, nil
}
func (t *transaction) StoreRecoveredRetainedUsage(ctx context.Context, version uint64, digest []byte, week time.Time, recorded int64) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,recorded_bytes) VALUES($1,$2,$3,$4) ON CONFLICT(pepper_version,identity_digest,week_start) DO UPDATE SET recorded_bytes=GREATEST(retained_quota_usage.recorded_bytes,EXCLUDED.recorded_bytes)`, version, digest, week, recorded)
	if err != nil {
		return unavailable(err)
	}
	return nil
}
func (t *transaction) CompleteRecoveredAccount(ctx context.Context, id, auditID string) error {
	if _, err := t.tx.Exec(ctx, `UPDATE public.accounts SET issuer=NULL,subject=NULL,email=NULL,email_verified=NULL,hosted_domain=NULL,plan_id=NULL,weekly_bytes_override=NULL,sessions_override=NULL,status='Pending',deleted_at=clock_timestamp(),deletion_started_at=COALESCE(deletion_started_at,clock_timestamp()) WHERE id=$1`, id); err != nil {
		return unavailable(err)
	}
	if _, err := t.tx.Exec(ctx, `UPDATE public.deletion_operations SET completed_at=clock_timestamp() WHERE account_id=$1 AND completed_at IS NULL`, id); err != nil {
		return unavailable(err)
	}
	if _, err := t.tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=(SELECT count(*) FROM public.deletion_operations WHERE completed_at IS NULL) WHERE singleton`); err != nil {
		return unavailable(err)
	}
	if _, err := t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'account_deleted',$2)`, auditID, id); err != nil {
		return unavailable(err)
	}
	return nil
}
func (t *transaction) RecordRecoveryRun(ctx context.Context, r persistence.RecoveryRun) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.recovery_runs(run_id,source,target,schema_revision,journal_head) VALUES($1,$2,$3,$4,$5)`, r.RunID, r.Source, r.Target, r.SchemaRevision, r.JournalHead)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

var _ persistence.RecoveryRepository = (*Recovery)(nil)
var _ persistence.RecoveryTransaction = (*transaction)(nil)
