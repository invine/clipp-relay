# Ticket 34 — single-session transport fallback evidence

Date: 2026-10-05. Status: integrated and verified locally.

## Accepted behavior

Clipp ADR-0012 retains one effective authenticated connection per Relay Configuration.
Electron tries TCP → WSS → WebRTC Direct; Android/extension try WSS → WebRTC Direct.
This is a framing/setup overhead preference, not a measured performance claim.
Selection covers verified dial, authentication and reservation. Transport-local
failure advances; authoritative authentication/quota/session refusals and retry
hints retain their semantics. Rendezvous failure repairs the retained owner.
Healthy connections remain selected; renewal failure retains the original expiry.

## Integration and preservation

- Approved client source baseline `8ccc67c`; final isolated source `510c1be`.
- Client local main implementation commits: `ff15e4c`, `d463aa8`, `c7ce26f`,
  `f0b3a41`, `c514e9e`. Final verified code snapshot: `c514e9e`.
- Relay source baseline `2bc9271`; final isolated source `6f3b200`.
  Canonical documentation advanced independently to `241a4c7` before integration.
- Relay local main implementation commits: `7f75ed1`, `4a88826`, `a29f581`.
  Final verified code snapshot: `a29f581`.
- Relay server/rendezvous/reporter production files match the original
  single-session code at `d0f761f`; atomic same-Peer-ID replacement is restored.
- Both `codex/managed-relay-concurrency-candidate-20261005` branches remain.
  Existing worktrees and uncommitted experimental client changes are preserved.
  Unrelated runtime qualification and runner fixes remain in main.

## TDD and independent review

Public controller/host/adapter seams were used as approved. RED→GREEN cases cover
preferred-route reservation failure, refusal/hints, all-family black-hole
exhaustion, recovery after loss, healthy selection, late dial/callback ownership,
awaited removal/stop cleanup, fractional/early retry timers, initial Rendezvous
failure lifetime, optional TLS SNI WSS eligibility and TCP-before-SNI ordering.

Review exposed lifetime/readiness/retry overlap defects. Authentication and
Rendezvous now have independent retry ownership; successful renewal cannot claim
Ready before registration. Tests cover renewal success/failure while registration
is pending, original expiry after renewal refusal, persistent RV degradation,
RV completion preserving authentication retries, and both orders of overlapping
renewal/RV failure. Numerical limits are unchanged.

Standards: one duplicate extension priority judgment fixed; no remaining findings.
Spec: lifetime, SNI and renewal/RV readiness/retry findings fixed; no remaining
findings. Independent final Spec probes recovered both overlap orders and reran
five affected suites (86 tests). Reviews used fixed nonempty source ranges and
subsequent committed deltas; server review had no findings.

## Canonical verification — passed

- `npm run check`: all runtime/shared type checks; **74 suites / 627 tests**.
- `npm --workspace apps/electron run build`.
- `npm --workspace apps/android run build`.
- `npm --workspace apps/extension run build`.
- `GOPROXY=off GOCACHE=/private/tmp/clipp-go-cache go vet ./...`.
- `go test -race -count=1 -v ./...` with `GOPROXY=off`, the same cache,
  `CLIPP_JS_NODE_MODULES=/Users/invine/src/js/clipp/node_modules`,
  `CLIPP_JS_TRANSPORT_HARNESS=/Users/invine/src/js/clipp/tests/harness/managedRelayFallbackInterop.ts`,
  and `CLIPP_CLIENT_SOURCE_ROOT=/Users/invine/src/js/clipp`.

All eight Go packages passed; the relay package took 83.786s. The composed real
Go/JS fallback fixture passed in 5.93s: TCP when usable, WSS after TCP AUTH reset,
and WebRTC Direct after TCP/WSS AUTH resets. Both devices attempted required
families after verified identity, retained exactly one physical relay connection,
completed an opaque Circuit Relay transfer with both accounts charged, released
sessions to zero and exited normally. Real discovery, AUTH, SignedPeerRecord,
Rendezvous and Circuit Relay protocols were exercised using synthetic owned
loopback fixtures, not external credentials or user profiles.

Versions: canonical client Node 24.16.0/npm 11.13.0; source checks and Go-launched
JS fixtures Node 26.10.0/npm 11.19.1; Go 1.27.1 darwin/arm64. Commands used scoped
`caffeinate -is` for owned long tests. Android/extension builds retain their
existing bundle-size warnings. The source full checks also passed.

Private logs, RED failures, review history and a version/ref manifest are retained
under the Clipp project's `.local/managed-relay/artifacts/frontier-20261004/fallback/`.
Earlier concurrency failures and experiments remain separate evidence.

## Not run / remaining frontier

SQL/provider fixtures were not configured for this final Go run and were skipped;
no SQL/provider implementation changed. This is not new PostgreSQL, OCI journal,
Google login, native runtime, forced live clip or cluster acceptance evidence.
Tickets 13–15 and 18 remain claimed with their existing external/runtime gates.
Ticket 35 is superseded (`wontfix`), not resolved by tests. Ticket 33 stays deferred.
The dependency scan found no newly unblocked unclaimed implementation ticket.
No push, publication, deployment, shared-cluster mutation or external load occurred.
