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

const ExpectedRevision = 5

type migration struct {
	revision int
	sql      string
}

var migrations = []migration{{1, `CREATE TABLE public.schema_migrations (
 revision integer PRIMARY KEY CHECK (revision > 0),
 checksum text NOT NULL CHECK (length(checksum) = 64),
 applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`}, {2, `CREATE TABLE public.accounts (
 id uuid PRIMARY KEY,
 issuer text NOT NULL CHECK (issuer = 'https://accounts.google.com'),
 subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
 email text NOT NULL CHECK (length(email) BETWEEN 1 AND 320),
 email_verified boolean NOT NULL,
 hosted_domain text CHECK (hosted_domain IS NULL OR length(hosted_domain) <= 255),
 validated_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 last_portal_login_at timestamptz NOT NULL,
 status text NOT NULL DEFAULT 'Pending' CHECK (status IN ('Pending','Active','Suspended','Denied')),
 credential_generation bigint NOT NULL DEFAULT 0 CHECK (credential_generation >= 0),
 UNIQUE (issuer,subject)
);
CREATE TABLE public.authorization_transactions (
 id uuid PRIMARY KEY,
 state_digest bytea NOT NULL UNIQUE CHECK (octet_length(state_digest)=32),
 nonce_digest bytea NOT NULL CHECK (octet_length(nonce_digest)=32),
 binding_digest bytea NOT NULL CHECK (octet_length(binding_digest)=32),
 pepper_version bigint NOT NULL CHECK (pepper_version>0),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 claimed_at timestamptz
);
CREATE INDEX authorization_transactions_expiry ON public.authorization_transactions (expires_at);
CREATE TABLE public.portal_sessions (
 credential_digest bytea PRIMARY KEY CHECK (octet_length(credential_digest)=32),
 pepper_version bigint NOT NULL CHECK (pepper_version>0),
 account_id uuid NOT NULL REFERENCES public.accounts(id),
 credential_generation bigint NOT NULL,
 csrf_digest bytea NOT NULL CHECK (octet_length(csrf_digest)=32),
 google_authenticated_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 last_used_at timestamptz NOT NULL,
 idle_expires_at timestamptz NOT NULL,
 absolute_expires_at timestamptz NOT NULL
);
CREATE INDEX portal_sessions_account ON public.portal_sessions (account_id);
CREATE INDEX portal_sessions_expiry ON public.portal_sessions (idle_expires_at,absolute_expires_at);
CREATE TABLE public.audit_events (
 id uuid PRIMARY KEY,
 occurred_at timestamptz NOT NULL,
 event text NOT NULL CHECK (event IN ('account_created')),
 account_id uuid NOT NULL
);
CREATE INDEX audit_events_expiry ON public.audit_events (occurred_at);
REVOKE ALL ON public.accounts, public.authorization_transactions, public.portal_sessions, public.audit_events FROM PUBLIC`}, {3, `CREATE INDEX portal_sessions_absolute_expiry ON public.portal_sessions (absolute_expires_at)`}, {4, `CREATE TABLE public.quota_plans (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
 weekly_bytes bigint NOT NULL CHECK (weekly_bytes >= 0),
 sessions integer NOT NULL CHECK (sessions >= 0),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 archived_at timestamptz,
 first_assigned_at timestamptz,
 create_request_digest bytea CHECK (create_request_digest IS NULL OR octet_length(create_request_digest)=32)
);
ALTER TABLE public.accounts ADD COLUMN plan_id uuid REFERENCES public.quota_plans(id);
ALTER TABLE public.accounts ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE public.accounts ADD CONSTRAINT active_has_plan CHECK (status <> 'Active' OR plan_id IS NOT NULL);
CREATE INDEX accounts_admin_page ON public.accounts (created_at,id);
CREATE INDEX quota_plans_admin_page ON public.quota_plans (created_at,id);
CREATE INDEX accounts_plan_id ON public.accounts (plan_id);
CREATE FUNCTION public.enforce_assigned_plan_allowance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.first_assigned_at IS NOT NULL AND (NEW.weekly_bytes <> OLD.weekly_bytes OR NEW.sessions <> OLD.sessions) THEN
  RAISE EXCEPTION 'assigned plan allowance is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER assigned_plan_allowance BEFORE UPDATE ON public.quota_plans FOR EACH ROW EXECUTE FUNCTION public.enforce_assigned_plan_allowance();
ALTER TABLE public.audit_events DROP CONSTRAINT audit_events_event_check;
ALTER TABLE public.audit_events ADD CONSTRAINT audit_events_event_check CHECK (event IN ('account_created','account_approved','account_denied','plan_created','plan_archived','plan_assigned'));
ALTER TABLE public.audit_events ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE public.audit_events ADD COLUMN plan_id uuid;
ALTER TABLE public.audit_events ADD COLUMN outcome text NOT NULL DEFAULT 'succeeded' CHECK (outcome='succeeded');
ALTER TABLE public.audit_events ADD COLUMN reason text CHECK (reason IN ('routine_administration','policy_enforcement','suspected_abuse','security_response','support_correction'));
ALTER TABLE public.audit_events ADD COLUMN actor_email text CHECK (length(actor_email) BETWEEN 1 AND 320);
ALTER TABLE public.audit_events ADD COLUMN client_type text CHECK (client_type IN ('electron','android','extension'));
ALTER TABLE public.audit_events ADD CONSTRAINT audit_actor_required CHECK (event='account_created' OR (reason IS NOT NULL AND actor_email IS NOT NULL));
ALTER TABLE public.audit_events ADD CONSTRAINT audit_target_required CHECK ((event IN ('account_created','account_approved','account_denied','plan_assigned') AND account_id IS NOT NULL) OR (event IN ('plan_created','plan_archived') AND plan_id IS NOT NULL));
INSERT INTO public.quota_plans (id,name,weekly_bytes,sessions) VALUES ('6dd09395-51a0-451c-96b3-716e6038e870','Baseline',1073741824,5);
REVOKE ALL ON public.quota_plans FROM PUBLIC`}, {5, `CREATE TABLE public.authorization_codes (
 credential_digest bytea PRIMARY KEY CHECK (octet_length(credential_digest)=32),
 pepper_version bigint NOT NULL CHECK (pepper_version>0),
 account_id uuid NOT NULL REFERENCES public.accounts(id),
 credential_generation bigint NOT NULL,
 client_type text NOT NULL CHECK (client_type IN ('electron','android','extension')),
 redirect_uri text NOT NULL CHECK (length(redirect_uri) BETWEEN 1 AND 512),
 challenge text NOT NULL CHECK (length(challenge)=43),
 issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz
);
CREATE INDEX authorization_codes_expiry ON public.authorization_codes (expires_at);
CREATE INDEX authorization_codes_account ON public.authorization_codes (account_id);
CREATE TABLE public.login_grants (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES public.accounts(id),
 credential_generation bigint NOT NULL,
 client_type text NOT NULL CHECK (client_type IN ('electron','android','extension')),
 current_refresh_generation bigint NOT NULL DEFAULT 1 CHECK (current_refresh_generation>0),
 created_at timestamptz NOT NULL,
 last_used_at timestamptz NOT NULL,
 idle_expires_at timestamptz NOT NULL,
 absolute_expires_at timestamptz NOT NULL,
 terminated_at timestamptz
);
CREATE INDEX login_grants_account ON public.login_grants (account_id);
CREATE INDEX login_grants_expiry ON public.login_grants (idle_expires_at,absolute_expires_at);
CREATE INDEX login_grants_absolute_expiry ON public.login_grants (absolute_expires_at);
CREATE INDEX login_grants_terminated ON public.login_grants (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE TABLE public.refresh_generations (
 credential_digest bytea PRIMARY KEY CHECK (octet_length(credential_digest)=32),
 pepper_version bigint NOT NULL CHECK (pepper_version>0),
 grant_id uuid NOT NULL REFERENCES public.login_grants(id),
 generation bigint NOT NULL CHECK (generation>0),
 issued_at timestamptz NOT NULL,
 consumed_at timestamptz,
 UNIQUE(grant_id,generation)
);
CREATE INDEX refresh_generations_grant ON public.refresh_generations (grant_id);
CREATE UNIQUE INDEX refresh_generations_one_current ON public.refresh_generations (grant_id) WHERE consumed_at IS NULL;
CREATE TABLE public.relay_access_tokens (
 credential_digest bytea PRIMARY KEY CHECK (octet_length(credential_digest)=32),
 pepper_version bigint NOT NULL CHECK (pepper_version>0),
 grant_id uuid NOT NULL REFERENCES public.login_grants(id),
 issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX relay_access_tokens_grant ON public.relay_access_tokens (grant_id);
CREATE INDEX relay_access_tokens_expiry ON public.relay_access_tokens (expires_at);
ALTER TABLE public.audit_events DROP CONSTRAINT audit_events_event_check;
ALTER TABLE public.audit_events ADD CONSTRAINT audit_events_event_check CHECK (event IN ('account_created','account_approved','account_denied','plan_created','plan_archived','plan_assigned','credential_blocked','grant_cap','refresh_reuse'));
ALTER TABLE public.audit_events DROP CONSTRAINT audit_target_required;
ALTER TABLE public.audit_events ADD CONSTRAINT audit_target_required CHECK ((event IN ('account_created','account_approved','account_denied','plan_assigned','credential_blocked','grant_cap','refresh_reuse') AND account_id IS NOT NULL) OR (event IN ('plan_created','plan_archived') AND plan_id IS NOT NULL));
ALTER TABLE public.audit_events DROP CONSTRAINT audit_actor_required;
ALTER TABLE public.audit_events ADD CONSTRAINT audit_actor_required CHECK (event IN ('account_created','credential_blocked','grant_cap','refresh_reuse') OR (reason IS NOT NULL AND actor_email IS NOT NULL));
ALTER TABLE public.authorization_transactions ADD COLUMN flow_kind text NOT NULL DEFAULT 'google' CHECK (flow_kind IN ('google','clipp'));
ALTER TABLE public.authorization_transactions ADD COLUMN client_type text CHECK (client_type IN ('electron','android','extension'));
ALTER TABLE public.authorization_transactions ADD COLUMN redirect_uri text CHECK (redirect_uri IS NULL OR length(redirect_uri) BETWEEN 1 AND 512);
ALTER TABLE public.authorization_transactions ADD COLUMN challenge text CHECK (challenge IS NULL OR length(challenge)=43);
ALTER TABLE public.authorization_transactions ADD COLUMN client_state_digest bytea CHECK (client_state_digest IS NULL OR octet_length(client_state_digest)=32);
ALTER TABLE public.authorization_transactions ADD COLUMN account_id uuid REFERENCES public.accounts(id);
ALTER TABLE public.authorization_transactions ADD COLUMN credential_generation bigint;
ALTER TABLE public.authorization_transactions ADD CONSTRAINT clipp_transaction_complete CHECK (flow_kind='google' OR (client_type IS NOT NULL AND redirect_uri IS NOT NULL AND challenge IS NOT NULL AND client_state_digest IS NOT NULL AND account_id IS NOT NULL AND credential_generation IS NOT NULL));
REVOKE ALL ON public.authorization_codes,public.login_grants,public.refresh_generations,public.relay_access_tokens FROM PUBLIC`}}

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
