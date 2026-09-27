package quota

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fixture(t *testing.T) (*Quota, string, *pgxpool.Pool) {
	t.Helper()
	path := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
	if path == "" {
		t.Skip("disposable PostgreSQL fixture not configured")
	}
	c, err := config.Load(path)
	if err != nil || c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") {
		t.Fatalf("unsafe fixture: %v", err)
	}
	m, err := c.ReadMaterial()
	if err != nil {
		t.Fatal(err)
	}
	p, err := database.NewPool(context.Background(), c, m, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	// A random account avoids coupling the test to the portal's login flow.
	id, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'quota@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, id, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), `DELETE FROM public.weekly_quota_usage WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, id)
	})
	q := New(p)
	t.Cleanup(q.Close)
	return q, id, p
}

func TestRestartDoesNotRestoreOrRefundFundedCredit(t *testing.T) {
	q, id, pool := fixture(t)
	first, err := q.Take(context.Background(), id, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(pool)
	t.Cleanup(restarted.Close)
	second, err := restarted.Take(context.Background(), id, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if first.Committed != 65536 || second.Committed != 131072 || second.Usable != 64512 {
		t.Fatalf("refunded or restored credit: first=%+v second=%+v", first, second)
	}
}

func TestConfirmedCreditIsSharedAndCommittedOnFunding(t *testing.T) {
	q, id, _ := fixture(t)
	ctx := context.Background()
	first, err := q.Take(ctx, id, 0, 10*1024)
	if err != nil {
		t.Fatal(err)
	}
	if first.Committed != 65536 || first.Usable != 55296 {
		t.Fatalf("first funding = %+v", first)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := q.Take(ctx, id, 0, 1024)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	last, err := q.Take(ctx, id, 0, 0)
	if err != nil || last.Committed != 65536 || last.Usable != 53248 {
		t.Fatalf("shared credit = %+v, %v", last, err)
	}
}

func TestEnsureFundsWithoutInventingTraffic(t *testing.T) {
	q, id, _ := fixture(t)
	ready, err := q.Ensure(context.Background(), id, 0)
	if err != nil || ready.Committed != BlockBytes || ready.Usable != BlockBytes {
		t.Fatalf("funding without traffic = %+v, %v", ready, err)
	}
	charged, err := q.Take(context.Background(), id, 0, 17)
	if err != nil || charged.Committed != BlockBytes || charged.Usable != BlockBytes-17 {
		t.Fatalf("real endpoint charge = %+v, %v", charged, err)
	}
}

func TestLostCommitReplyReconcilesSameReceipt(t *testing.T) {
	q, id, _ := fixture(t)
	q.afterCommit = func() error { q.afterCommit = nil; return errors.New("lost reply") }
	if _, err := q.Take(context.Background(), id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("unconfirmed credit: %v", err)
	}
	q.beforeCommit = func(context.Context, pgx.Tx) error { return errors.New("reconciliation unavailable") }
	if _, err := q.Take(context.Background(), id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("uncertain operation allowed later funding: %v", err)
	}
	q.beforeCommit = nil
	got, err := q.Take(context.Background(), id, 0, 1024)
	if err != nil || got.Committed != 65536 || got.Usable != 64512 {
		t.Fatalf("replay duplicated or lost receipt: %+v %v", got, err)
	}
	if _, err := q.Take(context.Background(), id, 0, 64512); err != nil {
		t.Fatal(err)
	}
	next, err := q.Take(context.Background(), id, 0, 1)
	if err != nil || next.Committed != 131072 {
		t.Fatalf("next allocation after reconciliation: %+v %v", next, err)
	}
}

func TestKnownRollbackRetriesWithoutDoubleDebit(t *testing.T) {
	q, id, _ := fixture(t)
	q.beforeCommit = func(context.Context, pgx.Tx) error { q.beforeCommit = nil; return errors.New("rollback") }
	if _, err := q.Take(context.Background(), id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("rolled-back credit: %v", err)
	}
	got, err := q.Take(context.Background(), id, 0, 1024)
	if err != nil || got.Committed != 65536 || got.Usable != 64512 {
		t.Fatalf("rollback replay: %+v %v", got, err)
	}
}

func TestSkewAndUncertaintyCloseConfirmedCredit(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	if _, err := q.Take(ctx, id, 0, 1024); err != nil {
		t.Fatal(err)
	}
	q.probeQuery = func(ctx context.Context) (time.Time, error) {
		var now time.Time
		err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '6 seconds'`).Scan(&now)
		return now, err
	}
	if err := q.Probe(ctx); !errors.Is(err, ErrTemporary) {
		t.Fatalf("skew accepted: %v", err)
	}
	if _, err := q.Take(ctx, id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("skew spent confirmed credit: %v", err)
	}
	q.probeQuery = func(ctx context.Context) (time.Time, error) {
		var now time.Time
		err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
		time.Sleep(2100 * time.Millisecond)
		return now, err
	}
	if err := q.Probe(ctx); !errors.Is(err, ErrTemporary) {
		t.Fatalf("uncertain interval accepted: %v", err)
	}
	if _, err := q.Take(ctx, id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("uncertain interval spent credit: %v", err)
	}
	q.probeQuery = nil
	for sample := 0; sample < 3; sample++ {
		q.clock.mu.Lock()
		q.clock.sampled = q.clock.sampled.Add(-time.Minute)
		q.clock.sampledMono = q.clock.sampledMono.Add(-time.Minute)
		q.clock.mu.Unlock()
		err := q.Probe(ctx)
		if sample < 2 && !errors.Is(err, ErrTemporary) || sample == 2 && err != nil {
			t.Fatalf("recovery sample %d: %v", sample+1, err)
		}
	}
	if _, err := q.Take(ctx, id, 0, 1024); err != nil {
		t.Fatalf("healthy cadence did not recover credit: %v", err)
	}
}

func TestLockWaitCrossingMondayUsesAfterLockWeek(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	boundary := weekStart(time.Now()).AddDate(0, 0, 7)
	var shift atomic.Int64
	shift.Store(int64(boundary.Add(-4 * time.Second).Sub(time.Now())))
	q.adjustTime = func(t time.Time) time.Time { return t.Add(time.Duration(shift.Load())) }
	q.clock.mu.Lock()
	q.clock.sampled = time.Time{}
	q.clock.sampledMono = time.Time{}
	q.clock.safe = false
	q.clock.mu.Unlock()
	if err := q.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM public.accounts WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	q.beforeLock = func() { entered <- struct{}{} }
	result := make(chan error, 1)
	go func() { _, e := q.Take(ctx, id, 0, 1024); result <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("funding did not reach account lock")
	}
	shift.Store(int64(boundary.Add(4 * time.Second).Sub(time.Now())))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrTemporary) {
		t.Fatalf("boundary credit installed: %v", err)
	}
	var fundedWeek time.Time
	if err := pool.QueryRow(ctx, `SELECT week_start FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&fundedWeek); err != nil {
		t.Fatal(err)
	}
	if !fundedWeek.Equal(boundary) {
		t.Fatalf("used pre-lock week %s; want %s", fundedWeek, boundary)
	}
}

func TestReducedAllowancePreservesCommittedUsageButStopsCredit(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	if _, err := q.Take(ctx, id, 0, 1024); err != nil {
		t.Fatal(err)
	}
	plan, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions) VALUES($1,'Reduced quota',1024,5)`, plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE public.accounts SET plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM public.quota_plans WHERE id=$1`, plan)
	})
	if _, err = pool.Exec(ctx, `UPDATE public.accounts SET plan_id=$2 WHERE id=$1`, id, plan); err != nil {
		t.Fatal(err)
	}
	q.Invalidate(id)
	if _, err = q.Take(ctx, id, 0, 1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("reduced cap permitted new credit: %v", err)
	}
	var committed int64
	if err = pool.QueryRow(ctx, `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != 65536 {
		t.Fatalf("durable committed amount changed: %d %v", committed, err)
	}
}

func TestDatabaseOutageOnlyAllowsConfirmedLocalCredit(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	if _, err := q.Take(ctx, id, 0, 1024); err != nil {
		t.Fatal(err)
	}
	offline, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	q.pool = offline
	offline.Close()
	if got, err := q.Take(ctx, id, 0, 1024); err != nil || got.Usable != 63488 {
		t.Fatalf("confirmed credit lost during outage: %+v %v", got, err)
	}
	if _, err := q.Take(ctx, id, 0, 63488); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("unconfirmed credit spent during outage: %v", err)
	}
}

func TestInvalidatedWorkerCannotInstallLateReceipt(t *testing.T) {
	q, id, pool := fixture(t)
	b := q.acquire(id)
	defer q.release(id, b)
	start := b.epoch.Load()
	finished := make(chan struct{})
	q.afterCommit = func() error {
		go func() { q.Invalidate(id); close(finished) }()
		deadline := time.After(time.Second)
		for b.epoch.Load() == start {
			select {
			case <-deadline:
				return errors.New("invalidation did not start")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		return nil
	}
	if _, err := q.Take(context.Background(), id, 0, 1024); !errors.Is(err, ErrTemporary) {
		t.Fatalf("late receipt installed: %v", err)
	}
	<-finished
	var committed int64
	if err := pool.QueryRow(context.Background(), `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != 65536 {
		t.Fatalf("committed receipt missing: %d %v", committed, err)
	}
}

