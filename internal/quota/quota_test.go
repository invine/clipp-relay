package quota

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type probeOverride struct {
	persistence.Accounting
	probe func(context.Context) (time.Time, error)
}

func (p probeOverride) ProbeDatabaseTime(ctx context.Context) (time.Time, error) { return p.probe(ctx) }

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
		for _, query := range []string{
			`DELETE FROM public.weekly_relay_usage WHERE account_id=$1`,

			`DELETE FROM public.accounts WHERE id=$1`,
		} {
			_, _ = p.Exec(context.Background(), query, id)
		}
	})
	q := New(postgres.New(database.NewRuntime(p)))
	t.Cleanup(q.Close)
	return q, id, p
}

func TestClockWarningIntervalIsVisibleWithoutClosingEligibility(t *testing.T) {
	q, id, pool := fixture(t)
	q.accounting = probeOverride{Accounting: q.accounting, probe: func(ctx context.Context) (time.Time, error) {
		var now time.Time
		err := pool.QueryRow(ctx, `SELECT clock_timestamp()+interval '2 seconds'`).Scan(&now)
		return now, err
	}}
	if err := q.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if safe, warning, _ := q.ClockHealth(); !safe || !warning {
		t.Fatalf("clock status safe=%v warning=%v", safe, warning)
	}
	if _, err := q.Eligibility(context.Background(), id); err != nil {
		t.Fatalf("warning alone closed eligibility: %v", err)
	}
}

func TestClockUncertaintyIntervalBoundaries(t *testing.T) {
	q := &Quota{}
	// The sampled database time is near each limit. A deterministic 400ms
	// request/response interval places the midpoint inside the limit while
	// its uncertainty interval crosses it.
	probeAtOffset := func(offset time.Duration) {
		t.Helper()
		before := time.Now()
		calls := 0
		q.adjustTime = func(t time.Time) time.Time {
			calls++
			switch calls {
			case 1:
				return before
			case 2:
				return before.Add(400 * time.Millisecond)
			default:
				return t
			}
		}
		q.accounting = probeOverride{probe: func(context.Context) (time.Time, error) { return before.Add(200*time.Millisecond + offset), nil }}
	}
	probeAtOffset(900 * time.Millisecond)
	if err := q.Probe(context.Background()); err != nil {
		t.Fatalf("warning-only interval closed eligibility: %v", err)
	}
	if safe, warning, _ := q.ClockHealth(); !safe || !warning {
		t.Fatalf("interval crossing 1s: safe=%v warning=%v", safe, warning)
	}
	probeAtOffset(4900 * time.Millisecond)
	if err := q.Probe(context.Background()); !errors.Is(err, ErrTemporary) {
		t.Fatalf("interval crossing 5s remained open: %v", err)
	}
	if safe, _, _ := q.ClockHealth(); safe {
		t.Fatal("interval crossing 5s remained healthy")
	}
}

func TestAgedClockProbeHasBoundedPoolAdmission(t *testing.T) {
	q, id, pool := fixture(t)
	ctx := context.Background()
	first, err := q.Eligibility(ctx, id)
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
	go func() { _, e := q.Eligibility(context.Background(), id); result <- e }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrTemporary) {
			t.Fatalf("aged clock spent or misclassified eligibility: %v", err)
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
	last, err := q.Eligibility(ctx, id)
	if err != nil || last.EffectiveBytes != first.EffectiveBytes {
		t.Fatalf("eligibility changed despite unavailable probe: %+v %v", last, err)
	}
}
