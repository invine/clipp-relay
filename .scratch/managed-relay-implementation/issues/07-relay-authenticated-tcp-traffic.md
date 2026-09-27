# 07: Relay authenticated TCP traffic

**What to build:** An approved client discovers the relay, authenticates its exact TCP connection and sends stock Circuit Relay v2 traffic while consuming account credit.

**Blocked by:** [05: Authorize and renew Clipp credentials](05-authorize-and-renew-clipp-credentials.md); [06: Commit weekly quota and show usage](06-commit-weekly-quota-and-show-usage.md).

**Status:** resolved

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I1, I4, I5, I8, I11.

## Contract and scope

One Go host, fresh in-memory Ed25519 identity per start, inbound-only peer
transport policy. GET /v1/relay requires scoped bearer over exact configured HTTPS,
no redirects, JSON/no-store; version=1, relay.peerId/addresses, validUntil only.
Initially explicit public-address overrides support isolated TCP tests; never
publish incomplete enabled transport set. Discovery admits any eligible Active
account independent of quota; 429 only global active-session cap, 503 dependency/
publication/rate overload, 401 auth, 400 malformed, Retry-After consistent.

Relay Auth /clipp/relay-auth/1.0.0: one unsigned-varint JSON request accessToken,
success ok/sessionExpiresAt/renewAfterMillis. Strict unique-key typed requests,
no extras/null/trailing frames/compression; 4KiB bodies, 10s operation. Initial
failure sends error then whole connection closes; semantic errors orderly,
bad framing/oversize reset. Before auth only setup/Identify/Ping/Auth.
Gate exact stream connection before unmodified HOP. No auth inheritance.

15m session capped token expiry; renew 75–85% admitted lifetime. Failed renewal
preserves deadline; other account same connection rejected; new same-Peer-ID
session swaps atomically after checks, even full, closing captured old connection.
Sorted account guards and generation-safe timers prevent stale cleanup.
Global sessions 6,000, secured pre-auth 256/10s, account default 5. Fund credit
before permission; stock HOP+STOP reporter charges both endpoints, twice same
account. Report-time association permits old tail charging replacement; absent
association aggregate only, no claimed exact overshoot bound. Exhaustion closes
all account sessions; buffered bytes may complete.

Stock reservations global/IP/IPv6-ASN each 6,000, TTL 30m, circuits/peer 16,
2MiB/direction whole circuit, 120s; stock server 60s control/30s STOP-connect.
No routine connection trimming, IP/subnet gates or allowlist bypass.
Explicit fixed RM system connections 8192/8192/512 C/I/O, streams
32768/32768/16384, FD8192/memory512MiB; transient connections256/256/256,
streams1024/1024/512 FD256/memory512MiB; peer connections4/4/4,
streams64/64/32 FD4/memory32MiB; connection 1/1/1, zero streams, FD1/8MiB;
stream zero connections, 1/1/1 streams, FD0/1MiB. Unknown/allowlisted scopes block.
Go soft memory1536MiB, required soft nofile≥16384, container2GiB.
Auth buckets global200/s burst400, connection1/s burst2; bounded buffers SetService.

The TCP slice installs its full enabled service/protocol profile immediately;
it must not wait for the additional-transports ticket. Each row has connections
0/0/0 and FD 0 in both aggregate and peer scopes. Stream values are combined /
inbound / outbound. Every zero blocks; use an empty fixed base, no inherited
defaults, scaling, identity overrides or IP/subnet restrictions.

| Scope | Streams | Memory | Peer streams | Peer memory |
| --- | --- | --- | --- | --- |
| libp2p.relay/v2 service | 24576/12288/12288 | 128MiB | 32/32/32 | 2MiB |
| HOP protocol | 12288/12288/0 | 64MiB | 32/32/0 | 1MiB |
| STOP protocol | 12000/0/12000 | 64MiB | 32/0/32 | 1MiB |
| libp2p.identify service | 4096/2048/2048 | 64MiB | 8/4/4 | 1MiB |
| Identify and Identify Push protocols, each | 2048/1024/1024 | 32MiB | 4/2/2 | 512KiB |
| libp2p.ping service and Ping protocol, each | 1024/1024/512 | 16MiB | 4/4/2 | 256KiB |
| clipp.relay-auth service and Relay Auth protocol, each | 512/512/0 | 8MiB | 8/8/0 | 256KiB |

Rendezvous adds only its explicitly accepted scopes when implemented by
[Register and find peers](08-register-and-find-peers.md). Assert the exact enabled
protocol inventory; disable unused AutoNAT/dial-back, UPnP, auto-relay/client-relay
transport and relay-side hole punching. No new relay-initiated peer dials.

## Acceptance criteria

- [x] Real JS/libp2p clients use bearer discovery, verify Noise Peer ID before sending token, reserve/connect over TCP and transfer actual opaque relayed bytes.
- [x] Unauthenticated second physical connection cannot use HOP even when its Peer ID has another authenticated session; stock PERMISSION_DENIED/resource statuses remain wire compatible.
- [x] Cross-account and same-account endpoint tests show conservative charging and closure, no DB per copied buffer or fabricated exact byte statistics.
- [x] Initial auth/renewal/account-change/expiry/replacement races obey deadlines, counts and captured-owner cleanup; failed replacement preserves old session.
- [x] Real DB failure rejects new authentication and allows only existing confirmed local credit/deadlines, with temporary versus quota outcomes distinguished.
- [x] Explicit RM/service/protocol limits, rates, descriptor check and malformed/oversize controls are asserted; no default autoscaling/hidden IP buckets.
- [x] Discovery uses 16KiB bound, full current peer addresses, no account/capacity fields and no cache; global cap differs from account quota and rate overload.
- [x] Go race and real TCP wire tests pass, including full-capacity one-for-one replacement under an isolated smaller test profile.

## Demonstration

Authorize two harness clients, discover/authenticate/reserve, open a cross-account TCP circuit, exhaust a small test-only allowance and observe account-wide connection closure.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

- Claimed centrally on 2026-09-27 after ticket 05 (`925cfe7`) and ticket 06 (`2306330`) resolution; assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.

- Resolved on 2026-09-27 after implementation commits `70ead40`, `d6e29d0` and integration merge `d24b159`. Independent spec and standards reviews rechecked connection-pinned STOP, drain, plan-change invalidation, publication withdrawal, readiness and framing with no remaining actionable findings. Grant-local refresh replay intentionally leaves existing fixed-deadline Relay Sessions running under I3. On the merged branch, `CLIPP_JS_NODE_MODULES=/Users/invine/src/js/clipp/node_modules GOPROXY=off GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./...`, the corresponding `go test -race -count=1 ./internal/relay ./internal/quota ./internal/auth`, `go vet ./...`, `go build ./...`, and `git diff --check HEAD^..HEAD` passed with Go 1.27.1 darwin/arm64 and Node 26.10.0. The JS/libp2p 3.1.2 harness verified bearer discovery, Noise Peer ID before token, reservation, a cross-account TCP circuit and 29 opaque bytes. `bash scripts/smoke-postgres.sh` passed against disposable verified-TLS, SCRAM PostgreSQL 18.6 and 17.11 and rejected 16.15; it exercised real auth/admin/quota SQL. Docker 29.8.0 was local and disposable. No push, publication or deployment occurred.
