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
	closed := make(chan ClosureOutcome, 1)
	q.periodic.close = func(out ClosureOutcome) { closed <- out }
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
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.AddDate(0, 0, 7)
	q.clock.mu.Unlock()
	now = now.Add(DefaultAccountingFailureDeadline)
	if _, err := q.Eligibility(ctx, id, 0); err != ErrTemporary {
		t.Fatalf("new week masked old pending=%v", err)
	}
	q.CheckAccountingHealth()
	select {
	case out := <-closed:
		if out.Reason != "accounting_unavailable" {
			t.Fatal(out)
		}
	default:
		t.Fatal("new week failure did not close at monotonic deadline")
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

func TestPeriodicRetiredPendingCannotBlockCurrentWeek(t *testing.T) {
	q, id, _ := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 71); err != nil {
		t.Fatal(err)
	}
	q.afterCommit = func() error { return ErrSettlementUncertain }
	if err := q.FlushAccounting(ctx, id); err != ErrSettlementUncertain {
		t.Fatal(err)
	}
	q.afterCommit = nil
	q.adjustTime = func(now time.Time) time.Time { return now.AddDate(0, 0, 91) }
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.AddDate(0, 0, 91)
	q.clock.mu.Unlock()
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatalf("retired work prevented bounded cleanup: %v", err)
	}
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatalf("retired work hid current allowance: %v", err)
	}
	if err := q.Observe(id, 19); err != nil {
		t.Fatal(err)
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	usage, err := q.RecordedUsage(ctx, id, weekStart(q.now()))
	if err != nil || usage.RecordedBytes != 19 {
		t.Fatalf("retired bytes crossed weeks: %+v %v", usage, err)
	}
}

func TestPeriodicMondayRequiresFreshEligibilityWithoutNewGeneration(t *testing.T) {
	q, id, pool := fixture(t)
	q.SetSettlementProtection(settlementTestProtection{})
	ctx := context.Background()
	monday := weekStart(time.Now()).AddDate(0, 0, 7)
	shift := monday.Add(-time.Second).Sub(time.Now())
	q.adjustTime = func(now time.Time) time.Time { return now.Add(shift) }
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.Add(shift)
	q.clock.mu.Unlock()
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if !q.AccountingReady(id) {
		t.Fatal("old week eligibility unavailable")
	}
	shift += 2 * time.Second
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.Add(2 * time.Second)
	q.clock.mu.Unlock()
	if q.AccountingReady(id) {
		t.Fatal("old week eligibility authorized new week")
	}
	if _, err := q.Eligibility(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if !q.AccountingReady(id) {
		t.Fatal("fresh new week read required reauthentication")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=0 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Eligibility(ctx, id, 0); err != ErrExhausted {
		t.Fatalf("new week policy not authoritative: %v", err)
	}
}
