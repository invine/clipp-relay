package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRuntimeAdmissionDoesNotChangeDatabaseState(t *testing.T) {
	var got []diagnostic.Event
	r := NewRuntime(nil)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	ctx, release, err := r.StartUnit(context.Background())
	if err != nil || ctx.Err() != nil {
		t.Fatalf("admission = context %v, err %v", ctx, err)
	}
	release()
	ctx, release, err = r.StartUnit(context.Background())
	if err != nil || ctx.Err() != nil {
		t.Fatalf("admission after release = context %v, err %v", ctx, err)
	}
	release()
	if len(got) != 0 {
		t.Fatalf("admission without SQL emitted database transitions = %+v", got)
	}
}

func TestRuntimeCanceledCallerDoesNotChangeDatabaseState(t *testing.T) {
	var got []diagnostic.Event
	r := NewRuntime(nil)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.StartUnit(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller = %v", err)
	}
	active, cancelActive := context.WithCancel(context.Background())
	unit, release, err := r.StartUnit(active)
	if err != nil {
		t.Fatalf("active caller admission = %v", err)
	}
	cancelActive()
	if !errors.Is(unit.Err(), context.Canceled) {
		t.Fatalf("caller cancellation was not retained by the operation: %v", unit.Err())
	}
	release()
	if len(got) != 0 {
		t.Fatalf("canceled caller emitted database transition = %+v", got)
	}
}

func TestRuntimeSQLFailureAndNoRowsRecovery(t *testing.T) {
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
	} else {
		var sqlErr *pgconn.PgError
		if !errors.As(err, &sqlErr) || sqlErr.Code != "22012" {
			t.Fatalf("work error was not preserved: %T %v", err, err)
		}
	}
	_, release, err := r.StartUnit(ctx)
	if err != nil {
		t.Fatalf("admission after SQL failure = %v", err)
	}
	release()
	if len(got) != 1 || got[0].Event != "dependency.database" || got[0].Outcome != "failed" || got[0].Reason != "unavailable" {
		t.Fatalf("database failure or admission neutrality = %+v", got)
	}
	var value int
	if err := r.QueryRow(ctx, "SELECT 1 WHERE false").Scan(&value); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("no-row query = %v", err)
	}
	if len(got) != 2 || got[1].Event != "dependency.database" || got[1].Outcome != "recovered" || got[1].Reason != "none" {
		t.Fatalf("no-row database recovery = %+v", got)
	}
	for _, e := range got {
		if e.Duration < 0 || e.Duration > 4*time.Second {
			t.Fatalf("unbounded database event = %+v", e)
		}
	}
	if _, err := r.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("steady success emitted an extra transition: %+v", got)
	}
}

func TestRuntimeExecHonorsEarlierCallerDeadline(t *testing.T) {
	c, _, serving := fixture(t)
	pool, err := NewPool(context.Background(), c, serving, false)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	runtime := NewRuntime(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := runtime.Exec(ctx, "SELECT pg_sleep(0.4)"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("database work did not report the caller deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("database work ignored the caller deadline: %s", elapsed)
	}
	if _, err := runtime.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("runtime did not recover after the canceled operation: %v", err)
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
	if _, err := unavailable.Exec(context.Background(), "SELECT 1"); !errors.Is(err, ErrPoolUnavailable) {
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
	if _, err := recovered.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Event != "dependency.database" || got[1].Outcome != "recovered" {
		t.Fatalf("acquisition recovery = %+v", got)
	}
}