func TestReorderedOldOperationCannotBecomeNewDebit(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	q.afterCommit = func() error { q.afterCommit = nil; return errors.New("lost reply") }
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("missing ambiguity: %v", err)
	}
	other := New(pool)
	t.Cleanup(other.Close)
	if _, err := other.Take(ctx, id, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("stale operation accepted: %v", err)
	}
	var committed int64
	if err := pool.QueryRow(ctx, `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != 131072 {
		t.Fatalf("old operation debited again: %d %v", committed, err)
	}
}

func TestClockWarningIntervalIsVisibleWithoutClosingCredit(t *testing.T) {
	q, id, pool := fixture(t)
	q.probeQuery = func(ctx context.Context) (time.Time, error) {
		var now time.Time
		err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '2 seconds'`).Scan(&now)
		return now, err
	}
	if err := q.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if safe, warning, _ := q.ClockHealth(); !safe || !warning {
		t.Fatalf("clock status safe=%v warning=%v", safe, warning)
	}
	if _, err := q.Take(context.Background(), id, 0, 1); err != nil {
		t.Fatalf("warning alone closed credit: %v", err)
	}
}

func TestDelayedCommitReplyCannotExtendMondayCredit(t *testing.T) {
	q, id, pool := fixture(t)
	boundary := weekStart(time.Now()).AddDate(0, 0, 7)
	var shift atomic.Int64
	shift.Store(int64(boundary.Add(-7 * time.Second).Sub(time.Now())))
	q.adjustTime = func(t time.Time) time.Time { return t.Add(time.Duration(shift.Load())) }
	q.clock.mu.Lock()
	q.clock.sampled = time.Time{}
	q.clock.sampledMono = time.Time{}
	q.clock.safe = false
	q.clock.mu.Unlock()
	if err := q.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.afterCommit = func() error { time.Sleep(1200 * time.Millisecond); return nil }
	if _, err := q.Take(context.Background(), id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("old-week credit installed after deadline: %v", err)
	}
	var committed int64
	if err := pool.QueryRow(context.Background(), `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != 65536 {
		t.Fatalf("durable debit missing: %d %v", committed, err)
	}
}

func TestCancelledCallerCannotInstallConfirmedCredit(t *testing.T) {
	q, id, pool := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.beforeInstall = cancel
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("cancelled caller installed credit: %v", err)
	}
	got, err := q.Take(context.Background(), id, 0, 0)
	if err != nil || got.Usable != 0 {
		t.Fatalf("cancelled credit remained usable: %+v %v", got, err)
	}
	var committed int64
	if err := pool.QueryRow(context.Background(), `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != 65536 {
		t.Fatalf("committed receipt missing: %d %v", committed, err)
	}
}

func TestPoolSaturationBoundsQuotaAdmission(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	var borrowed []*pgxpool.Conn
	for range 8 {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		borrowed = append(borrowed, conn)
	}
	defer func() {
		for _, conn := range borrowed {
			conn.Release()
		}
	}()
	limit, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := q.Take(limit, id, 0, 1)
	if !errors.Is(err, ErrTemporary) || time.Since(started) > time.Second {
		t.Fatalf("pool admission was unbounded: %v after %s", err, time.Since(started))
	}
}

func TestAgedClockProbeHasBoundedPoolAdmission(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	first, err := q.Take(ctx, id, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	q.clock.mu.Lock()
	q.clock.sampled = q.clock.sampled.Add(-6 * time.Minute)
	q.clock.sampledMono = q.clock.sampledMono.Add(-6 * time.Minute)
	q.clock.mu.Unlock()
	var borrowed []*pgxpool.Conn
	for range 8 {
		conn, e := pool.Acquire(ctx)
		if e != nil {
			t.Fatal(e)
		}
		borrowed = append(borrowed, conn)
	}
	release := func() {
		for _, conn := range borrowed {
			conn.Release()
		}
	}
	defer release()
	result := make(chan error, 1)
	go func() { _, e := q.Take(context.Background(), id, 0, 1024); result <- e }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrTemporary) {
			t.Fatalf("aged clock spent or misclassified credit: %v", err)
		}
	case <-time.After(900 * time.Millisecond):
		release()
		borrowed = nil
		<-result
		t.Fatal("aged clock probe waited on the pool without a bound")
	}
	release()
	borrowed = nil
	q.clock.mu.Lock()
	q.clock.sampled = q.now()
	q.clock.sampledMono = time.Now()
	q.clock.safe = true
	q.clock.mu.Unlock()
	last, err := q.Take(ctx, id, 0, 0)
	if err != nil || last.Usable != first.Usable {
		t.Fatalf("credit changed despite unavailable probe: %+v %v", last, err)
	}
}

func TestCancelledWaiterCannotUseLocalCredit(t *testing.T) {
	q, id, _ := fixture(t)
	first, err := q.Take(context.Background(), id, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	b := q.acquire(id)
	defer q.release(id, b)
	b.mu.Lock()
	locked := true
	defer func() {
		if locked {
			b.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, e := q.Take(ctx, id, 0, 1024); result <- e }()
	started := time.Now()
	for {
		q.mu.Lock()
		refs := b.refs
		q.mu.Unlock()
		if refs >= 2 {
			break
		}
		if time.Since(started) > time.Second {
			t.Fatal("waiter did not reach account guard")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	b.mu.Unlock()
	locked = false
	if err := <-result; !errors.Is(err, ErrTemporary) {
		t.Fatalf("cancelled waiter spent local credit: %v", err)
	}
	last, err := q.Take(context.Background(), id, 0, 0)
	if err != nil || last.Usable != first.Usable {
		t.Fatalf("local credit changed: %+v %v", last, err)
	}
}
