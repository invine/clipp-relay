package database

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"clipp-relay/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ExpectedRevision = 1

type migration struct {
	revision int
	sql      string
}

var migrations = []migration{{1, `CREATE TABLE public.schema_migrations (
 revision integer PRIMARY KEY CHECK (revision > 0),
 checksum text NOT NULL CHECK (length(checksum) = 64),
 applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`}}

func checksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

func poolConfig(c config.Config, m config.Material, maintenance bool) (*pgxpool.Config, error) {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(m.DBUsername, m.DBPassword), Host: fmt.Sprintf("%s:%d", c.Database.Host, c.Database.Port), Path: "/" + c.Database.Name}
	q := u.Query()
	q.Set("sslmode", "verify-full")
	u.RawQuery = q.Encode()
	pc, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, errors.New("invalid database connection configuration")
	}
	pc.ConnConfig.TLSConfig = &tls.Config{RootCAs: m.DBRootCAs, ServerName: c.Database.Host, MinVersion: tls.VersionTLS12}
	pc.ConnConfig.Fallbacks = nil
	pc.ConnConfig.ConnectTimeout = 3 * time.Second
	pc.MinConns = 0
	if maintenance {
		pc.MaxConns = 1
		pc.ConnConfig.RuntimeParams["statement_timeout"] = "300000"
		pc.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
		pc.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "60000"
		pc.ConnConfig.RuntimeParams["maintenance_work_mem"] = "32MB"
	} else {
		pc.MaxConns = 8
		pc.MaxConnIdleTime = 5 * time.Minute
		pc.MaxConnLifetime = 30 * time.Minute
		pc.MaxConnLifetimeJitter = 5 * time.Minute
		pc.HealthCheckPeriod = time.Minute
		pc.ConnConfig.RuntimeParams["statement_timeout"] = "2000"
		pc.ConnConfig.RuntimeParams["lock_timeout"] = "250"
		pc.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"
	}
	return pc, nil
}

func NewPool(ctx context.Context, c config.Config, m config.Material, maintenance bool) (*pgxpool.Pool, error) {
	pc, err := poolConfig(c, m, maintenance)
	if err != nil {
		return nil, err
	}
	p, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, errors.New("database pool initialization failed")
	}
	return p, nil
}

func verifyMajor(ctx context.Context, conn *pgxpool.Conn) error {
	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		return errors.New("database version unavailable")
	}
	n, err := strconv.Atoi(version)
	if err != nil || n/10000 < 17 || n/10000 > 18 {
		return errors.New("unsupported PostgreSQL major")
	}
	return nil
}

func VerifyServing(ctx context.Context, p *pgxpool.Pool) error {
	conn, err := p.Acquire(ctx)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer conn.Release()
	if err := verifyMajor(ctx, conn); err != nil {
		return err
	}
	var privileged bool
	// pg_shdepend covers extensions and other owned objects. Explicit owner
	// catalog checks remain because initdb-pinned objects can lack dependencies.
	err = conn.QueryRow(ctx, `
SELECT
  EXISTS (
    SELECT 1 FROM pg_roles reachable
    WHERE (reachable.rolname = current_user OR pg_has_role(reachable.oid, 'MEMBER'))
      AND (
        reachable.rolsuper OR reachable.rolcreatedb OR reachable.rolcreaterole
        OR EXISTS (
          SELECT 1 FROM pg_database db WHERE db.datname = current_database()
            AND (has_database_privilege(reachable.oid, db.oid, 'CREATE')
              OR has_database_privilege(reachable.oid, db.oid, 'TEMP'))
        )
        OR EXISTS (
          SELECT 1 FROM pg_namespace ns
          WHERE left(ns.nspname, 3) <> 'pg_' AND ns.nspname <> 'information_schema'
            AND has_schema_privilege(reachable.oid, ns.oid, 'CREATE')
        )
      )
  )
  OR EXISTS (
    SELECT 1 FROM pg_database db
    WHERE db.datname = current_database()
      AND (db.datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(db.datdba, 'MEMBER'))
  )
  OR EXISTS (
    SELECT 1 FROM pg_shdepend ownership
    JOIN pg_database db ON db.datname = current_database()
    WHERE ownership.deptype = 'o'
      AND ownership.dbid IN (0, db.oid)
      AND (ownership.refobjid = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(ownership.refobjid, 'MEMBER'))
  )
  OR EXISTS (
    SELECT 1 FROM pg_namespace ns
    WHERE left(ns.nspname, 3) <> 'pg_' AND ns.nspname <> 'information_schema'
      AND (ns.nspowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(ns.nspowner, 'MEMBER'))
  )
  OR EXISTS (
    SELECT 1 FROM pg_class rel JOIN pg_namespace ns ON ns.oid = rel.relnamespace
    WHERE left(ns.nspname, 3) <> 'pg_' AND ns.nspname <> 'information_schema'
      AND (rel.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(rel.relowner, 'MEMBER'))
  )
  OR EXISTS (
    SELECT 1 FROM pg_proc routine JOIN pg_namespace ns ON ns.oid = routine.pronamespace
    WHERE left(ns.nspname, 3) <> 'pg_' AND ns.nspname <> 'information_schema'
      AND (routine.proowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(routine.proowner, 'MEMBER'))
  )
  OR EXISTS (
    SELECT 1 FROM pg_type typ JOIN pg_namespace ns ON ns.oid = typ.typnamespace
    WHERE left(ns.nspname, 3) <> 'pg_' AND ns.nspname <> 'information_schema'
      AND (typ.typowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        OR pg_has_role(typ.typowner, 'MEMBER'))
  )`).Scan(&privileged)
	if err != nil || privileged {
		return errors.New("serving database role has DDL authority or cannot be verified")
	}
	var boundedTemp bool
	if err = conn.QueryRow(ctx, `SELECT pg_size_bytes(current_setting('temp_file_limit')) BETWEEN 1 AND 67108864`).Scan(&boundedTemp); err != nil || !boundedTemp {
		return errors.New("serving database temporary-file limit unavailable or unsafe")
	}
	rows, err := conn.Query(ctx, "SELECT revision, checksum FROM public.schema_migrations ORDER BY revision")
	if err != nil {
		return errors.New("schema revision unavailable")
	}
	defer rows.Close()
	var revision int
	var hash string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&revision, &hash); err != nil {
			return errors.New("schema revision unavailable")
		}
		count++
		if revision != count || count > len(migrations) || hash != checksum(migrations[count-1].sql) {
			return errors.New("schema revision or checksum mismatch")
		}
	}
	if rows.Err() != nil || count != ExpectedRevision {
		return errors.New("schema revision mismatch")
	}
	return nil
}

