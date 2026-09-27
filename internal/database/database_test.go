package database

import (
	"context"
	"crypto/x509"
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
	if _, e = p.Exec(ctx, "DROP TABLE IF EXISTS public.deletion_operations, public.deletion_capacity, public.retained_quota_usage, public.weekly_quota_usage, public.relay_access_tokens, public.refresh_generations, public.login_grants, public.authorization_codes, public.portal_sessions, public.authorization_transactions, public.audit_events, public.accounts, public.quota_plans, public.schema_migrations CASCADE; DROP FUNCTION IF EXISTS public.enforce_assigned_plan_allowance() CASCADE"); e != nil {
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
