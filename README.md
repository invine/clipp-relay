# Clipp Relay service

This repository implements the service foundation, Google portal registration,
administrator account and quota-plan operations, shared weekly Quota operation,
and authenticated TCP, WSS and WebRTC Direct Circuit Relay v2 traffic. It provides
explicit PostgreSQL migration, fail-closed startup checks, PostgreSQL-backed
Relay Accounts and Portal Sessions, registered public-client OAuth with Relay
Access Tokens, separate public and private HTTP listeners, bearer discovery,
exact-connection Relay Sessions, and stock HOP/STOP forwarding.
Native authorization and refresh operations declare their persistence
capabilities at admission; Auth owns each transaction while Fosite callbacks
share that same transaction for credential reads and writes.
Owner deletion uses the separate Recovery Journal: a committed conditional
head and exact immutable event content must be proven before identity detaches.
The journal owns append, exact readback, conditional head advancement, lost
acknowledgement reconciliation, committed-chain proof, and justified rebasing.
Auth records each immutable event preparation in the existing deletion operation
before journal I/O; Auth retains due-work selection, retries, accounting, and final
deletion. Startup qualification and offline recovery share the same
repository-qualified chain proof; offline recovery rechecks the head and ETag
before changing restored accounts.
Confirmed current-week Recorded Relay Usage follows a fresh Pending registration by keyed
digest; old account state, plan, credentials, and presentation do not.
With periodic accounting, deletion first invalidates credentials and physically
closes sessions. After mandatory Recovery Journal proof, it attempts a bounded
best-effort final settlement outside account protection and database row locks.
A failed or unresolved flush alone cannot delay finalization. Finalization locks
the old account while capturing confirmed current-week usage and sealing further
settlements in one transaction. A committed batch with a lost reply is included;
late work cannot charge a replacement account. Re-registration imports only that
current-week total once, as an opening total without a settlement receipt.
Quota aggregates observed account-week traffic across sessions and settles nonempty
immutable batches on a one-second target cadence. The confirmed total and latest
retry receipt commit atomically; lost replies reconcile the identical operation
once. Eligibility counts confirmed and locally unsettled usage once. The allowance
is soft: full attributable overrun is recorded, and observations not persisted
before a crash may be lost. No historical settlement replay or multi-instance
writer coordination is supported. The accounting failure deadline defaults to
three seconds and closes affected sessions independently of blocked SQL workers.
Quota keeps pending observations, the immutable in-flight batch, its confirmed
receipt, uncertainty and retirement together for each account and week. Each
window serializes settlement without holding the observation lock during SQL;
eligibility and policy changes use the same effective-usage calculation.
The owner profile shows confirmed Recorded Relay Usage and twelve preceding
weekly totals. Its capacity table distinguishes configured limits from separately
sampled live counts. Active Login Grants are sampled from PostgreSQL; live Relay
Sessions are sampled from this process. A confirmed grant-local revocation closes
Relay Sessions authenticated with that Login Grant; Account-wide Revocation and
account access restrictions invalidate all affected sessions. Auth keeps administrator
changes, Account-wide Revocation, and deletion admission as distinct complete operations
and renders their classified outcomes after protected work releases its guards. The
private Relay Session manager owns connection, authentication-attempt, reservation,
Rendezvous Lease, and bounded physical-close state. It removes logical permission
synchronously and performs physical closure after Auth releases protection. Already admitted streams and buffered
traffic can finish. Notifications are in-process: direct
database edits do not invalidate sessions immediately, and multiple relay processes
are unsupported.
`/readyz` is `200 ok` only after every enabled relay listener and a complete
public address snapshot are available; otherwise it is `503 unavailable`. `/livez` is
`200 ok` while the process runs. Public relay discovery is withdrawn during
drain or when the address snapshot expires.
Relay Authentication sends one length-delimited JSON request and half-closes
its write side; the relay checks end of request before returning its response.
Rendezvous accepts one request per stream on both the legacy unframed v1 and
length-delimited v2 protocols, then sends one response and closes the stream.
Both versions answer a complete request even while the client's write side is
open, preserving v1 clients that wait for a response before closing. The server
resets a stream when extra bytes are already queued after the request. Bytes
sent only after the response cannot retroactively invalidate that response;
clients can half-close after their request to make the end explicit.

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
go run ./cmd/clipp-relay -command validate-config -config /path/to/config.json
go run ./cmd/clipp-relay -command migrate -config /path/to/config.json
go run ./cmd/clipp-relay -command reset-auth -config /path/to/migration-config.json
go run ./cmd/clipp-relay -command serve -config /path/to/config.json
```

`validate-config` checks strict JSON, runtime configuration rules, and static
serving requirements without opening referenced Secret files or connecting to
the database, journal, or listeners. It requires at least one relay listener
and a configured Recovery Journal. A successful result means configuration
acceptance only; it does not establish Secret availability, live schema
compatibility, external reachability, or operational readiness. Migration,
reset, and recovery continue to apply their own command-specific requirements.

Provision one stable 32-byte OAuth signing Secret before migration. Fence every
old writer, apply migrations, then run `reset-auth` with migration credentials
and the mounted signing Secret before enabling serving. The reset removes old
and native OAuth credentials, releases grant capacity, and records its key
identity and audit outcome while preserving Relay Accounts, Portal Sessions,
Quota Plans and usage. A retry with the same key is a no-op after a confirmed
commit; a changed key makes `serve` fail closed until `reset-auth` completes.
Startup never resets credentials.

The two commands must use separate database username/password Secret files.
The migration process reads only its database username, password and CA files;
the other referenced Secret files need not be mounted in the migration Job.
Serving checks the exact schema revision and every checksum; it never migrates.
Migration runners serialize with a PostgreSQL advisory lock, then commit each
transaction-compatible migration with its revision marker. Stop serving before
a schema change.

The single non-secret JSON configuration is versioned. No environment or flag
overrides affect policy. Unknown and duplicate fields are rejected. Serving
requires at least one of `relay_tcp.listen`, `relay_websocket.listen`, or
`relay_webrtc_direct.listen`; migration does not start a relay listener.
`accounting_failure_deadline` is an optional Go duration string, defaults to
`"3s"`, and must be at least the one-second accounting cadence. Explicit null,
empty, zero, negative, nonfinite and overflowing values are rejected. It takes
effect on restart. With unsettled observations, successful balance reads do not
hide failed writes. Successful settlement cannot hide unavailable authoritative
eligibility. Each has an independent monotonic health deadline that closes all
affected account sessions and denies admission until accounting recovers. Idle accounts refresh through
authoritative reads without empty debits. This is a soft allowance, without a
fixed byte-overrun bound. Policy cap edits retain recorded and pending usage;
equality is exhausted. Suspension, account-wide revocation and session-cap
reductions below the live count apply immediately through protected mutation.

Every enabled transport needs a complete public address source for discovery
and readiness. WSS addresses are configured. TCP and WebRTC Direct use either
explicit `public_addresses` overrides or a named Kubernetes Service in
`relay_services`; configured `public_port` supplies each watched transport's
published port. An override removes only that transport's Service dependency.
The serving ServiceAccount needs read-only `get`, `list`, and `watch` access
limited to those named Services in its namespace. The process reads its
projected token on every API request, watches changes, and resyncs at least
once per minute. An API outage can retain a complete last-verified snapshot
for at most five minutes; missing addresses withdraw the whole document.
The WebSocket listener is internal plaintext behind public TLS termination on
the distinct exact `wss_hostname`; it does not serve portal or private routes.
The service adds the current Peer ID to all published addresses and the current
process certificate hash to WebRTC Direct addresses. An example shape is:

```json
{
  "version": 1,
  "accounting_failure_deadline": "3s",
  "portal_origin": "https://portal.example.com",
  "wss_hostname": "wss.example.com",
  "public_clients": {
    "android_redirect": "clipp-relay://oauth/callback",
    "extension_redirect": "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
  },
  "listeners": { "public": ":8080", "private": ":8081" },
  "relay_tcp": {
    "listen": "/ip4/0.0.0.0/tcp/4001",
    "public_addresses": ["/dns4/relay.example.com/tcp/4001"]
  },
  "relay_websocket": {
    "listen": "/ip4/0.0.0.0/tcp/4002/ws",
    "public_addresses": ["/dns4/wss.example.com/tcp/443/tls/ws"]
  },
  "relay_webrtc_direct": {
    "listen": "/ip4/0.0.0.0/udp/4003/webrtc-direct",
    "public_addresses": ["/dns4/relay.example.com/udp/443/webrtc-direct"]
  },
  "relay_services": {
    "namespace": "clipp",
    "tcp_name": "clipp-relay-tcp",
    "udp_name": "clipp-relay-udp",
    "token_file": "/var/run/secrets/kubernetes.io/serviceaccount/token",
    "ca_file": "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
  },
  "database": {
    "mode": "external",
    "host": "db.internal.example.com",
    "port": 5432,
    "name": "clipp_relay",
    "username_file": "/run/secrets/db/username",
    "password_file": "/run/secrets/db/password",
    "ca_file": "/run/secrets/db/ca.crt"
  },
  "journal": {
    "region": "us-ashburn-1",
    "namespace": "operator-namespace",
    "bucket": "independent-deletion-journal",
    "repository_id": "operator-attested-repository-id",
    "coverage_floor": 0,
    "coverage_hash": "0000000000000000000000000000000000000000000000000000000000000000",
    "tenancy_ocid": "ocid1.tenancy.oc1..example",
    "user_ocid": "ocid1.user.oc1..example",
    "fingerprint": "aa:bb:cc:dd:example",
    "private_key_file": "/run/secrets/journal/api-signing-key.pem"
  },
  "secrets": {
    "google_client_id_file": "/run/secrets/google/client-id",
    "google_client_secret_file": "/run/secrets/google/client-secret",
    "admin_allowlist_file": "/run/secrets/admin/allowlist.json",
    "pepper_keyring_file": "/run/secrets/peppers/keyring.json",
    "native_signing_key_file": "/run/secrets/native-oauth/signing-key"
  }
}
```

Serving requires this journal configuration and an already existing head whose
repository identity, coverage floor and coverage hash match the operator's
attested values. It never initializes an empty repository. The signing key is
read from the mounted Secret; requests use OCI native RSA-SHA256 signatures
over verified HTTPS to the regional Object Storage endpoint. The serving
identity needs immutable event create/read and conditional head replacement,
with no event overwrite/delete or backup authority. The local smoke harness
uses a process scoped TLS CONNECT fixture for startup checks; that is local
protocol evidence, not OCI service or IAM qualification.

This example uses explicit TCP and UDP overrides, so the `relay_services`
entries are inactive. Remove a transport's `public_addresses` to source that
transport from its named Service and set its `public_port` to the expected
Service port. Kubernetes injects
`KUBERNETES_SERVICE_HOST` and `KUBERNETES_SERVICE_PORT_HTTPS`; the API client
verifies the mounted CA and never publishes a partial enabled-transport set.

The allowlist file contains `{"revision":1,"emails":[]}`. The keyring file
contains `{"current":1,"keys":[{"version":1,"material":"<base64 of at least 32 bytes>"}]}`.
The Google client and pepper files supply portal registration and sign-in.
An allowlisted administrator opens `/admin` to create or archive immutable
quota plans, approve a Pending account with a plan, suspend or deny access,
return Denied accounts to Pending review, reactivate Suspended accounts,
revoke credentials, and reassign plans or set per-account quota overrides.
Owners can sign out of the portal alone or sign out everywhere, which revokes
their relay credentials and logically invalidates all live Relay Sessions before
the operation succeeds; physical connection closure follows asynchronously.
Revoking one Login Grant through supported credential flows invalidates only
sessions authenticated with that grant. The seeded
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
integration tests, including admin approval, plan assignment, quota receipt
replay, rollback, restart, week rollover, clock faults, and profile history.
It also rejects a
verified-TLS PostgreSQL 16 target. It removes only containers created by the
script and all temporary Secret files on exit. It does not use any existing
database or Docker volume.

Other checks: `gofmt -l cmd internal`, `go vet ./...`, `go test ./...`.

For local Linux ARM64/AMD64 image builds and the manual GHCR publication workflow,
see [Relay image build](docs/image-build.md).

### Recorded usage query and weekly retention

The service displays **Recorded Relay Usage**:
confirmed durable traffic observations, with periodic confirmation lag. Recent
pending traffic does not appear as confirmed. HOP and STOP endpoint observations
count independently (including twice for one account), and control bytes may
count; this is a conservative traffic measure, not exact application payload.
Owner and administrator usage views each snapshot account policy and usage in
one database statement. Separate live capacity samples are not frozen with that
snapshot and presentation reads grant no Relay Authentication authority.

Weekly Quota Windows begin Monday at 00:00 UTC. A session keeps its fixed
credential expiry across Monday; new HOP/STOP use waits for authoritative
eligibility in the new week, without a new sign-in. The fixed accounting workers
refresh continuing accounts on the one-second target cadence. Existing admitted
stock circuits retain the accounting failure deadline while a new-week read is
unavailable, even when settlement writes succeed. This is a soft allowance,
with no hard boundary byte guarantee.
Observations retain their processing week through lock waits and lost replies.

History retains the current week and twelve preceding weeks as absolute totals,
not percentages of today's allowance. Remaining allowance is clamped to zero
after overrun. Weekly totals and their latest retry receipts expire together;
bounded cleanup also removes hidden account children fairly. Local unresolved
work stays attached to its original week until settlement or authoritative
retirement. Ordinary disconnects and revocation do not discard pending usage;
expired weeks cannot be recreated or charged to a newer week by a stale retry.

### Offline accounting migration and compatibility

Schema revision **13** makes observed accounting the sole serving behavior. Stop
and fence the old process before running the migration with the maintenance role;
then start the new binary with its restricted serving role. There is no accounting
mode switch and no mixed-version serving. See [offline accounting cutover](docs/operator/accounting-migration.md).
The explicit migration drops obsolete prepaid totals and allocation receipts and
clears test retained usage before renaming its amount to `recorded_bytes`. It
preserves accounts, plans, credentials, audit, Recovery Journal/recovery state,
and already confirmed observed totals and receipts. Migration reruns and ordinary
restart never reset new usage. Exact schema/checksum verification rejects the old
binary against revision13 and the new binary against unmigrated schemas.
