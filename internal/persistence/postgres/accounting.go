package postgres

import (
	"context"
	"errors"
	"math"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

var _ persistence.Accounting = (*Work)(nil)

func (w *Work) ProbeDatabaseTime(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := w.runtime.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, unavailable(err)
	}
	return now, nil
}

func (w *Work) ReadAccountingEligibility(ctx context.Context, accountID string) (persistence.AccountingEligibility, error) {
	var result persistence.AccountingEligibility
	err := w.runtime.QueryRow(ctx, `WITH observed AS MATERIALIZED (SELECT clock_timestamp() AS now)
		SELECT observed.now,COALESCE(a.weekly_bytes_override,p.weekly_bytes),COALESCE(a.sessions_override,p.sessions),
		COALESCE(u.recorded_bytes,0),COALESCE(u.sequence,0),COALESCE(u.latest_operation_id::text,''),COALESCE(u.latest_delta_bytes,0)
		FROM observed CROSS JOIN public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id
		LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id
			AND u.week_start=date_trunc('week',observed.now AT TIME ZONE 'UTC')::date
		WHERE a.id=$1 AND a.deleted_at IS NULL AND a.deletion_started_at IS NULL`, accountID).
		Scan(&result.DatabaseNow, &result.CapBytes, &result.SessionLimit, &result.RecordedBytes,
			&result.Sequence, &result.OperationID, &result.DeltaBytes)
	if err != nil {
		return persistence.AccountingEligibility{}, unavailable(err)
	}
	return result, nil
}

func (w *Work) ReadRecordedUsage(ctx context.Context, accountID string, week time.Time) (persistence.RecordedUsage, error) {
	var result persistence.RecordedUsage
	result.WeekStart = week.UTC()
	var deleted *time.Time
	err := w.runtime.QueryRow(ctx, `SELECT a.deleted_at,clock_timestamp(),
		COALESCE(u.recorded_bytes,0),COALESCE(u.sequence,0),COALESCE(u.latest_operation_id::text,''),COALESCE(u.latest_delta_bytes,0)
		FROM public.accounts a LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id AND u.week_start=$2
		WHERE a.id=$1`, accountID, week).Scan(&deleted, &result.DatabaseNow, &result.RecordedBytes,
		&result.Sequence, &result.OperationID, &result.DeltaBytes)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted != nil {
		return persistence.RecordedUsage{}, persistence.ErrUsageRetired
	}
	if err != nil {
		return persistence.RecordedUsage{}, unavailable(err)
	}
	return result, nil
}

func (w *Work) StartSettlement(parent context.Context) (persistence.AccountingOperation, error) {
	ctx, release, err := w.runtime.StartUnit(parent)
	if err != nil {
		return nil, unavailable(err)
	}
	return &accountingOperation{runtime: w.runtime, ctx: ctx, release: release}, nil
}

type accountingOperation struct {
	runtime *database.Runtime
	ctx     context.Context
	release func()
}

func (o *accountingOperation) Context() context.Context { return o.ctx }
func (o *accountingOperation) Close()                   { o.release() }

func (o *accountingOperation) BeginAccounting(ctx context.Context) (persistence.AccountingTransaction, error) {
	if err := o.ctx.Err(); err != nil {
		return nil, unavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, unavailable(err)
	}
	tx, err := o.runtime.Begin(o.ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	return &accountingTransaction{tx: tx}, nil
}

type accountingTransaction struct{ tx pgx.Tx }

func (t *accountingTransaction) LockSettlementAccount(ctx context.Context, accountID string) (persistence.SettlementAccount, error) {
	var deleted *time.Time
	err := t.tx.QueryRow(ctx, `SELECT deleted_at FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted != nil {
		return persistence.SettlementAccount{}, persistence.ErrUsageRetired
	}
	if err != nil {
		return persistence.SettlementAccount{}, unavailable(err)
	}
	return persistence.SettlementAccount{}, nil
}

func (t *accountingTransaction) DatabaseTime(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := t.tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, unavailable(err)
	}
	return now, nil
}

func (t *accountingTransaction) SettleUsage(ctx context.Context, batch persistence.UsageSettlement) (persistence.RecordedUsage, error) {
	result := persistence.RecordedUsage{WeekStart: batch.WeekStart}
	err := t.tx.QueryRow(ctx, `SELECT recorded_bytes,sequence,COALESCE(latest_operation_id::text,''),latest_delta_bytes
		FROM public.weekly_relay_usage WHERE account_id=$1 AND week_start=$2`, batch.AccountID, batch.WeekStart).
		Scan(&result.RecordedBytes, &result.Sequence, &result.OperationID, &result.DeltaBytes)
	absent := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !absent {
		return persistence.RecordedUsage{}, unavailable(err)
	}
	if result.OperationID == batch.OperationID {
		if result.Sequence != batch.Sequence || result.DeltaBytes != batch.DeltaBytes {
			return result, persistence.ErrConflict
		}
		return result, nil
	}
	if result.Sequence == math.MaxInt64 || batch.Sequence != result.Sequence+1 || batch.DeltaBytes > math.MaxInt64-result.RecordedBytes {
		return result, persistence.ErrConflict
	}
	if absent {
		_, err = t.tx.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes)
			VALUES($1,$2,$3,$4,$5,$3)`, batch.AccountID, batch.WeekStart, batch.DeltaBytes, batch.Sequence, batch.OperationID)
	} else {
		_, err = t.tx.Exec(ctx, `UPDATE public.weekly_relay_usage SET recorded_bytes=recorded_bytes+$3,sequence=$4,latest_operation_id=$5,latest_delta_bytes=$3
			WHERE account_id=$1 AND week_start=$2`, batch.AccountID, batch.WeekStart, batch.DeltaBytes, batch.Sequence, batch.OperationID)
	}
	if err != nil {
		return persistence.RecordedUsage{}, unavailable(err)
	}
	result.RecordedBytes += batch.DeltaBytes
	result.Sequence = batch.Sequence
	result.OperationID = batch.OperationID
	result.DeltaBytes = batch.DeltaBytes
	return result, nil
}

func (t *accountingTransaction) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return persistence.ErrKnownRollback
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return errors.Join(persistence.ErrUncertainCommit, err)
		}
		return persistence.ErrUncertainCommit
	}
	return nil
}

func (t *accountingTransaction) Rollback(ctx context.Context) error {
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return unavailable(err)
	}
	return nil
}
