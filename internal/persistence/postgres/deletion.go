package postgres

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Deletions struct{ runtime *database.Runtime }

func NewDeletions(runtime *database.Runtime) *Deletions { return &Deletions{runtime: runtime} }

func (d *Deletions) BeginDeletion(ctx context.Context) (persistence.DeletionTransaction, error) {
	op, err := New(d.runtime).Start(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := op.Begin(op.Context())
	if err != nil {
		op.Close()
		return nil, err
	}
	deletion, ok := tx.(persistence.DeletionTransaction)
	if !ok {
		_ = tx.Rollback(op.Context())
		op.Close()
		return nil, persistence.ErrUnavailable
	}
	return &deletionTx{DeletionTransaction: deletion, operation: op}, nil
}

type deletionTx struct {
	persistence.DeletionTransaction
	operation persistence.Operation
}

func (t *deletionTx) Commit(ctx context.Context) error {
	defer t.operation.Close()
	return t.DeletionTransaction.Commit(ctx)
}
func (t *deletionTx) Rollback(ctx context.Context) error {
	defer t.operation.Close()
	return t.DeletionTransaction.Rollback(ctx)
}

func (t *transaction) DeletionOwner(ctx context.Context, id string) (persistence.DeletionOwner, error) {
	var out persistence.DeletionOwner
	err := t.tx.QueryRow(ctx, `SELECT subject FROM public.accounts WHERE id=$1 AND deletion_started_at IS NULL AND deleted_at IS NULL`, id).Scan(&out.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, persistence.ErrNotFound
	}
	if err != nil {
		return out, unavailable(err)
	}
	return out, nil
}

func (t *transaction) AdmitDeletion(ctx context.Context, r persistence.DeletionAdmissionRequest) (persistence.DeletionAdmission, error) {
	var out persistence.DeletionAdmission
	var subject string
	var live, recent bool
	err := t.tx.QueryRow(ctx, `SELECT a.subject,ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp() AND ps.credential_generation=a.credential_generation,
ps.google_authenticated_at>=clock_timestamp()-interval '10 minutes' AND ps.google_authenticated_at<=clock_timestamp(),
COALESCE(a.weekly_bytes_override,p.weekly_bytes,0),COALESCE(a.sessions_override,p.sessions,0)
FROM public.accounts a LEFT JOIN public.quota_plans p ON p.id=a.plan_id JOIN public.portal_sessions ps ON ps.account_id=a.id
WHERE a.id=$1 AND a.subject=$4 AND a.deletion_started_at IS NULL AND a.deleted_at IS NULL AND ps.credential_digest=$2 AND ps.pepper_version=$3 FOR UPDATE OF a`, r.AccountID, r.SessionDigest, r.PepperVersion, r.ExpectedSubject).Scan(&subject, &live, &recent, &out.WeeklyBytes, &out.Sessions)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, persistence.ErrInvalidCredential
	}
	if err != nil {
		return out, unavailable(err)
	}
	if !live {
		return out, persistence.ErrInvalidCredential
	}
	if !recent {
		return out, persistence.ErrRecentAuthentication
	}
	var pending int
	if err = t.tx.QueryRow(ctx, `SELECT pending FROM public.deletion_capacity WHERE singleton=true FOR UPDATE`).Scan(&pending); err != nil {
		return out, unavailable(err)
	}
	if pending >= 1024 {
		return out, persistence.ErrCapacityExceeded
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO public.deletion_operations(id,account_id,admitted_at,next_attempt_at) VALUES($1,$2,clock_timestamp(),clock_timestamp())`, r.OperationID, r.AccountID); err != nil {
		return out, unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.accounts SET deletion_started_at=clock_timestamp(),credential_generation=credential_generation+1 WHERE id=$1`, r.AccountID); err != nil {
		return out, unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE account_id=$1 AND terminated_at IS NULL`, r.AccountID); err != nil {
		return out, unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1`, r.AccountID); err != nil {
		return out, unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=pending+1 WHERE singleton=true`); err != nil {
		return out, unavailable(err)
	}
	out.Subject = subject
	return out, nil
}

func (d *Deletions) PrepareDeletionEvent(ctx context.Context, p persistence.DeletionEventPreparation) (bool, error) {
	var tag pgconn.CommandTag
	var err error
	if p.ExpectedSequence == nil {
		tag, err = d.runtime.Exec(ctx, `UPDATE public.deletion_operations SET event_sequence=$2,event_hash=$3,event_data=$4,journal_generation=$5 WHERE id=$1 AND event_sequence IS NULL AND completed_at IS NULL`, p.OperationID, p.Sequence, p.Hash, p.Data, p.Generation)
	} else {
		tag, err = d.runtime.Exec(ctx, `UPDATE public.deletion_operations SET event_sequence=$2,event_hash=$3,event_data=$4,journal_generation=$5 WHERE id=$1 AND event_sequence=$6 AND event_hash=$7 AND journal_generation=$8 AND completed_at IS NULL`, p.OperationID, p.Sequence, p.Hash, p.Data, p.Generation, *p.ExpectedSequence, p.ExpectedHash, *p.ExpectedGeneration)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return false, persistence.ErrKnownRollback
		}
		if errors.Is(err, database.ErrWorkUnavailable) || errors.Is(err, database.ErrPoolUnavailable) {
			return false, persistence.ErrUnavailable
		}
		return false, persistence.ErrUncertainCommit
	}
	return tag.RowsAffected() == 1, nil
}

func (t *transaction) FinalizeDeletion(ctx context.Context, operationID, accountID, expectedSubject string, pepper uint64, digest []byte, auditID string) error {
	var subject *string
	var deleted *time.Time
	err := t.tx.QueryRow(ctx, `SELECT subject,deleted_at FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&subject, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.ErrNotFound
	}
	if err != nil {
		return unavailable(err)
	}
	if deleted != nil || subject == nil || *subject != expectedSubject {
		return persistence.ErrConflict
	}
	var completed *time.Time
	err = t.tx.QueryRow(ctx, `SELECT completed_at FROM public.deletion_operations WHERE id=$1 AND account_id=$2 FOR UPDATE`, operationID, accountID).Scan(&completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.ErrNotFound
	}
	if err != nil {
		return unavailable(err)
	}
	if completed != nil {
		return persistence.ErrConflict
	}
	var week time.Time
	var recorded int64
	err = t.tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week)
	if err != nil {
		return unavailable(err)
	}
	err = t.tx.QueryRow(ctx, `SELECT recorded_bytes FROM public.weekly_relay_usage WHERE account_id=$1 AND week_start=$2`, accountID, week).Scan(&recorded)
	if err == nil {
		if _, err = t.tx.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,recorded_bytes) VALUES($1,$2,$3,$4)`, pepper, digest, week, recorded); err != nil {
			return unavailable(err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.accounts SET issuer=NULL,subject=NULL,email=NULL,email_verified=NULL,hosted_domain=NULL,plan_id=NULL,weekly_bytes_override=NULL,sessions_override=NULL,status='Pending',deleted_at=clock_timestamp(),credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		return unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'account_deleted',$2)`, auditID, accountID); err != nil {
		return unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.deletion_operations SET completed_at=clock_timestamp() WHERE id=$1`, operationID); err != nil {
		return unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=pending-1 WHERE singleton=true`); err != nil {
		return unavailable(err)
	}
	return nil
}
func (d *Deletions) DeletionCommitted(ctx context.Context, id string) (bool, error) {
	var complete bool
	err := d.runtime.QueryRow(ctx, `SELECT d.completed_at IS NOT NULL AND a.deleted_at IS NOT NULL AND EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=a.id AND e.event='account_deleted') FROM public.deletion_operations d JOIN public.accounts a ON a.id=d.account_id WHERE d.id=$1`, id).Scan(&complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, persistence.ErrUnavailable
	}
	return complete, nil
}

