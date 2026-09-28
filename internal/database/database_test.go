package database

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CLIPP_TEST_MIGRATION_CONFIG and CLIPP_TEST_SERVING_CONFIG must point at the
// same explicitly disposable, local database named clipp_ticket02_*. The
// migration identity must own CREATE and the serving identity must be read-only.
func fixture(t *testing.T) (config.Config, config.Material, config.Material) {
	t.Helper()
	migrationPath := os.Getenv("CLIPP_TEST_MIGRATION_CONFIG")
	servingPath := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
	if migrationPath == "" || servingPath == "" {
		t.Skip("real PostgreSQL fixture not configured")
	}
	c, e := config.Load(migrationPath)
	if e != nil {
		t.Fatal(e)
	}
	s, e := config.Load(servingPath)
	if e != nil {
		t.Fatal(e)
	}
	if c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") || c.Database.Name != s.Database.Name || c.Database.Port != s.Database.Port {
		t.Fatal("fixture must be isolated local clipp_ticket02_* database")
	}
	mm, e := c.ReadMaterial()
	if e != nil {
		t.Fatal(e)
	}
	sm, e := s.ReadMaterial()
	if e != nil {
		t.Fatal(e)
	}
	return c, mm, sm
}

// The serving runtime owns one bounded work budget even when independent
// account, quota and cleanup callers enter it at the same time.
func TestRuntimeWorkAdmissionIsSharedAndRejectsOverflow(t *testing.T) {
	runtime := NewRuntime(nil)
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := runtime.WithUnit(context.Background(), func(ctx context.Context) error {
				entered <- struct{}{}
				<-release
				return ctx.Err()
			}); err != nil && err != context.DeadlineExceeded {
				t.Errorf("admitted work: %v", err)
			}
		}()
	}
	for range 64 {
		<-entered
	}
	start := time.Now()
	if err := runtime.WithUnit(context.Background(), func(context.Context) error {
		t.Fatal("overflow work entered")
		return nil
	}); err == nil || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("overflow admission was not promptly rejected: %v", err)
	}
	close(release)
	workers.Wait()
}

func TestRuntimeRowRowsAndTransactionRetainWorkUntilCompletion(t *testing.T) {
	c, _, sm := fixture(t)
	p, err := NewPool(context.Background(), c, sm, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	runtime := NewRuntime(p)
	entered := make(chan struct{}, 63)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for range 63 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_ = runtime.WithUnit(context.Background(), func(context.Context) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}
	defer func() { close(release); workers.Wait() }()
	for range 63 {
		<-entered
	}
	ctx := context.Background()
	assertFull := func() {
		var n int
		if err := runtime.QueryRow(ctx, "SELECT 1").Scan(&n); !errors.Is(err, ErrWorkUnavailable) {
			t.Fatalf("work admitted while a result or transaction owned the last slot: %v", err)
		}
	}
	tx, err := runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertFull()
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := runtime.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	assertFull()
	rows.Close()
	row := runtime.QueryRow(ctx, "SELECT 1")
	assertFull()
	var n int
	if err := row.Scan(&n); err != nil || n != 1 {
		t.Fatalf("row did not complete: %d %v", n, err)
	}
	if err := runtime.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("released work did not recover: %d %v", n, err)
	}
}

func TestRuntimeTransactionHonorsStatementAndUnitContexts(t *testing.T) {
	c, _, sm := fixture(t)
	p, err := NewPool(context.Background(), c, sm, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	runtime := NewRuntime(p)
	for _, operation := range []string{"exec", "query", "row"} {
		t.Run(operation, func(t *testing.T) {
			tx, err := runtime.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			short, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			defer cancel()
			start := time.Now()
			switch operation {
			case "exec":
				_, err = tx.Exec(short, "SELECT pg_sleep(0.4)")
			case "query":
				var rows pgx.Rows
				rows, err = tx.Query(short, "SELECT 1 FROM pg_sleep(0.4)")
				if err == nil {
					rows.Next()
					err = rows.Err()
					rows.Close()
				}
			case "row":
				var n int
				err = tx.QueryRow(short, "SELECT 1 FROM pg_sleep(0.4)").Scan(&n)
			}
			if err == nil || time.Since(start) > 300*time.Millisecond {
				t.Fatalf("short statement deadline ignored: %v after %s", err, time.Since(start))
			}
		})
	}
	tx, err := runtime.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commitCtx, cancelCommit := context.WithCancel(context.Background())
	cancelCommit()
	if err := tx.Commit(commitCtx); err == nil {
		t.Fatal("commit ignored caller cancellation")
	}
	parent, cancelParent := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancelParent()
	tx, err = runtime.Begin(parent)
	if err != nil {
		t.Fatal(err)
	}
	<-parent.Done()
	start := time.Now()
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT 1").Scan(&n); err == nil || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("unit deadline ignored by later statement: %v after %s", err, time.Since(start))
	}
	_ = tx.Rollback(context.Background())
}

