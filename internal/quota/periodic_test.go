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
	if _, err := pool.Exec(context.Background(), `UPDATE public.accounts SET weekly_bytes_override=100 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Eligibility(context.Background(), id); err != nil {
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
	if _, err := q.Eligibility(context.Background(), id); err != ErrExhausted {
		t.Fatalf("eligibility=%v", err)
	}
}

func TestPeriodicObservationsSurviveLostAcknowledgmentWithoutDoubleCounting(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	current, err := q.Eligibility(ctx, id)
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
	current, err = q.Eligibility(ctx, id)
	if err != nil || current.RecordedBytes != 80 || current.EffectiveBytes != 140 {
		t.Fatalf("after retry=%+v %v", current, err)
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	current, err = q.Eligibility(ctx, id)
	if err != nil || current.RecordedBytes != 140 || current.EffectiveBytes != 140 {
		t.Fatalf("final=%+v %v", current, err)
	}
}

func TestPeriodicCutoffIsIndependentOfBlockedPersistence(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	now := time.Now()
	q.periodic.monotonicNow = func() time.Time { return now }
	closed := make(chan ClosureOutcome, 1)
	q.periodic.close = func(out ClosureOutcome) { closed <- out }
	initial, err := q.Eligibility(ctx, id)
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
	if _, err := q.Eligibility(ctx, id); err != ErrTemporary {
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
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatalf("recovery=%v", err)
	}
}

func TestPeriodicConcurrentObservationsRemainAccountScopedAfterRevocation(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	q.ApplyAccountingPolicy(id, 1<<30)
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	usage, err := q.RecordedUsage(ctx, id, weekStart(time.Now()))
	if err != nil || usage.RecordedBytes != 8400 {
		t.Fatalf("revoked observed usage=%+v %v", usage, err)
	}
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatalf("account access revocation affected quota assessment: %v", err)
	}
	if !q.AccountingReady(id) {
		t.Fatal("account access revocation marked accounting unhealthy")
	}
}

func TestPeriodicPriorWeekPendingCannotBeHiddenByNewWeekRead(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	now := time.Now()
	q.periodic.monotonicNow = func() time.Time { return now }
	closed := make(chan ClosureOutcome, 1)
	q.periodic.close = func(out ClosureOutcome) { closed <- out }
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	if _, err := q.Eligibility(ctx, id); err != ErrTemporary {
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
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	ctx := context.Background()
	monday := weekStart(time.Now()).AddDate(0, 0, 7)
	shift := monday.Add(-time.Second).Sub(time.Now())
	q.adjustTime = func(now time.Time) time.Time { return now.Add(shift) }
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.Add(shift)
	q.clock.mu.Unlock()
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !q.AccountingReady(id) {
		t.Fatal("fresh new week read required reauthentication")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=0 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Eligibility(ctx, id); err != ErrExhausted {
		t.Fatalf("new week policy not authoritative: %v", err)
	}
}

func TestPeriodicConfiguredDeadlineAndIdleRefresh(t *testing.T) {
	q, id, _ := fixture(t)
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
	initial, err := q.Eligibility(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	if _, err := q.Eligibility(ctx, id); err != ErrTemporary {
		t.Fatalf("read masked pending write at deadline: %v", err)
	}
	q.CheckAccountingHealth()
	if q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("deadline retained admission authority")
	}
	if err := q.FlushAccounting(ctx, id); err != nil {
		t.Fatal(err)
	}
	recovered, err := q.Eligibility(ctx, id)
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
	ctx := context.Background()
	initial, err := q.Eligibility(ctx, id)
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
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=1000 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q.ApplyAccountingPolicy(id, 1000)
	releaseAck()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if q.AccountingCurrent(id, initial.Revision) {
		t.Fatal("late settlement restored a stale policy assessment")
	}
	recovered, err := q.Eligibility(ctx, id)
	if err != nil || recovered.EffectiveBytes != 71 {
		t.Fatalf("new generation lost pending usage: %+v %v", recovered, err)
	}
}

func TestPeriodicPolicyCapUsesUncertainBatchExactlyOnce(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
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
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatal(err)
	}
	if q.ApplyAccountingPolicy(id, 141) {
		t.Fatal("uncertain committed batch counted twice")
	}
	if !q.ApplyAccountingPolicy(id, 140) {
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
	if !q.ApplyAccountingPolicy(id, 140) {
		t.Fatal("reconciliation lost pending successor")
	}
}

func TestFinalFlushSettlesRevokedAccountAndWaitsForActiveBatch(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatal(err)
	}
	q.Observe(id, 80)
	entered, release := make(chan struct{}), make(chan struct{})
	q.SetSettlementHooksForTest(nil, func(context.Context) error { close(entered); <-release; return ErrSettlementUncertain })
	done := make(chan error, 1)
	go func() { done <- q.FlushAccounting(ctx, id) }()
	<-entered
	q.Observe(id, 60)
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	final := make(chan error, 1)
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	go func() { final <- q.FlushAllAccounting(bounded) }()
	q.SetSettlementHooksForTest(nil, nil)
	close(release)
	<-done
	q.SetSettlementHooksForTest(nil, nil)
	if err := <-final; err != nil {
		t.Fatal(err)
	}
	got, err := q.RecordedUsage(ctx, id, weekStart(time.Now()))
	if err != nil || got.RecordedBytes != 140 {
		t.Fatalf("final=%+v %v", got, err)
	}
}

func TestQuotaCloseIsBoundedWithUnresponsiveAccounting(t *testing.T) {
	_, id, pool := fixture(t)
	q := New(pool)
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, id); err != nil {
		q.Close()
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	q.SetSettlementHooksForTest(nil, func(context.Context) error {
		once.Do(func() { close(entered) })
		<-release
		return ErrSettlementUncertain
	})
	if err := q.StartAccounting(func(ClosureOutcome) {}); err != nil {
		q.Close()
		t.Fatal(err)
	}
	if err := q.Observe(id, 123); err != nil {
		q.Close()
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		q.Close()
		t.Fatal("periodic write did not reach boundary")
	}
	started := time.Now()
	q.Close()
	if time.Since(started) > 300*time.Millisecond {
		close(release)
		t.Fatal("unresponsive accounting blocked close")
	}
	close(release)
	select {
	case <-q.periodic.done:
	case <-time.After(time.Second):
		t.Fatal("released worker did not exit")
	}
}

func TestSuccessfulSettlementCannotConcealUnsafeClock(t *testing.T) {
	for _, kind := range []string{"unsafe", "stale", "regression"} {
		t.Run(kind, func(t *testing.T) {
			q, id, _ := fixture(t)
			ctx := context.Background()
			closed := make(chan ClosureOutcome, 1)
			q.periodic.close = func(out ClosureOutcome) { closed <- out }
			first, err := q.Eligibility(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = q.Observe(id, 12345); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unsafe":
				q.clock.mu.Lock()
				q.clock.safe = false
				q.clock.mu.Unlock()
			case "stale":
				q.clock.mu.Lock()
				q.clock.sampledMono = q.clock.sampledMono.Add(-6 * time.Minute)
				q.clock.mu.Unlock()
			case "regression":
				q.adjustTime = func(v time.Time) time.Time { return v.Add(-6 * time.Second) }
			}
			if err = q.FlushAccounting(ctx, id); err != nil {
				t.Fatalf("already observed bytes must settle: %v", err)
			}
			q.CheckAccountingHealth()
			select {
			case out := <-closed:
				if out.Reason != "clock_unavailable" || !q.ClosureCurrent(out) {
					t.Fatalf("closure %+v", out)
				}
			default:
				t.Fatal("successful settlement concealed unsafe clock")
			}
			if q.AccountingCurrent(id, first.Revision) {
				t.Fatal("old admission retained authority")
			}
			if err = q.Observe(id, 1); err != nil {
				t.Fatal(err)
			}
			if err = q.FlushAccounting(ctx, id); err != nil {
				t.Fatal(err)
			}
			if q.AccountingCurrent(id, first.Revision+1) {
				t.Fatal("later successful write revived clock-blocked authority")
			}

			if _, err = q.Eligibility(ctx, id); err != ErrTemporary {
				t.Fatalf("unsafe readmission %v", err)
			}
		})
	}
}

func TestSmallBackwardStepAcrossMondayCannotRestoreOldWeekAuthority(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	boundary := weekStart(time.Now()).AddDate(0, 0, 7)
	shift := boundary.Add(time.Second).Sub(time.Now())
	q.adjustTime = func(v time.Time) time.Time { return v.Add(shift) }
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.Add(shift)
	q.clock.mu.Unlock()
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatal(err)
	}
	closed := make(chan ClosureOutcome, 1)
	q.periodic.close = func(out ClosureOutcome) { closed <- out }
	shift -= 2 * time.Second
	if safe, _, _ := q.ClockHealth(); !safe {
		t.Fatal("test must remain within ordinary skew threshold")
	}
	if q.AccountingReady(id) {
		t.Fatal("Monday permission authorized Sunday")
	}
	if _, err := q.Eligibility(ctx, id); err != ErrTemporary {
		t.Fatalf("regression reauthorized past week: %v", err)
	}
	q.CheckAccountingHealth()
	select {
	case out := <-closed:
		if out.Reason != "clock_unavailable" {
			t.Fatalf("closure %+v", out)
		}
	default:
		t.Fatal("already admitted circuit survived week regression")
	}
}
