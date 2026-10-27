package quota

import (
	"context"
	"errors"
	"sync"
	"time"

	"clipp-relay/internal/persistence"
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
func (q *Quota) SetSettlementHooksForTest(before func(context.Context, persistence.AccountingTransaction) error, after func(context.Context) error) {
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
func (q *Quota) Settle(ctx context.Context, batch Settlement) (result RecordedUsage, err error) {
	started := time.Now()
	defer func() { q.accountingDiagnostic("quota.settlement", err, started) }()
	if batch.AccountID == "" || batch.OperationID == "" || batch.DeltaBytes <= 0 || batch.Sequence <= 0 || !batch.WeekStart.Equal(weekStart(batch.WeekStart)) {
		return result, ErrSettlementConflict
	}
	batch.WeekStart = batch.WeekStart.UTC()
	if q.accounting == nil {
		return result, ErrTemporary
	}
	op, startErr := q.accounting.StartSettlement(ctx)
	if startErr != nil {
		return result, ErrTemporary
	}
	defer op.Close()
	err = func(unitCtx context.Context) error {
		key := settlementKey{batch.AccountID, batch.WeekStart}
		s := q.acquireSettlement(key)
		defer q.releaseSettlement(key, s)
		for !s.mu.TryLock() {
			select {
			case <-unitCtx.Done():
				return ErrTemporary
			case <-time.After(time.Millisecond):
			}
		}
		defer s.mu.Unlock()
		if s.unresolved != nil && *s.unresolved != batch {
			return ErrSettlementUnresolved
		}
		var workErr error
		result, workErr = q.settle(op, batch)
		if errors.Is(workErr, ErrSettlementUncertain) {
			copy := batch
			s.unresolved = &copy
		} else if workErr == nil || errors.Is(workErr, ErrUsageRetired) || errors.Is(workErr, ErrSettlementConflict) {
			s.unresolved = nil
		}
		return workErr
	}(op.Context())
	if err != nil && !errors.Is(err, ErrSettlementConflict) && !errors.Is(err, ErrSettlementUncertain) && !errors.Is(err, ErrSettlementUnresolved) && !errors.Is(err, ErrUsageRetired) {
		err = ErrTemporary
	}
	return result, err
}

func (q *Quota) settle(op persistence.AccountingOperation, b Settlement) (RecordedUsage, error) {
	ctx := op.Context()
	tx, err := op.BeginAccounting(ctx)
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	defer tx.Rollback(ctx)
	if q.beforeLock != nil {
		q.beforeLock()
	}
	account, err := tx.LockSettlementAccount(ctx, b.AccountID)
	if errors.Is(err, persistence.ErrUsageRetired) || err == nil && account.Deleted {
		return RecordedUsage{}, ErrUsageRetired
	}
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	now, err := tx.DatabaseTime(ctx)
	if err != nil {
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
	result, err := tx.SettleUsage(ctx, persistence.UsageSettlement{AccountID: b.AccountID, WeekStart: b.WeekStart, OperationID: b.OperationID, DeltaBytes: b.DeltaBytes, Sequence: b.Sequence})
	if errors.Is(err, persistence.ErrConflict) {
		return resultFromPersistence(result), ErrSettlementConflict
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
		if errors.Is(err, persistence.ErrKnownRollback) {
			return RecordedUsage{}, ErrTemporary
		}
		if errors.Is(err, persistence.ErrUncertainCommit) {
			return RecordedUsage{}, ErrSettlementUncertain
		}
		return RecordedUsage{}, ErrTemporary
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
	return resultFromPersistence(result), nil
}

// RecordedUsage reads authoritative durable state without reconstructing lost
// observations. It never authorizes further Relay traffic.
func (q *Quota) RecordedUsage(ctx context.Context, id string, week time.Time) (RecordedUsage, error) {
	result := RecordedUsage{WeekStart: week.UTC()}
	if !week.Equal(weekStart(week)) {
		return result, ErrUsageRetired
	}
	if q.accounting == nil {
		return RecordedUsage{}, ErrTemporary
	}
	stored, err := q.accounting.ReadRecordedUsage(ctx, id, week)
	if errors.Is(err, persistence.ErrUsageRetired) {
		return RecordedUsage{}, ErrUsageRetired
	}
	if err != nil {
		return RecordedUsage{}, ErrTemporary
	}
	result = resultFromPersistence(stored)
	current := weekStart(q.dbTime(stored.DatabaseNow))
	if week.After(current) {
		return RecordedUsage{}, ErrTemporary
	}
	if week.Before(current.AddDate(0, 0, -84)) {
		return RecordedUsage{}, ErrUsageRetired
	}
	return result, nil
}

func resultFromPersistence(stored persistence.RecordedUsage) RecordedUsage {
	return RecordedUsage{RecordedBytes: stored.RecordedBytes, Sequence: stored.Sequence, OperationID: stored.OperationID, DeltaBytes: stored.DeltaBytes, WeekStart: stored.WeekStart}
}
