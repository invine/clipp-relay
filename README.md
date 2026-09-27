# Clipp Relay service

This repository currently implements the service foundation, Google portal
registration, and administrator account and quota-plan operations. It provides
explicit PostgreSQL migration, fail-closed startup checks, PostgreSQL-backed
Relay Accounts and Portal Sessions, and separate public and private HTTP
listeners. It does **not** yet provide Relay credentials, Relay Sessions or
public relay transport.
`/readyz` therefore remains `503 unavailable`; `/livez` is `200 ok` while the
process runs. A later relay slice must start its listeners and publish a
complete address snapshot before it may change readiness.

## Run

Requires Go 1.27.1 or newer and PostgreSQL 17 or 18 with verified TLS and
SCRAM-authenticated, separately provisioned roles. The migration identity
needs CREATE on the target schema. The serving identity needs SELECT on
`public.schema_migrations`, no persistent DDL authority, and an administrator
set `temp_file_limit` of at most 64 MB. Revoke database `TEMPORARY` from PUBLIC
and the serving role as well as schema CREATE; serving cannot own the database,
an extension, or other application objects, or join an owning/DDL-capable role.
Bootstrap and role provisioning are explicit operator steps; this binary never
creates roles or databases.

```sh
go run ./cmd/clipp-relay -command migrate -config /path/to/config.json
go run ./cmd/clipp-relay -command serve -config /path/to/config.json
```

The two commands must use separate database username/password Secret files.
The migration process reads only its database username, password and CA files;
the other referenced Secret files need not be mounted in the migration Job.
Serving checks the exact schema revision and every checksum; it never migrates.
Migration runners serialize with a PostgreSQL advisory lock, then commit each
transaction-compatible migration with its revision marker. Stop serving before
a schema change.

The single non-secret JSON configuration is versioned. No environment or flag
overrides affect policy. All fields are required; unknown and duplicate fields
are rejected. An example shape is:

```json
{
  "version": 1,
  "portal_origin": "https://portal.example.com",
  "wss_hostname": "wss.example.com",
  "listeners": { "public": ":8080", "private": ":8081" },
  "database": {
    "mode": "external",
    "host": "db.internal.example.com",
    "port": 5432,
    "name": "clipp_relay",
    "username_file": "/run/secrets/db/username",
    "password_file": "/run/secrets/db/password",
    "ca_file": "/run/secrets/db/ca.crt"
  },
  "secrets": {
    "google_client_id_file": "/run/secrets/google/client-id",
    "google_client_secret_file": "/run/secrets/google/client-secret",
    "admin_allowlist_file": "/run/secrets/admin/allowlist.json",
    "pepper_keyring_file": "/run/secrets/peppers/keyring.json"
  }
}
```

The allowlist file contains `{"revision":1,"emails":[]}`. The keyring file
contains `{"current":1,"keys":[{"version":1,"material":"<base64 of at least 32 bytes>"}]}`.
The Google client and pepper files supply portal registration and sign-in.
An allowlisted administrator opens `/admin` to create or archive immutable
quota plans, then approve a Pending account with a plan or deny it. The seeded
Baseline plan provides exactly 1,073,741,824 bytes per week and five sessions.
Admin mutations require a Portal Session with Google authentication no older
than ten minutes, a valid Origin and CSRF token, an enumerated reason, and the
current account or plan revision. The mounted allowlist is read for each admin
request: an empty list grants nobody, and an unreadable or malformed update
denies admin access. The nonsecret applied revision appears in the admin page
and response header. Allowlist entries use exact ASCII case-folded email
matching. All admin identities need verified email; the address must be Gmail
or have a Google hosted-domain claim. Changes to the mounted file take effect
on the next observed request
without restarting the service.
The database connection always uses hostname and CA verification, with no
plaintext fallback. Normal application logs omit credential values and stable
identity labels.

## Local real-database check

`bash scripts/smoke-postgres.sh` creates disposable PostgreSQL 18 and 17 Docker
containers bound only to loopback, provisions TLS and distinct roles, migrates,
starts the process, checks health and public route isolation, stops PostgreSQL
to check liveness, and runs the real SQL and deterministic local OIDC provider
integration tests, including admin approval and plan assignment. It also rejects a
verified-TLS PostgreSQL 16 target. It removes only containers created by the
script and all temporary Secret files on exit. It does not use any existing
database or Docker volume.

Other checks: `gofmt -l cmd internal`, `go vet ./...`, `go test ./...`.
