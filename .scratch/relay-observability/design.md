# Relay connection diagnostics

Status: interview in progress; normal client activity is the settled goal.
Privacy policy remains accepted. Direct Connection visibility and detailed event
policy remain pending.

## Goal

Understand normal client activity: successful connections, failed attempts,
failure reasons, and recovery, without requiring user complaints or reproduction.
Investigate whether Direct Connection failures can also be observed. The
initiating observation was one stdout row:
`service starting`, with `schema_revision: 10`. That row proves neither readiness
nor successful relay use.

## Verified local facts — 2026-10-04

Inspected canonical Go checkout at `d41941d`. These are source findings, not a
fresh verification of the deployed image, live metrics, or operator monitoring.

- `cmd/clipp-relay/main.go` creates the stdout JSON logger. `service starting`
  precedes relay construction, address publication, and HTTP serving. The logger
  is not supplied to request, authentication, quota, publication, or relay paths.
- `internal/service/service.go` exposes private `/livez`, `/readyz`, and
  `/metrics`. Current metrics cover readiness, Rendezvous protocol-version
  request counts, deletion, and cleanup; they do not provide the full accepted
  operational contract.
- Metrics currently synchronously query PostgreSQL for deletion/cleanup through
  `internal/auth/deletion.go:670` and `internal/auth/cleanup.go:17`. This conflicts
  with ticket 26's bounded local-snapshot scrape requirement. Readiness is a
  publication/routing signal, not a successful dependency or relay-journey test.
- `internal/relay/circuits.go` counts active circuits internally but discards
  stock tracer reservation outcomes, connection-request status, duration, and
  byte events. Application outcomes cannot be inferred from opaque relay bytes.
- `internal/relay/server.go:206` disables Resource Manager metrics. Publication
  sync/watch failures, auth/admission failures, unauthenticated connection expiry,
  quota-reporting failure, HTTP overload, and drain results lack operational
  outcome signals. Raising verbosity cannot recover instrumentation that has no
  call sites.
- [Implementation ticket 26](../managed-relay-implementation/issues/26-observe-failures-and-expose-alert-signals.md)
  is `ready-for-agent`, with observability implementation not started. Its
  acceptance checks are open.
- The chart configures private health probes and requires monitoring before
  serving. No configured alert destination, scrape/alert rules, or external
  synthetic monitor was found in the inspected checkout. Actual operator
  configuration remains unverified.
- The accepted [service specification](../managed-relay-service/spec.md), I6 and
  I12, requires bounded aggregate metrics and redacted stdout transitions.
  Application logs permit ephemeral request/session IDs and safe outcomes, not
  account identity, email, Peer IDs, public addresses, credentials, or content.
  Metrics must use bounded enums. Already-detected critical conditions must
  reach an operator-owned destination outside the process within five minutes.
- The client Android background-continuity specification currently excludes
  automatic telemetry and remote collection. Real-user telemetry would need an
  explicit scope/policy decision rather than being introduced implicitly.
- The saved Android reservation diagnosis is a concrete blind-spot example:
  server authentication/reservation succeeded, but a minified client listener
  check rejected the success and showed `relay_reservation_failed`. Server
  counters alone could not establish the client outcome. The handoff records
  the client fix and a Ready restart, but no Clip-transfer acceptance.

## Decision tree

Settled after clarification — 2026-10-04:

1. Cover actual ordinary client use through relay logs and metrics. The initial
   synthetic-device and notification proposals were based on a misunderstood
   goal and are superseded. No external monitor, synthetic deployment, or
   notification destination is part of this design request.
2. Privacy: ephemeral request/session correlation only. No stable account/device
   identifiers, email, IP addresses, Peer IDs, credentials, or Clip content in
   application diagnostics. Automatic client reporting was initially excluded;
   reconsider it explicitly only if needed for the newly requested Direct
   Connection visibility.
3. Desired evidence: successful and failed connections, failing stage, safe
   reason, frequency, delay, and recovery. Server-side evidence must not claim
   successful device-to-device delivery or client acceptance that it cannot see.

Dependent branches, to interview after their prerequisites settle:

- Direct Connection visibility → relay-only evidence versus explicitly scoped
  client outcome reporting, observation gaps, retry/upload behavior, and
  distinction between failed direct upgrade and loss of usable connectivity.
- Normal-use evidence → connection stages, transport distinctions, success/
  failure events, bounded reason vocabulary, correlated lifetime, periodic
  summaries, and repeat-event limits.
- Identity policy → event fields, correlation lifetime, log/metric retention,
  access, cardinality/volume budgets, and synthetic-secret redaction acceptance.
- Operator inspection → stdout examples, private metric semantics, and a short
  diagnosis guide; no new monitoring infrastructure assumed.
- Settled signal contract → module instrumentation, external behavior tests,
  fault matrix, implementation tickets, and final shared-understanding check.

## Direction agreed in principle; detailed choices pending

Combine aggregate outcome/latency/saturation metrics with bounded structured
success, failure, and lifecycle events from real traffic. A readiness probe alone
cannot establish successful Relay Authentication, reservation, or device-side
connection acceptance. A relay observes Relayed Traffic, not application
delivery or all Direct Connection attempts.

Reference: [libp2p Circuit Relay documentation](https://github.com/libp2p/docs/blob/master/content/concepts/nat/circuit-relay.md).

No runtime changes, production checks, provisioning, notifications, or deployment
have been performed by this interview. Record settled terms in `CONTEXT.md` only
if project-specific language needs clarification; create an ADR only for a
settled architectural trade-off that merits one.
