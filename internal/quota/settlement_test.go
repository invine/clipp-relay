package quota

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
)

func TestRecordedUsageSettlesFullOverrunAndIdenticalRetry(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 1000000, Sequence: 1}
	first, err := q.Settle(ctx, batch)
	if err != nil || first.RecordedBytes != 1000000 || first.Sequence != 1 {
		t.Fatalf("settlement=%+v %v", first, err)
	}
	second, err := q.Settle(ctx, batch)
	if err != nil || second != first {
		t.Fatalf("retry=%+v %v", second, err)
	}
	durable, err := q.RecordedUsage(ctx, id, week)
	if err != nil || durable != first {
		t.Fatalf("durable=%+v %v", durable, err)
	}
}

func TestRecordedUsageRollbackAndLostCommitReply(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 987654, Sequence: 1}
	q.beforeCommit = func(context.Context, persistence.AccountingTransaction) error {
		return errors.New("known failure before commit")
	}
	if _, err := q.Settle(ctx, batch); !errors.Is(err, ErrTemporary) {
		t.Fatalf("rollback=%v", err)
	}
	durable, err := q.RecordedUsage(ctx, id, week)
	if err != nil || durable.RecordedBytes != 0 || durable.Sequence != 0 {
		t.Fatalf("rollback left partial state=%+v %v", durable, err)
	}
	q.beforeCommit = nil
	q.afterCommit = func() error { return errors.New("reply lost") }
	if _, err := q.Settle(ctx, batch); !errors.Is(err, ErrSettlementUncertain) {
		t.Fatalf("unknown=%v", err)
	}
	durable, err = q.RecordedUsage(ctx, id, week)
	if err != nil || durable.RecordedBytes != 987654 || durable.Sequence != 1 {
		t.Fatalf("lost reply lost debit=%+v %v", durable, err)
	}
	next := batch
	next.OperationID, _ = NewOperationID()
	next.Sequence = 2
	next.DeltaBytes = 1
	if _, err := q.Settle(ctx, next); !errors.Is(err, ErrSettlementUnresolved) {
		t.Fatalf("successor bypassed reconciliation: %v", err)
	}
	// A query cannot resolve the immutable pending operation. Its same retry does.
	q.afterCommit = nil
	retried, err := q.Settle(ctx, batch)
	if err != nil || retried != durable {
		t.Fatalf("reconciliation=%+v %v", retried, err)
	}
	if result, err := q.Settle(ctx, next); err != nil || result.RecordedBytes != 987655 || result.Sequence != 2 {
		t.Fatalf("successor=%+v %v", result, err)
	}
}

func TestRecordedUsageRejectsConflictsGapsAndStaleRetriesAcrossRestart(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	first := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 80000, Sequence: 1}
	if _, err := q.Settle(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Settlement){
		func(b *Settlement) { b.DeltaBytes++ },
		func(b *Settlement) { b.Sequence++ },
		func(b *Settlement) { b.OperationID, _ = NewOperationID() },
		func(b *Settlement) { b.OperationID, _ = NewOperationID(); b.Sequence = 3 },
		func(b *Settlement) { b.DeltaBytes = 0 },
		func(b *Settlement) { b.DeltaBytes = -1 },
	} {
		invalid := first
		change(&invalid)
		if _, err := q.Settle(ctx, invalid); !errors.Is(err, ErrSettlementConflict) {
			t.Fatalf("invalid %+v: %v", invalid, err)
		}
	}
	next := first
	next.OperationID, _ = NewOperationID()
	next.Sequence = 2
	next.DeltaBytes = 7
	if _, err := q.Settle(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Settle(ctx, first); !errors.Is(err, ErrSettlementConflict) {
		t.Fatalf("stale=%v", err)
	}
	restarted := New(postgres.New(database.NewRuntime(pool)))
	t.Cleanup(restarted.Close)
	durable, err := restarted.RecordedUsage(ctx, id, week)
	if err != nil || durable.RecordedBytes != 80007 || durable.Sequence != 2 || durable.OperationID != next.OperationID {
		t.Fatalf("restart=%+v %v", durable, err)
	}
	if repeated, err := restarted.Settle(ctx, next); err != nil || repeated != durable {
		t.Fatalf("restart identical=%+v %v", repeated, err)
	}
	if _, err := restarted.Settle(ctx, first); !errors.Is(err, ErrSettlementConflict) {
		t.Fatalf("restart stale=%v", err)
	}
}

func TestRecordedUsageCancellationAfterCommitRemainsUncertain(t *testing.T) {
	q, id, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 33, Sequence: 1}
	q.afterCommit = func() error { cancel(); return nil }
	if _, err := q.Settle(ctx, batch); !errors.Is(err, ErrSettlementUncertain) {
		t.Fatalf("committed cancellation lost uncertainty: %v", err)
	}
	q.afterCommit = nil
	result, err := q.Settle(context.Background(), batch)
	if err != nil || result.RecordedBytes != 33 || result.Sequence != 1 {
		t.Fatalf("reconciliation=%+v %v", result, err)
	}
}

