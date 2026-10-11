# Relay logging implementation ticket proposal

Status: approved breakdown, 2026-10-11 — published in implementation.md

This proposal refines the preliminary build outlines against the accepted
[specification](spec.md), [logging contract](logging-contract.md) and
[fixed catalog](logging-catalog.json). Implementation repository: the explicitly
selected `clipp-relay` checkout. This completed tracker now lives in the relay
repository; [the run record](run.md) preserves its earlier Clipp coordination.

All slices include their behavior tests, canary checks and interpretation notes.
The final slice integrates their evidence; it does not postpone privacy or bounded
output controls. Metric families/endpoints/collectors, deployment and full
observability release qualification remain outside scope. Missing infrastructure
producers are explicitly unavailable and do not block this local logging effort.

## Proposed slices and dependencies

### 1. Log startup, readiness and bounded shutdown

**Blocked by:** None (can start immediately).

**What it delivers:** A real service start distinguishes startup validation,
listeners serving and usable publication, while readiness loss, recovery and
drain produce safe events. The same stdout path stays bounded even when the
output writer stops responding.

Proposed acceptance criteria:

- [ ] Drive existing service/lifecycle seams to observe distinct startup,
  listener, readiness/publication and stop/drain outcomes. Preserve truthful
  completed, deadline and forced outcomes; do not infer readiness from starting.
- [ ] Replace existing raw startup/error reasons with catalog-owned wording and
  safe classifications. Test synthetic secret, identity, address and arbitrary
  error canaries through both successful and failed lifecycle paths.
- [ ] Establish the shared typed event interface, field/pair validation and
  ephemeral diagnostic ID generation for later slices. Existing public
  authority, protocol, transaction and runtime semantics stay intact.
- [ ] Implement the agreed activity/per-tuple/reserved budgets, fixed limiter
  state, single non-blocking 256-record writer queue and 2 KiB row ceiling.
  Eligible loss summaries are emitted at most once per ten seconds only after
  loss; quiet intervals produce no heartbeat.
- [ ] Block stdout and prove producer completion and service shutdown remain
  bounded, including the 100 ms maximum flush wait. Verify queue-full, rate loss
  and malformed diagnostic input do not create another queue/writer/logger or
  change the business result.
- [ ] Keep meaningful lifecycle logging usable while request activity reaches
  its budget; preserve best-effort semantics when the reserved budget/output is
  also exhausted. Add concise startup/drain examples and record focused checks.

### 2. Log portal sign-in and OAuth credentials

**Blocked by:** Log startup, readiness and bounded shutdown.

**What it delivers:** Operators can locate browser sign-in, consent,
authorization-code exchange and refresh failures, and distinguish them from
successful server-side credential issuance.

Proposed acceptance criteria:

- [ ] HTTP and provider fixtures exercise login initiation, callback validation,
  exchange/verification, Portal Session issuance, authorization/consent,
  code exchange and refresh outcomes using only catalog fields/reasons.
- [ ] Each admitted HTTP request receives its own generated request correlation;
  callbacks do not reuse OAuth state, cookies, caller IDs or earlier request IDs.
  Client type is a registered fixed enum or fixed unknown category.
- [ ] Successful issuance/rotation is logged only at the existing committed or
  proven boundary. Refusal, replay, expiry, policy limit and ambiguous commit
  remain distinguishable; no row claims the browser/client received a response.
- [ ] Successful page and asset reads emit no activity rows. Safe route/method
  refusals never include the raw path, query, headers or body.
- [ ] Inject credential/identity/content/address/URL/error canaries into successful,
  refused and failed requests and provider responses. Verify no forbidden value
  reaches captured application output; expected refusals stay INFO and dependency
  degradation is WARN.
- [ ] Ship focused HTTP and real-PostgreSQL evidence for affected durable flows,
  with skipped/unavailable fixtures explicitly recorded, and provide operator
  sign-in/credential examples.

### 3. Log Relay Authentication and Relay Session lifetimes

**Blocked by:** Log startup, readiness and bounded shutdown.

**What it delivers:** An operator can follow admission, preauthentication,
authentication, renewal and the end of one physical connection without logging
the Device Identity or Relay Account.

Proposed acceptance criteria:

- [ ] Real protocol/connection fixtures drive admission/refusal, preauthentication
  expiry, authentication/renewal and Relay Session start/end. Assert exact safe
  stage and reason rather than deriving authentication success from a connection.
