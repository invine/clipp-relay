package quota

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Settlement identifies one immutable batch of observed traffic. Sequence is
// exactly the preceding durable sequence plus one; retries retain every field.
type Settlement struct {
	AccountID   string
	WeekStart   time.Time
	OperationID string
	DeltaBytes  int64
	Sequence    int64
}

// RecordedUsage is confirmed durable consumption, including its bounded latest
// receipt. Sequence zero represents an opening total without a settlement.
type RecordedUsage struct {
	RecordedBytes int64
	Sequence      int64
	OperationID   string
	DeltaBytes    int64
	WeekStart     time.Time
}

var ErrSettlementConflict = errors.New("settlement payload conflict or invalid sequence")
var ErrSettlementUncertain = errors.New("settlement commit outcome unknown; reconcile same operation")
var ErrSettlementUnresolved = errors.New("previous settlement must be reconciled")
var ErrUsageRetired = errors.New("account or weekly usage retired")

// SettlementProtection joins complete settlement to Auth's account guards.
// Configure before starting observed accounting; database work is admitted first.
type SettlementProtection interface {
	WithAccountGuards(context.Context, []string, func(context.Context)) bool
}

func (q *Quota) SetSettlementProtection(p SettlementProtection) { q.settlementProtection = p }

type settlementKey struct {
	id   string
	week time.Time
}
type settlementState struct {
	mu         sync.Mutex
	refs       int
	unresolved *Settlement
	retired    bool
}

// SetSettlementHooksForTest controls persistence failures and acknowledgement
// timing without replacing production composition or PostgreSQL.
func (q *Quota) SetSettlementHooksForTest(before func(context.Context, pgx.Tx) error, after func(context.Context) error) {
	q.settlementMu.Lock()
	q.settlementBefore = before
	q.settlementAfter = after
	q.settlementMu.Unlock()
}

func (q *Quota) acquireSettlement(key settlementKey) *settlementState {
	q.settlementMu.Lock()
	defer q.settlementMu.Unlock()
	if q.settlements == nil {
		q.settlements = make(map[settlementKey]*settlementState)
	}
	s := q.settlements[key]
	if s == nil {
		s = &settlementState{}
		q.settlements[key] = s
	}
	s.refs++
	return s
}
func (q *Quota) releaseSettlement(key settlementKey, s *settlementState) {
	q.settlementMu.Lock()
	defer q.settlementMu.Unlock()
	s.refs--
	if s.refs == 0 && (s.unresolved == nil || s.retired) {
		delete(q.settlements, key)
	}
}

// Settle atomically records all observed bytes, even above the allowance or
// after authorization changes. Only final deletion or week retirement seals it.
// An ambiguous commit blocks successors until the identical batch is reconciled.
func (q *Quota) Settle(ctx context.Context, batch Settlement) (RecordedUsage, error) {
	var result RecordedUsage
	if batch.AccountID == "" || batch.OperationID == "" || batch.DeltaBytes <= 0 || batch.Sequence <= 0 || !batch.WeekStart.Equal(weekStart(batch.WeekStart)) {
		return result, ErrSettlementConflict
	}
	batch.WeekStart = batch.WeekStart.UTC()
	err := q.runtime.WithUnit(ctx, func(unitCtx context.Context) error {
		if q.settlementProtection == nil {
			return ErrTemporary
		}
		var workErr error = ErrTemporary
		guarded := q.settlementProtection.WithAccountGuards(unitCtx, []string{batch.AccountID}, func(guardCtx context.Context) {
			key := settlementKey{batch.AccountID, batch.WeekStart}
			s := q.acquireSettlement(key)
			defer q.releaseSettlement(key, s)
			for !s.mu.TryLock() {
				select {
				case <-guardCtx.Done():
					return
				case <-time.After(time.Millisecond):
				}
			}
			defer s.mu.Unlock()
			if s.unresolved != nil && *s.unresolved != batch {
				workErr = ErrSettlementUnresolved
				return
			}
			result, workErr = q.settle(guardCtx, batch)
			if errors.Is(workErr, ErrSettlementUncertain) {
				copy := batch
				s.unresolved = &copy
			} else if workErr == nil || errors.Is(workErr, ErrUsageRetired) || errors.Is(workErr, ErrSettlementConflict) {
				s.unresolved = nil
			}
		})
		if !guarded {
			if errors.Is(workErr, ErrSettlementUncertain) {
				return workErr
			}
			return ErrTemporary
		}
		return workErr
	})
	if err != nil && !errors.Is(err, ErrSettlementConflict) && !errors.Is(err, ErrSettlementUncertain) && !errors.Is(err, ErrSettlementUnresolved) && !errors.Is(err, ErrUsageRetired) {
		err = ErrTemporary
	}
	return result, err
}