func Migrate(ctx context.Context, p *pgxpool.Pool) error { return migrate(ctx, p, migrations) }

func migrate(ctx context.Context, p *pgxpool.Pool, steps []migration) error {
	conn, err := p.Acquire(ctx)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer conn.Release()
	if err := verifyMajor(ctx, conn); err != nil {
		return err
	}
	// Session advisory lock is held on this dedicated connection throughout verification and DDL.
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(57908122002)"); err != nil {
		return errors.New("migration lock unavailable")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock(57908122002)")
	}()
	var exists bool
	if err = conn.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return errors.New("schema state unavailable")
	}
	applied := 0
	if exists {
		rows, e := conn.Query(ctx, "SELECT revision, checksum FROM public.schema_migrations ORDER BY revision")
		if e != nil {
			return errors.New("migration history unavailable")
		}
		for rows.Next() {
			var rev int
			var hash string
			if e = rows.Scan(&rev, &hash); e != nil {
				break
			}
			applied++
			if rev != applied || applied > len(steps) || hash != checksum(steps[applied-1].sql) {
				e = errors.New("migration revision or checksum mismatch")
				break
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return errors.New("migration revision or checksum mismatch")
		}
	}
	for _, step := range steps[applied:] {
		tx, e := conn.Begin(ctx)
		if e != nil {
			return errors.New("migration transaction unavailable")
		}
		// Migration SQL is a checked-in literal. Simple protocol permits a
		// transaction-compatible sequence; the revision is recorded only after
		// every statement succeeds.
		if _, e = tx.Exec(ctx, step.sql, pgx.QueryExecModeSimpleProtocol); e == nil {
			_, e = tx.Exec(ctx, "INSERT INTO public.schema_migrations (revision,checksum) VALUES ($1,$2)", step.revision, checksum(step.sql))
		}
		if e != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = tx.Rollback(rollbackCtx)
			cancel()
			return errors.New("migration transaction failed")
		}
		if e = tx.Commit(ctx); e != nil {
			return errors.New("migration commit failed or uncertain")
		}
	}
	return nil
}

type Runtime struct {
	Pool      *pgxpool.Pool
	admission chan struct{}
}

func NewRuntime(p *pgxpool.Pool) *Runtime {
	return &Runtime{Pool: p, admission: make(chan struct{}, 64)}
}
func (r *Runtime) Unit(parent context.Context, work func(context.Context, *pgxpool.Conn) error) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	select {
	case r.admission <- struct{}{}:
		defer func() { <-r.admission }()
	case <-ctx.Done():
		return errors.New("database work admission unavailable")
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer acquireCancel()
	conn, err := r.Pool.Acquire(acquireCtx)
	if err != nil {
		return errors.New("database pool temporarily unavailable")
	}
	defer conn.Release()
	return work(ctx, conn)
}
