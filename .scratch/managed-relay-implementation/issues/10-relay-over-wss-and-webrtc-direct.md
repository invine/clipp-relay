# 10: Relay over WSS and WebRTC Direct

**What to build:** Clients relay traffic through WSS or WebRTC Direct with the same connection authentication and account enforcement as TCP.

**Blocked by:** [07: Relay authenticated TCP traffic](07-relay-authenticated-tcp-traffic.md).

**Status:** resolved

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I1, I4, I11, I12.

## Contract and scope

Add required transports without changing relay wire or bypassing auth. WSS
public TLS/exact hostname and separate internal WebSocket listener; never mount
portal/private operations on WSS. WebRTC Direct ephemeral certificate/certhash per
process, embedded Noise remote identity verified before token. All supported
addresses complete/current Peer ID; TLS/origin/address config validated.
Inbound-only relay; STOP on existing authenticated connections, no AutoNAT/dial-
back/UPnP/relay-side hole punching. Keep direct connectivity in clients unchanged.

WSS negotiation10s; stock WebRTC setup10s/128 pending, no test-only knobs sold as
configuration. Preserve session/reservation/circuit quotas and full fixed RM profile.
Relay service streams24576/12288/12288,128MiB, peer32/32/32,2MiB;
HOP12288/12288/0,64MiB, peer32/32/0,1MiB;
STOP12000/0/12000,64MiB, peer32/0/32,1MiB.
Identify service4096/2048/2048,64MiB, peer8/4/4,1MiB; each id/id-push protocol
2048/1024/1024,32MiB, peer4/2/2,512KiB.
Ping service/protocol1024/1024/512,16MiB, peer4/4/2,256KiB.
Auth service/protocol512/512/0,8MiB, peer8/8/0,256KiB.
All service/protocol connection+FD fields zero-block. System/peer/connection
scopes retain the TCP ticket values. Rendezvous scopes, if enabled by the independent
[Register and find peers](08-register-and-find-peers.md) slice, retain its values;
this transport slice does not require Rendezvous implementation. No hidden built-in defaults.
Account custom buffers and assert allowed protocol inventory.

Inspected WebRTC reservation2,631,680 bytes at medium priority means at most121
reservations under512MiB even before other use; not RSS proof. Yamux windows also
consume memory. Keep2GiB container/1536MiB Go/512MiB RM and5,000-session goal,
no undercount/fork/forced transport preference to conceal constraint.

## Acceptance criteria

- [x] Force WSS and WebRTC Direct separately with real libp2p clients, reserving and transferring actual relayed traffic in both directions; no TCP fallback false pass.
- [x] Auth initial/renewal/expiry/replacement and same/cross-account accounting outcomes match TCP, including buffered-tail limitations.
- [x] Wrong WSS TLS identity and stale/wrong WebRTC certhash/Peer ID fail before token disclosure; restart rotates key/certificate.
- [x] Private/account routes are absent on WSS and HTTP portal idle/write deadlines do not kill upgraded libp2p sessions.
- [x] RM resolved values/service tagging, descriptor/memory limits and transport setup bounds are asserted; pressure rejects visibly without hidden IP cap or allowlist bypass.
- [x] Network harness proves no new outbound peer dial, while STOP on accepted sessions works; actual OCI policy qualification remains separate.

## Demonstration

Run the authenticated two-client circuit probe with TCP disabled, first WSS-only then WebRTC-Direct-only, and show charging and account-wide cutoff for each.

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

- Claimed centrally on 2026-09-27 after ticket 07 resolution (`49eed87`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.

- Resolved centrally on 2026-09-27 after TDD implementation (`d78c9b2`, `2f24af2`, `433d0c6`), independent specification and standards reviews with fixes and clean final rechecks, and combined integration merge (`49083bf`). Go 1.27.1 on darwin/arm64: `go test -count=1 ./...`, `go test -race -count=1 ./internal/relay ./internal/config`, `go vet ./...`, `go build ./...`, `gofmt -l cmd internal`, and `git diff --check` passed on the merged ticket 08+09+10 state with local loopback and offline cache. The integration test expectation was updated for ticket 09's guarded credential recheck; failed TLS/Peer ID connects still produce zero token validations. Forced WSS and WebRTC Direct wire tests cover authentication, renewal/expiry/replacement, bidirectional opaque circuits, account charging/cutoff, and inbound-only behavior. A local exact-name TLS proxy models WSS ingress, and a real incomplete UDP/STUN WebRTC setup releases stock candidate resources at about 10s; exported 128-pending and RM memory/FD scope values are asserted, and memory pressure visibly rejects then recovers. Actual FD saturation, OCI ingress policy and browser/mobile interoperability were not run; those belong to later qualification/runtime tickets. No schema change required another PostgreSQL smoke run. No push, publication, deployment or external load occurred.
