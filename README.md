# Clipp Relay service

This repository implements the service foundation, Google portal registration,
administrator account and quota-plan operations, shared weekly Quota operation,
and authenticated TCP, WSS and WebRTC Direct Circuit Relay v2 traffic. It provides
explicit PostgreSQL migration, fail-closed startup checks, PostgreSQL-backed
Relay Accounts and Portal Sessions, registered public-client OAuth with Relay
Access Tokens, separate public and private HTTP listeners, bearer discovery,
exact-connection Relay Sessions, and stock HOP/STOP forwarding.
The Quota operation commits at most 64 KiB of account-week credit per block
before making it locally usable. Callers must split larger reporter counts and
consume any smaller remaining local balance before requesting another block.
Committed credit is retained in PostgreSQL across restart and never refunded;
unused process-local credit is not restored. The owner profile shows Quota
committed and 12 completed weekly totals. Its capacity table distinguishes
configured limits from separately sampled live counts. Active Login Grants are
sampled from PostgreSQL; live Relay Sessions are sampled from this process.
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
overrides affect policy. Unknown and duplicate fields are rejected. Serving
requires at least one of `relay_tcp.listen`, `relay_websocket.listen`, or
`relay_webrtc_direct.listen`; migration does not start a relay listener.
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
  "secrets": {
    "google_client_id_file": "/run/secrets/google/client-id",
    "google_client_secret_file": "/run/secrets/google/client-secret",
    "admin_allowlist_file": "/run/secrets/admin/allowlist.json",
    "pepper_keyring_file": "/run/secrets/peppers/keyring.json"
  }
}
```

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
their relay credentials and closes live Relay Sessions. The seeded
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
