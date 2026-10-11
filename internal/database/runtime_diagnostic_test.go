package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeBusinessErrorsDoNotChangeDatabaseState(t *testing.T) {
	var got []diagnostic.Event
	r := NewRuntime(nil)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	secret := errors.New("quota exhausted: SELECT private_identity FROM accounts WHERE token='SQL canary'")
	for range 2 {
		if err := r.WithUnit(context.Background(), func(ctx context.Context) error {
			return r.WithUnit(ctx, func(context.Context) error { return secret })
		}); !errors.Is(err, secret) {
			t.Fatalf("business error = %v", err)
		}
	}
	_ = r.WithUnit(context.Background(), func(context.Context) error { return nil })
	if len(got) != 0 {
		t.Fatalf("business errors emitted database transitions = %+v", got)
	}
}

func TestRuntimeCanceledCallerDoesNotChangeDatabaseState(t *testing.T) {
	var got []diagnostic.Event
	r := NewRuntime(nil)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.WithUnit(ctx, func(context.Context) error {
		t.Fatal("canceled work entered")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller = %v", err)
	}
	active, cancelActive := context.WithCancel(context.Background())
	if err := r.WithUnit(active, func(ctx context.Context) error {
		cancelActive()
		return ctx.Err()
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller canceled during work = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("canceled caller emitted database transition = %+v", got)
	}
}

func TestRuntimeSQLFailureAndRecoveryIgnoreInterveningBusinessErrors(t *testing.T) {
	c, _, serving := fixture(t)
	pool, err := NewPool(context.Background(), c, serving, false)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var got []diagnostic.Event
	r := NewRuntime(pool)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	ctx := context.Background()
	if _, err := r.Exec(ctx, "SELECT 1/0"); err == nil {
		t.Fatal("invalid SQL succeeded")
	}
	policy := errors.New("policy denied: SQL canary private token")
	if err := r.Unit(ctx, func(context.Context, *pgxpool.Conn) error { return policy }); !errors.Is(err, policy) {
		t.Fatalf("policy result = %v", err)
	}
	if len(got) != 1 || got[0].Event != "dependency.database" || got[0].Outcome != "failed" || got[0].Reason != "unavailable" {
		t.Fatalf("database failure or policy neutrality = %+v", got)
	}
	if _, err := r.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Event != "dependency.database" || got[1].Outcome != "recovered" || got[1].Reason != "none" {
		t.Fatalf("database recovery = %+v", got)
	}
	for _, e := range got {
		if e.Duration < 0 || e.Duration > 4*time.Second || e.Reason == policy.Error() {
			t.Fatalf("unbounded or private database event = %+v", e)
		}
	}
}

func TestRuntimeAcquisitionFailureRecoversAfterPoolReturns(t *testing.T) {
	c, _, serving := fixture(t)
	closed, err := NewPool(context.Background(), c, serving, false)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	var got []diagnostic.Event
	transitions := diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) })
	unavailable := NewRuntime(closed)
	unavailable.SetTransitions(transitions)
	if err := unavailable.Unit(context.Background(), func(context.Context, *pgxpool.Conn) error {
		t.Fatal("closed pool acquired a connection")
		return nil
	}); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("closed pool result = %v", err)
	}
	if len(got) != 1 || got[0].Outcome != "failed" {
		t.Fatalf("acquisition failure = %+v", got)
	}
	pool, err := NewPool(context.Background(), c, serving, false)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	recovered := NewRuntime(pool)
	recovered.SetTransitions(transitions)
	if err := recovered.Unit(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		var n int
		return conn.QueryRow(ctx, "SELECT 1").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Event != "dependency.database" || got[1].Outcome != "recovered" {
		t.Fatalf("acquisition recovery = %+v", got)
	}
}