func TestRecordedUsageSettlesAfterPolicyChangesButNeverResurrectsDeletedAccount(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET status='Suspended',credential_generation=credential_generation+1,weekly_bytes_override=0,deletion_started_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q.ApplyAccountingPolicy(id, 1<<30)
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 999999, Sequence: 1}
	if result, err := q.Settle(ctx, batch); err != nil || result.RecordedBytes != 999999 {
		t.Fatalf("observations discarded on policy change=%+v %v", result, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE public.accounts SET issuer=NULL,subject=NULL,email=NULL,email_verified=NULL,hosted_domain=NULL,plan_id=NULL,weekly_bytes_override=NULL,sessions_override=NULL,status='Pending',deleted_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	q.beforeLock = func() { close(started) }
	finished := make(chan error, 1)
	go func() { _, err := q.Settle(ctx, batch); finished <- err }()
	<-started
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; !errors.Is(err, ErrUsageRetired) {
		t.Fatalf("late retry reopened finalized account: %v", err)
	}
	q.beforeLock = nil
	if _, err = q.RecordedUsage(ctx, id, week); !errors.Is(err, ErrUsageRetired) {
		t.Fatalf("deleted read=%v", err)
	}
	// A fresh account for the same external identity must not inherit the late batch.
	replacement, _ := NewOperationID()
	if _, err = pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'new@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Pending','6dd09395-51a0-451c-96b3-716e6038e870')`, replacement, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id=$1`, replacement) })
	result, err := q.RecordedUsage(ctx, replacement, week)
	if err != nil || result.RecordedBytes != 0 || result.Sequence != 0 {
		t.Fatalf("old batch redirected=%+v %v", result, err)
	}
	absent, _ := NewOperationID()
	batch.AccountID = absent
	if _, err = q.Settle(ctx, batch); !errors.Is(err, ErrUsageRetired) {
		t.Fatalf("absent account recreated: %v", err)
	}
}

func TestRecordedUsageRetirementUsesDatabaseTimeAfterAccountWait(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week.AddDate(0, 0, -84), OperationID: op, DeltaBytes: 71, Sequence: 1}
	// The oldest retained window is still writable until the next Monday.
	if _, err := q.Settle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// Simulate retention removing the row as its window retires.
	if _, err := pool.Exec(ctx, `DELETE FROM public.weekly_relay_usage WHERE account_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var elapsedDays atomic.Int64
	q.adjustTime = func(now time.Time) time.Time { return now.AddDate(0, 0, int(elapsedDays.Load())) }
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM public.accounts WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	q.beforeLock = func() { close(started) }
	finished := make(chan error, 1)
	go func() { _, err := q.Settle(ctx, batch); finished <- err }()
	<-started
	elapsedDays.Store(7)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; !errors.Is(err, ErrUsageRetired) {
		t.Fatalf("retired window recreated after account wait: %v", err)
	}
	q.beforeLock = nil
	q.adjustTime = nil
	result, err := q.RecordedUsage(ctx, id, batch.WeekStart)
	if err != nil || result.RecordedBytes != 0 || result.Sequence != 0 {
		t.Fatalf("retired write persisted=%+v %v", result, err)
	}
	batch.WeekStart = week.AddDate(0, 0, 7)
	if _, err = q.Settle(ctx, batch); !errors.Is(err, ErrTemporary) {
		t.Fatalf("future window must remain temporarily unsettled: %v", err)
	}
	batch.WeekStart = week.Add(time.Hour)
	if _, err = q.Settle(ctx, batch); !errors.Is(err, ErrSettlementConflict) {
		t.Fatalf("unaligned window accepted: %v", err)
	}
}

func TestRecordedUsageOpeningTotalAndOverflow(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	if _, err := pool.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_delta_bytes) VALUES($1,$2,$3,0,0)`, id, week, int64(math.MaxInt64-9)); err != nil {
		t.Fatal(err)
	}
	opening, err := q.RecordedUsage(ctx, id, week)
	if err != nil || opening.RecordedBytes != math.MaxInt64-9 || opening.Sequence != 0 || opening.OperationID != "" || opening.DeltaBytes != 0 {
		t.Fatalf("opening=%+v %v", opening, err)
	}
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 8, Sequence: 1}
	if result, err := q.Settle(ctx, batch); err != nil || result.RecordedBytes != math.MaxInt64-1 {
		t.Fatalf("opening settlement=%+v %v", result, err)
	}
	batch.OperationID, _ = NewOperationID()
	batch.Sequence = 2
	batch.DeltaBytes = 2
	if _, err = q.Settle(ctx, batch); !errors.Is(err, ErrSettlementConflict) {
		t.Fatalf("overflow=%v", err)
	}
	durable, err := q.RecordedUsage(ctx, id, week)
	if err != nil || durable.RecordedBytes != math.MaxInt64-1 || durable.Sequence != 1 {
		t.Fatalf("overflow changed total=%+v %v", durable, err)
	}
}

func TestRecordedUsageSettlesWithoutAuthorizationProtection(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	want := RecordedUsage{RecordedBytes: 5, Sequence: 1, OperationID: op, DeltaBytes: 5, WeekStart: week}
	if got, err := q.Settle(ctx, Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 5, Sequence: 1}); err != nil || got != want {
		t.Fatalf("settlement without authorization guard=%+v err=%v", got, err)
	}
	durable, err := q.RecordedUsage(ctx, id, week)
	if err != nil || durable != want {
		t.Fatalf("durable=%+v %v", durable, err)
	}
}

func TestRecordedUsageCommitRollbackIsKnownFailure(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	week := weekStart(time.Now())
	op, _ := NewOperationID()
	batch := Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: 19, Sequence: 1}
	q.beforeCommit = func(context.Context, persistence.AccountingTransaction) error {
		return errors.New("known failure before commit")
	}
	if _, err := q.Settle(ctx, batch); !errors.Is(err, ErrTemporary) || errors.Is(err, ErrSettlementUncertain) {
		t.Fatalf("known commit rollback=%v", err)
	}
	durable, err := q.RecordedUsage(ctx, id, week)
	if err != nil || durable.RecordedBytes != 0 || durable.Sequence != 0 {
		t.Fatalf("commit rollback partial=%+v %v", durable, err)
	}
	q.beforeCommit = nil
	if result, err := q.Settle(ctx, batch); err != nil || result.RecordedBytes != 19 || result.Sequence != 1 {
		t.Fatalf("rollback retry=%+v %v", result, err)
	}
}