func (d *Deletions) NextDeletion(ctx context.Context) (persistence.DeletionOperation, error) {
	var x persistence.DeletionOperation
	err := d.runtime.QueryRow(ctx, `SELECT id,account_id,admitted_at,event_sequence,event_hash,event_data,journal_generation,attempts FROM public.deletion_operations WHERE completed_at IS NULL AND next_attempt_at<=clock_timestamp() ORDER BY next_attempt_at,admitted_at,id LIMIT 1`).Scan(&x.ID, &x.AccountID, &x.AdmittedAt, &x.EventSequence, &x.EventHash, &x.EventData, &x.JournalGeneration, &x.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, persistence.ErrNotFound
	}
	if err != nil {
		return x, persistence.ErrUnavailable
	}
	return x, nil
}
func (d *Deletions) DeletionStatus(ctx context.Context, id string) (bool, error) {
	var completed *time.Time
	err := d.runtime.QueryRow(ctx, `SELECT completed_at FROM public.deletion_operations WHERE id=$1`, id).Scan(&completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, persistence.ErrNotFound
	}
	if err != nil {
		return false, persistence.ErrUnavailable
	}
	return completed != nil, nil
}
func (d *Deletions) DeletionIdentity(ctx context.Context, id string) (string, error) {
	var subject string
	err := d.runtime.QueryRow(ctx, `SELECT subject FROM public.accounts WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", persistence.ErrNotFound
	}
	if err != nil {
		return "", persistence.ErrUnavailable
	}
	return subject, nil
}
func (d *Deletions) RecordDeletionRetry(ctx context.Context, id string, attempt int, seconds float64) error {
	_, err := d.runtime.Exec(ctx, `UPDATE public.deletion_operations SET attempts=attempts+1,next_attempt_at=clock_timestamp()+make_interval(secs=>$2) WHERE id=$1 AND completed_at IS NULL`, id, seconds)
	if err != nil {
		return persistence.ErrUnavailable
	}
	return nil
}
func (d *Deletions) DeletionSignals(ctx context.Context) (persistence.DeletionReadSignals, error) {
	var v persistence.DeletionReadSignals
	var sec float64
	err := d.runtime.QueryRow(ctx, `SELECT count(*),COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(admitted_at)),0) FROM public.deletion_operations WHERE completed_at IS NULL`).Scan(&v.Pending, &sec)
	if err != nil {
		return v, persistence.ErrUnavailable
	}
	v.OldestAge = time.Duration(sec * float64(time.Second))
	return v, nil
}

var _ persistence.DeletionRepository = (*Deletions)(nil)
var _ persistence.DeletionTransaction = (*transaction)(nil)