- [ ] Generate a separate diagnostic connection ID at its admitted lifetime;
  propagate it only where the producer has that association and remove it when
  the connection ends. Protocol request IDs are independent. Do not link a new
  connection to an earlier one through account, Peer ID or credentials.
- [ ] Report a supported transport only with actual producer evidence; use
  unknown/omitted metadata at stages that cannot supply it.
- [ ] Cover expiry, disconnect, replacement, policy invalidation, account mismatch,
  accounting/clock failure and drain without weakening the accepted
  one-session-per-Peer-ID contract or existing termination timing.
- [ ] Inject token/Peer ID/account/address and malformed-frame canaries. High-volume
  refusals obey the shared limits and emit only best-effort loss summaries.
- [ ] Ship protocol-level checks and authentication/session interpretation notes;
  success proves server-side authentication only.

### 4. Log owner and administrator account actions

**Blocked by:** Log startup, readiness and bounded shutdown.

**What it delivers:** Owner logout/revocation/deletion and administrator account or
plan changes have safe completion, refusal, conflict and uncertainty events.

Proposed acceptance criteria:

- [ ] Drive HTTP account/administrator workflows for portal-only logout,
  Account-wide Revocation, deletion acceptance and account/plan mutations.
  Authorization, Origin/CSRF, recent-auth and revision checks retain their authority.
- [ ] Log only safe action/outcome/reason enums and generated request correlation.
  Never emit actor/target identities, email, plan names, free text, raw form data,
  operation proofs or deletion credentials.
- [ ] Emit success only after commit/proof. Exercise rollback, conflict,
  unconfirmed commit and proven ambiguous-commit reconciliation without inventing
  success or duplicating a completed business operation.
- [ ] Preserve restricted transactional audit and existing account-policy/session
  effects. Operational rows are not a replacement audit history.
- [ ] Add hostile canary checks and real-PostgreSQL HTTP evidence for affected
  durable mutations. Provide owner/admin examples explaining what the result proves.

### 5. Log reservations, circuits and Rendezvous

**Blocked by:** Log Relay Authentication and Relay Session lifetimes.

**What it delivers:** Authentication success is distinguishable from reservation
refusal, circuit setup/lifetime and Rendezvous lookup/lease outcomes.

Proposed acceptance criteria:

- [ ] Real relay/protocol fixtures exercise reservation/new-versus-renewed status,
  protected HOP refusal, circuit request/open/close and Rendezvous v1/v2 outcomes.
- [ ] Preserve stock callback meanings: aggregate callbacks omit request/session
  correlation they do not supply and use unknown transport. Correlated gate
  events use existing connection evidence; do not infer a callback association.
- [ ] Include completed-circuit duration only when the stock producer supplies it.
  Log no per-byte activity, payload interpretation or exact per-circuit byte total.
- [ ] Demonstrate authentication success followed by reservation refusal as
  distinct evidence. A successful reservation/circuit never claims client
  acceptance, Direct Connection success or Clip delivery.
- [ ] Inject peer/address/content/error canaries through rejected and successful
  protocol paths; verify bounded repeated events and unchanged stock relay behavior.
- [ ] Ship protocol checks and interpretation examples for aggregate visibility
  limits and version-specific Rendezvous stages.

### 6. Log quota and accounting failure outcomes

**Blocked by:** Log Relay Authentication and Relay Session lifetimes.

**What it delivers:** Quota funding, expected exhaustion, uncertain allocation,
credit retirement and accounting failure are visible, including their observed
Relay Session consequences.

Proposed acceptance criteria:

- [ ] Drive complete Quota/Relay seams for confirmed funding, exhausted/blocked
  account refusal, database/clock failure, uncertain allocation/reconciliation,
  discarded/expired credit and accounting-reporter failure.
- [ ] Safe rows contain no account/grant/generation identifiers, Peer IDs, public
  addresses or payload data. Request/session correlation is present only where
  the caller provides a real ephemeral association.
- [ ] Preserve allocation, conservative charging, no ordinary refund and all-session
  cutoff rules. Do not report allocation as exact payload transfer, infer exact
  crash-stranded credit or add traffic counters/histograms.
- [ ] Verify known rollback and ambiguous/proven allocation outcomes with real
  PostgreSQL fixtures, and protocol-visible session closure after reporting failure.