func (q *Quota) settle(ctx context.Context, b Settlement) (RecordedUsage, error) {
	tx, err := q.runtime.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	defer tx.Rollback(ctx)
	var deleted *time.Time
	if q.beforeLock != nil {
		q.beforeLock()
	}
	err = tx.QueryRow(ctx, `SELECT deleted_at FROM public.accounts WHERE id=$1 FOR UPDATE`, b.AccountID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted != nil {
		return RecordedUsage{}, ErrUsageRetired
	}
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	now = q.dbTime(now)
	current := weekStart(now)
	if b.WeekStart.After(current) {
		return RecordedUsage{}, ErrTemporary
	}
	if b.WeekStart.Before(current.AddDate(0, 0, -84)) {
		return RecordedUsage{}, ErrUsageRetired
	}
	result := RecordedUsage{WeekStart: b.WeekStart}
	err = tx.QueryRow(ctx, `SELECT recorded_bytes,sequence,COALESCE(latest_operation_id::text,''),latest_delta_bytes FROM public.weekly_relay_usage WHERE account_id=$1 AND week_start=$2`, b.AccountID, b.WeekStart).Scan(&result.RecordedBytes, &result.Sequence, &result.OperationID, &result.DeltaBytes)
	absent := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !absent {
		return RecordedUsage{}, ErrTemporary
	}
	if result.OperationID == b.OperationID {
		if result.Sequence != b.Sequence || result.DeltaBytes != b.DeltaBytes {
			return result, ErrSettlementConflict
		}
		// The locked row confirms the prior commit; no fresh commit is needed.
		return result, nil
	}
	if result.Sequence == math.MaxInt64 || b.Sequence != result.Sequence+1 || b.DeltaBytes > math.MaxInt64-result.RecordedBytes {
		return result, ErrSettlementConflict
	}
	if absent {
		_, err = tx.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes) VALUES($1,$2,$3,$4,$5,$3)`, b.AccountID, b.WeekStart, b.DeltaBytes, b.Sequence, b.OperationID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE public.weekly_relay_usage SET recorded_bytes=recorded_bytes+$3,sequence=$4,latest_operation_id=$5,latest_delta_bytes=$3 WHERE account_id=$1 AND week_start=$2`, b.AccountID, b.WeekStart, b.DeltaBytes, b.Sequence, b.OperationID)
	}
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	q.settlementMu.Lock()
	before, after := q.settlementBefore, q.settlementAfter
	q.settlementMu.Unlock()
	if before != nil {
		if err = before(ctx, tx); err != nil {
			return RecordedUsage{}, ErrTemporary
		}
	}
	if q.beforeCommit != nil {
		if err = q.beforeCommit(ctx, tx); err != nil {
			return RecordedUsage{}, ErrTemporary
		}
	}
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return RecordedUsage{}, ErrTemporary
		}
		return RecordedUsage{}, ErrSettlementUncertain
	}
	if after != nil {
		if err = after(ctx); err != nil {
			return RecordedUsage{}, ErrSettlementUncertain
		}
	}
	if q.afterCommit != nil {
		if err = q.afterCommit(); err != nil {
			return RecordedUsage{}, ErrSettlementUncertain
		}
	}
	if ctx.Err() != nil {
		return RecordedUsage{}, ErrSettlementUncertain
	}
	result.RecordedBytes += b.DeltaBytes
	result.Sequence = b.Sequence
	result.OperationID = b.OperationID
	result.DeltaBytes = b.DeltaBytes
	return result, nil
}

// RecordedUsage reads authoritative durable state without reconstructing lost
// observations. It never authorizes further Relay traffic.
func (q *Quota) RecordedUsage(ctx context.Context, id string, week time.Time) (RecordedUsage, error) {
	result := RecordedUsage{WeekStart: week.UTC()}
	if !week.Equal(weekStart(week)) {
		return result, ErrUsageRetired
	}
	var now time.Time
	var deleted *time.Time
	err := q.runtime.QueryRow(ctx, `SELECT a.deleted_at,clock_timestamp(),COALESCE(u.recorded_bytes,0),COALESCE(u.sequence,0),COALESCE(u.latest_operation_id::text,''),COALESCE(u.latest_delta_bytes,0) FROM public.accounts a LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id AND u.week_start=$2 WHERE a.id=$1`, id, week).Scan(&deleted, &now, &result.RecordedBytes, &result.Sequence, &result.OperationID, &result.DeltaBytes)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted != nil {
		return RecordedUsage{}, ErrUsageRetired
	}
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	current := weekStart(q.dbTime(now))
	if week.After(current) {
		return RecordedUsage{}, ErrTemporary
	}
	if week.Before(current.AddDate(0, 0, -84)) {
		return RecordedUsage{}, ErrUsageRetired
	}
	return result, nil
}
