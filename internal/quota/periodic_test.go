package quota

import (
	"context"
	"github.com/jackc/pgx/v5"
	"sync"
	"testing"
	"time"
)

func TestPeriodicUsageAggregatesFullOverrun(t *testing.T) {
	q, id, pool := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	if _, err := pool.Exec(context.Background(), `UPDATE public.accounts SET weekly_bytes_override=100 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Eligibility(context.Background(), id, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 60); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 70); err != nil {
		t.Fatal(err)
	}
	if err := q.FlushAccounting(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	usage, err := q.RecordedUsage(context.Background(), id, weekStart(time.Now()))
	if err != nil || usage.RecordedBytes != 130 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	if _, err := q.Eligibility(context.Background(), id, 0); err != ErrExhausted {
		t.Fatalf("eligibility=%v", err)
	}
}

func TestPeriodicObservationsSurviveLostAcknowledgmentWithoutDoubleCounting(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 80); err != nil {
		t.Fatal(err)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	q.afterCommit = func() error { close(committed); <-release; return ErrSettlementUncertain }
	done := make(chan error, 1)
	go func() { done <- q.FlushAccounting(ctx, id) }()
	<-committed
	if err := q.Observe(id, 60); err != nil {
		t.Fatal(err)
	}
	current, err := q.Eligibility(ctx, id, 0)
	if err != nil || current.RecordedBytes != 80 || current.EffectiveBytes != 140 {
		t.Fatalf("during ack=%+v %v", current, err)
	}
	close(release)
	if err := <-done; err != ErrSettlementUncertain {
		t.Fatalf("lost ack=%v", err)
	}
	q.afterCommit = nil
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	// The retry acknowledges only 80; the 60 observed during persistence remain.
	current, err = q.Eligibility(ctx, id, 0)
	if err != nil || current.RecordedBytes != 80 || current.EffectiveBytes != 140 {
		t.Fatalf("after retry=%+v %v", current, err)
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	current, err = q.Eligibility(ctx, id, 0)
	if err != nil || current.RecordedBytes != 140 || current.EffectiveBytes != 140 {
		t.Fatalf("final=%+v %v", current, err)
	}
}

func TestPeriodicCutoffIsIndependentOfBlockedPersistence(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	now := time.Now()
	q.periodic.monotonicNow = func() time.Time { return now }
	closed := make(chan ClosureOutcome, 1)
	q.periodic.close = func(out ClosureOutcome) { closed <- out }
	initial, err := q.Eligibility(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 91); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	q.beforeCommit = func(context.Context, pgx.Tx) error { close(entered); <-release; return ErrTemporary }
	done := make(chan error, 1)
	go func() { done <- q.FlushAccounting(ctx, id) }()
	<-entered
	now = now.Add(DefaultAccountingFailureDeadline)
	// A successful authoritative read while a write is pending cannot mask it.
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("stale eligibility=%v", err)
	}
	q.CheckAccountingHealth()
	select {
	case out := <-closed:
		if out.Reason != "accounting_unavailable" || !q.ClosureCurrent(out) {
			t.Fatalf("closure=%+v", out)
		}
	default:
		t.Fatal("blocked persistence delayed deadline")
	}
	if q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("in-flight admission retained authority")
	}
	close(release)
	<-done
	q.beforeCommit = nil
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatalf("recovery=%v", err)
	}
}

func TestPeriodicConcurrentObservationsRemainAccountScopedAfterRevocation(t *testing.T) {
	q, id, pool := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if err := q.Observe(id, 7); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET status='Suspended',credential_generation=credential_generation+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q.Invalidate(id)
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	usage, err := q.RecordedUsage(ctx, id, weekStart(time.Now()))
	if err != nil || usage.RecordedBytes != 8400 {
		t.Fatalf("revoked observed usage=%+v %v", usage, err)
	}
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("revocation restored authority: %v", err)
	}
}

func TestPeriodicPriorWeekPendingCannotBeHiddenByNewWeekRead(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	now := time.Now()
	q.periodic.monotonicNow = func() time.Time { return now }
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 123); err != nil {
		t.Fatal(err)
	}
	prior := weekStart(time.Now())
	// Move both authoritative and processing clocks, while monotonic elapsed
	// reaches the cutoff. The prior week's original pending state must survive.
	q.adjustTime = func(t time.Time) time.Time { return t.AddDate(0, 0, 7) }
	now = now.Add(DefaultAccountingFailureDeadline)
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("new week masked old pending=%v", err)
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	usage, err := q.RecordedUsage(ctx, id, prior)
	if err != nil || usage.RecordedBytes != 123 {
		t.Fatalf("original week=%+v %v", usage, err)
	}
	current, err := q.RecordedUsage(ctx, id, prior.AddDate(0, 0, 7))
	if err != nil || current.RecordedBytes != 0 {
		t.Fatalf("new week=%+v %v", current, err)
	}
}

func TestPeriodicConfiguredDeadlineAndIdleRefresh(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	now := time.Now()
	q.periodic.monotonicNow = func() time.Time { return now }
	for _, invalid := range []time.Duration{0, -time.Second, time.Second - time.Nanosecond} {
		if q.ConfigureAccountingFailureDeadline(invalid) == nil {
			t.Fatalf("accepted deadline %v", invalid)
		}
	}
	if err := q.ConfigureAccountingFailureDeadline(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	initial, err := q.Eligibility(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatalf("idle refresh: %v", err)
	}
	usage, err := q.RecordedUsage(ctx, id, initial.WeekStart)
	if err != nil || usage.Sequence != 0 || usage.RecordedBytes != 0 {
		t.Fatalf("idle read debited usage: %+v %v", usage, err)
	}
	if err := q.Observe(id, 41); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	q.CheckAccountingHealth()
	if !q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("configured deadline ignored")
	}
	now = now.Add(time.Second)
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("read masked pending write at deadline: %v", err)
	}
	q.CheckAccountingHealth()
	if q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("deadline retained admission authority")
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	recovered, err := q.Eligibility(ctx, id, 0)
	if err != nil || recovered.RecordedBytes != 41 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	if err := q.StartAccounting(func(ClosureOutcome) {}); err != nil {
		t.Fatal(err)
	}
	if q.ConfigureAccountingFailureDeadline(time.Second) == nil {
		t.Fatal("changed restart-scoped policy after serving")
	}
}

func TestPeriodicPolicyInvalidationFencesLateSettlement(t *testing.T) {
	q, id, pool := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	initial, err := q.Eligibility(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 71); err != nil {
		t.Fatal(err)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	releaseAck := sync.OnceFunc(func() { close(release) })
	defer releaseAck()
	q.afterCommit = func() error { close(committed); <-release; return nil }
	done := make(chan error, 1)
	go func() { done <- q.FlushAccounting(ctx, id) }()
	<-committed
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET credential_generation=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q.ApplyAccountingPolicy(id, 1000, true)
	releaseAck()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("late settlement restored revoked assessment")
	}
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("old generation recovered: %v", err)
	}
	recovered, err := q.Eligibility(ctx, id, 1)
	if err != nil || recovered.EffectiveBytes != 71 {
		t.Fatalf("new generation lost pending usage: %+v %v", recovered, err)
	}
}

func TestPeriodicPolicyCapUsesUncertainBatchExactlyOnce(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 80); err != nil {
		t.Fatal(err)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	releaseAck := sync.OnceFunc(func() { close(release) })
	defer releaseAck()
	q.afterCommit = func() error { close(committed); <-release; return ErrSettlementUncertain }
	done := make(chan error, 1)
	go func() { done <- q.FlushAccounting(ctx, id) }()
	<-committed
	if err := q.Observe(id, 60); err != nil {
		t.Fatal(err)
	}
	// The authoritative read already includes the 80-byte flight. The pending
	// 60-byte successor must remain counted without adding the flight twice.
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if q.ApplyAccountingPolicy(id, 141, false) {
		t.Fatal("uncertain committed batch counted twice")
	}
	if !q.ApplyAccountingPolicy(id, 140, false) {
		t.Fatal("equality did not exhaust recorded plus pending usage")
	}
	releaseAck()
	if err := <-done; err != ErrSettlementUncertain {
		t.Fatalf("ack=%v", err)
	}
	q.afterCommit = nil
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !q.ApplyAccountingPolicy(id, 140, false) {
		t.Fatal("reconciliation lost pending successor")
	}
}