- [ ] Apply logging limits to hot accounting paths; routine byte callbacks do not
  become a stream of successful log rows. Include hostile canaries and explanatory
  quota/accounting examples.

### 7. Log dependency and maintenance state transitions

**Blocked by:** Log startup, readiness and bounded shutdown.

**What it delivers:** Database/provider/publication/journal degradation and recovery,
cleanup/deletion backlog thresholds and diagnostic freshness produce meaningful
events while unavailable infrastructure producers remain honest.

Proposed acceptance criteria:

- [ ] Use existing dependency and maintenance seams for DB work/acquisition,
  Google token/JWKS, publication API, journal and quota-clock outcomes. Successful
  internal queries do not produce individual routine stdout rows.
- [ ] Use bounded diagnostic observations: 60-second cadence, a single in-flight
  pass with a 5-second deadline, immediate failed-sample invalidation and
  180-second staleness. Preserve the stricter quota-clock safety timing and
  maintenance ordering/cadence; diagnostic observation does not become authority.
- [ ] Log initial state, actual changes and proven recovery only. Same-state
  retry/success produces no recurring row. Log warning/critical/breach crossings
  and clearance even if observation availability stays unchanged.
- [ ] Verify accepted cleanup and pending-deletion thresholds, including a failed
  observation followed by fresh recovery and an observation aging into stale.
  Failed/stale/unavailable states carry no fabricated current value or periodic gauge.
- [ ] Declare absent recovery/backup/repository/certificate/DB infrastructure
  producers unavailable. Do not implement those producers, infer infrastructure
  health from process/pool data, or modify metric endpoints and collector behavior.
- [ ] Use existing real-PostgreSQL maintenance fixtures and controlled dependency
  doubles for failure/privacy/freshness checks. Include raw upstream/SQL/identity
  canaries and operator examples for uncertainty and threshold changes.

### 8. Qualify integrated logging and publish the operator guide

**Blocked by:** Log portal sign-in and OAuth credentials; Log owner and
administrator account actions; Log reservations, circuits and Rendezvous;
Log quota and accounting failure outcomes; Log dependency and maintenance
state transitions. (Startup and Relay Session work are transitive prerequisites.)

**What it delivers:** One locally verified logging implementation and a complete
operator guide with evidence covering cross-service journeys and their limits.

Proposed acceptance criteria:

- [ ] Exercise representative combined portal/OAuth/account/relay/quota/dependency/
  publication/drain journeys at the integrated revision and compare captured rows
  with the agreed catalog. No relevant serving path retains a raw second logger.
- [ ] Verify shared output bounds under concurrent public activity, repeated
  refusals, blocked/failed stdout and shutdown. Demonstrate that loss summaries
  are best effort, no idle heartbeat appears and the logging path cannot stall
  serving indefinitely.
- [ ] Sweep synthetic identity, credential, address, URL, header, body, content and
  arbitrary error canaries across integrated paths. Classifications are fixed;
  the implementation does not claim to sanitize arbitrary upstream text.
- [ ] Run routine and separate race profiles and required real-PostgreSQL fixtures;
  record exact revision, commands/environment and passed/failed/not-run evidence.
  Resolve required missing evidence before claiming local implementation complete.
- [ ] Publish the guide using actual final row examples, safe severity/reason
  meanings, correlation lifetime, suppression/freshness behavior and server
  visibility limits. Clearly distinguish logs from audit and future metrics.
- [ ] Record metric/collector, infrastructure producer and deployment/release
  exclusions. Do not close the accepted full observability release ticket or
  claim deployed behavior based on local fixture results.

## Proposed implementation frontier

Begin with **Log startup, readiness and bounded shutdown**. After it completes,
portal/OAuth, owner/admin, Relay Session and dependency/maintenance slices can be
taken independently. Relay Session completion unlocks reservation/circuit and
quota/accounting slices. Only leaf-completion edges gate integrated qualification.

The shared logger is introduced through the first working lifecycle slice rather
than as a separate infrastructure-only ticket. Each later slice includes main
process wiring and its own relevant safety tests; file overlap alone does not
create a blocking relationship.

## Publication plan

Keep the resolved decision tickets and parent map/specification intact. The human approved the granularity and blocking edges on 2026-10-11; publication is recorded in [the implementation graph](implementation.md).
Refine matching preliminary outlines, preserve a superseded outline's history
with a clear pointer, and provide an explicit build-ticket index so orchestration
cannot confuse decision history or retired outlines with executable work.
