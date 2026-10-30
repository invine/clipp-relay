package quota

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/persistence"
)

// settlement identifies one immutable batch of observed traffic. Sequence is
// exactly the preceding durable sequence plus one; retries retain every field.
type settlement struct {
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

// SetSettlementHooksForTest controls persistence failures and acknowledgement
// timing without replacing production composition or PostgreSQL.
func (q *Quota) SetSettlementHooksForTest(before func(context.Context, persistence.AccountingTransaction) error, after func(context.Context) error) {
	q.periodic.mu.Lock()
	q.settlementBefore = before
	q.settlementAfter = after
	q.periodic.mu.Unlock()
}

// settle atomically records all observed bytes, even above the allowance or
// after authorization changes. Only final deletion or week retirement seals it.
// The observed Weekly Quota Window owns the in-flight batch and its uncertainty;
// an ambiguous commit blocks successors until that identical batch is reconciled.
func (q *Quota) settle(ctx context.Context, batch settlement) (result RecordedUsage, err error) {
	started := time.Now()
	defer func() { q.accountingDiagnostic("quota.settlement", err, started) }()
	if batch.AccountID == "" || batch.OperationID == "" || batch.DeltaBytes <= 0 || batch.Sequence <= 0 || !batch.WeekStart.Equal(weekStart(batch.WeekStart)) {
		return result, ErrSettlementConflict
	}
	batch.WeekStart = batch.WeekStart.UTC()
	if q.accounting == nil {
		return result, ErrTemporary
	}
	week, created, lockErr := q.lockSettlementWeek(ctx, batch)
	if lockErr != nil {
		return result, lockErr
	}
	defer week.settleMu.Unlock()
	op, startErr := q.accounting.StartSettlement(ctx)
	if startErr != nil {
		return result, ErrTemporary
	}
	defer op.Close()
	result, err = q.settleInOperation(op, batch)
	q.periodic.mu.Lock()
	if week.retired || errors.Is(err, ErrUsageRetired) {
		week.retired = true
		if account := q.periodic.accounts[batch.AccountID]; account != nil && account.weeks[batch.WeekStart] == week {
			delete(account.weeks, batch.WeekStart)
		}
	} else if err == nil {
		week.recorded = result
		week.initialized = true
		week.flight = nil
		week.uncertain = false
	} else if errors.Is(err, ErrSettlementUncertain) {
		week.uncertain = true
	} else if errors.Is(err, ErrSettlementConflict) && created && week.flight != nil && *week.flight == batch {
		week.flight = nil
		week.uncertain = false
	}
	q.periodic.mu.Unlock()
	if err != nil && !errors.Is(err, ErrSettlementConflict) && !errors.Is(err, ErrSettlementUncertain) && !errors.Is(err, ErrSettlementUnresolved) && !errors.Is(err, ErrUsageRetired) {
		err = ErrTemporary
	}
	return result, err
}

// lockSettlementWeek reserves one account/week batch in the same state that
// owns observations, receipts and retirement. Waiting is outside the accounting
// mutex and happens before acquiring a persistence operation/connection.
func (q *Quota) lockSettlementWeek(ctx context.Context, batch settlement) (*observedWeek, bool, error) {
	q.periodic.mu.Lock()
	account := q.observedAccountLocked(batch.AccountID)
	if account == nil {
		q.periodic.mu.Unlock()
		return nil, false, ErrTemporary
	}
	week := account.weeks[batch.WeekStart]
	if week == nil {
		week = &observedWeek{}
		account.weeks[batch.WeekStart] = week
	}
	if week.retired {
		q.periodic.mu.Unlock()
		return nil, false, ErrUsageRetired
	}
	q.periodic.mu.Unlock()
	for !week.settleMu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, false, ErrTemporary
		case <-time.After(time.Millisecond):
		}
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	if week.retired {
		week.settleMu.Unlock()
		return nil, false, ErrUsageRetired
	}
	if week.flight != nil {
		if *week.flight != batch {
			week.settleMu.Unlock()
			return nil, false, ErrSettlementUnresolved
		}
		return week, false, nil
	}
	copy := batch
	week.flight = &copy
	week.uncertain = false
	return week, true, nil
}

func (q *Quota) settleInOperation(op persistence.AccountingOperation, b settlement) (RecordedUsage, error) {
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
	q.periodic.mu.Lock()
	before, after := q.settlementBefore, q.settlementAfter
	q.periodic.mu.Unlock()
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