func TestTLSWrongHostnameAndUntrustedCAFailClosed(t *testing.T) {
	c, _, sm := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, kind := range []string{"hostname", "ca"} {
		pc, e := poolConfig(c, sm, false)
		if e != nil {
			t.Fatal(e)
		}
		if len(pc.ConnConfig.Fallbacks) != 0 || pc.ConnConfig.TLSConfig == nil {
			t.Fatal("TLS fallback configured")
		}
		if kind == "hostname" {
			pc.ConnConfig.TLSConfig.ServerName = "wrong.example"
		} else {
			pc.ConnConfig.TLSConfig.RootCAs = x509.NewCertPool()
		}
		p, e := pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal(e)
		}
		err := p.Ping(ctx)
		p.Close()
		if err == nil {
			t.Fatalf("%s mismatch connected", kind)
		}
	}
}

func TestMigrationAndServingAgainstRealPostgres(t *testing.T) {
	c, mm, sm := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := NewPool(ctx, c, mm, true)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	// The fixture is intentionally disposable. Reset only this owned test table.
	if _, e = p.Exec(ctx, "DROP TABLE IF EXISTS public.cleanup_breach_record, public.deletion_operations, public.deletion_capacity, public.retained_quota_usage, public.weekly_quota_usage, public.relay_access_tokens, public.refresh_generations, public.login_grants, public.authorization_codes, public.portal_sessions, public.authorization_transactions, public.audit_events, public.accounts, public.quota_plans, public.schema_migrations CASCADE; DROP FUNCTION IF EXISTS public.enforce_assigned_plan_allowance() CASCADE"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewPool(ctx, c, mm, true)
			if err != nil {
				errs <- err
				return
			}
			defer other.Close()
			errs <- Migrate(ctx, other)
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent runner: %v", e)
		}
	}
	if e = Migrate(ctx, p); e != nil {
		t.Fatalf("idempotent migration: %v", e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at) VALUES('11111111-1111-4111-8111-111111111111',NULL,'missing-issuer','issuer@example.test',true,clock_timestamp(),clock_timestamp())`); e == nil {
		t.Fatal("live account accepted NULL issuer")
	}
	if _, e = p.Exec(ctx, "GRANT SELECT ON public.schema_migrations TO "+(pgx.Identifier{sm.DBUsername}).Sanitize()); e != nil {
		t.Fatal(e)
	}
	sp, e := NewPool(ctx, c, sm, false)
	if e != nil {
		t.Fatal(e)
	}
	defer sp.Close()
	if e = VerifyServing(ctx, sp); e != nil {
		t.Fatalf("serving schema/role verification: %v", e)
	}
	if sp.Stat().MaxConns() != 8 {
		t.Fatalf("runtime pool max = %d", sp.Stat().MaxConns())
	}
	var statement, lock, idle string
	if e = sp.QueryRow(ctx, "SELECT current_setting('statement_timeout'),current_setting('lock_timeout'),current_setting('idle_in_transaction_session_timeout')").Scan(&statement, &lock, &idle); e != nil || statement != "2s" || lock != "250ms" || idle != "5s" {
		t.Fatalf("runtime SQL budgets = %q %q %q: %v", statement, lock, idle, e)
	}
	borrowed := make([]*pgxpool.Conn, 0, 8)
	for i := 0; i < 8; i++ {
		conn, err := sp.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		borrowed = append(borrowed, conn)
	}
	start := time.Now()
	e = NewRuntime(sp).Unit(ctx, func(context.Context, *pgxpool.Conn) error { return nil })
	for _, conn := range borrowed {
		conn.Release()
	}
	if e == nil || time.Since(start) > time.Second {
		t.Fatalf("pool acquisition was not bounded: %v", e)
	}
	if _, e = sp.Exec(ctx, "CREATE TABLE public.serving_must_not_create (id integer)"); e == nil {
		t.Fatal("serving role unexpectedly has DDL authority")
	}
	if _, e = sp.Exec(ctx, "CREATE TEMP TABLE serving_must_not_create_temp (id integer)"); e == nil {
		t.Fatal("serving role unexpectedly has TEMP authority")
	}
	steps := append(append([]migration{}, migrations...), migration{ExpectedRevision + 1, "CREATE TABLE public.rollback_probe(id integer); SELECT 1/0"})
	if e = migrate(ctx, p, steps); e == nil {
		t.Fatal("failed migration reported success")
	}
	var exists bool
	if e = p.QueryRow(ctx, "SELECT to_regclass('public.rollback_probe') IS NOT NULL").Scan(&exists); e != nil || exists {
		t.Fatalf("failed transaction left DDL: %v, %v", exists, e)
	}
	if _, e = p.Exec(ctx, "UPDATE public.schema_migrations SET checksum = repeat('0',64) WHERE revision=1"); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, p); e == nil {
		t.Fatal("changed checksum accepted")
	}
	if e = VerifyServing(ctx, sp); e == nil {
		t.Fatal("serving accepted changed checksum")
	}
	if _, e = p.Exec(ctx, "UPDATE public.schema_migrations SET checksum=$1 WHERE revision=1", checksum(migrations[0].sql)); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, "INSERT INTO public.schema_migrations(revision,checksum) VALUES ($1,repeat('0',64))", ExpectedRevision+1); e != nil {
		t.Fatal(e)
	}
	if e = VerifyServing(ctx, sp); e == nil {
		t.Fatal("serving accepted newer schema")
	}
	if e = Migrate(ctx, p); e == nil {
		t.Fatal("migration accepted unknown revision")
	}
}
