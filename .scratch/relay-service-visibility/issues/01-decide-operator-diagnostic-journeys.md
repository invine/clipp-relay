# Decide the relay's operator diagnostic journeys

Type: grilling
Status: resolved
Assignee: Codex (this chat)
Blocked by: none

## Question

For normal use and failures across the portal, OAuth, Relay Authentication, admission, reservation, circuits, quota, publication, dependency health, and drain, which transitions must an operator see? For each journey, decide what counts as a meaningful success, failure, recovery, and safe reason, and state what server-side evidence cannot prove about a client or Clip delivery. Reconcile the unfinished relay connection-diagnostics draft with the user's whole-service scope and the accepted observability contract.

## Comments

The user chose whole-service coverage, meaningful outcome events rather than per-page request logs, accepted aggregate metrics, and local implementation plus checks. Preserve the accepted privacy rules and distinguish current source findings from deployed-image evidence.

- Scope amendment on 2026-10-11: while reviewing the next prototype, the human
  explicitly removed metric endpoint/collector work from this effort. The metric
  portions of the historical answer below are superseded by that narrower scope;
  stage coverage, no heartbeat, severity and freshness choices remain accepted.
  See [Prototype the structured logging contract](02-prototype-the-signal-contract.md).

- 2026-10-11: Claimed in this chat. Current source findings and missing producer
  boundaries are recorded in [Relay observability source inventory](../source-inventory.md).
  The human round covered stage visibility, quiet-period behavior, severity,
  and the boundary around absent infrastructure producers.

## Answer

Resolved with the human on 2026-10-11. Cover successful Relay Authentication,
session start/end, reservation outcomes and circuit start/end as distinct stages,
alongside portal/OAuth actions, quota refusals and service lifecycle. Aggregate
metrics count every observable outcome. Logs limit repeated events from busy
paths; the exact budget and suppression contract is the next decision.

### Operator journeys

| Journey | Required evidence | Meaning of success and failure |
| --- | --- | --- |
| Service lifecycle | Startup validation, listener serving, publication/readiness transitions, stop request, drain and exit outcome | Starting is distinct from serving and being ready. A drain may complete, reach its deadline, or be forced. |
| Portal login and sessions | Login initiation, provider exchange and identity verification outcomes, Portal Session issuance and logout | Issuance establishes server-side session creation; it does not establish browser acceptance of the cookie. |
| OAuth authorization and credentials | Authorization validation, consent outcome, code issuance/exchange, refresh, expired/replayed credential refusal | A committed issuance or rotation is distinct from confirmed client receipt. Ambiguous commit remains uncertain unless the existing flow proves its result. |
| Owner and administrator actions | Revocation, deletion accepted/pending/completed/retry, account/plan actions, access rejection, revision conflict and commit uncertainty | Log successful mutations only at their existing committed/proven outcome boundary. Restricted transactional audit remains independent. |
| Discovery and publication | Discovery accepted/refused, publication usable/stale/missing/invalid, API failure and verified recovery | Published/ready means usable routing evidence, not a successful client journey. Recovery requires fresh valid evidence. |
| Connection and Relay Authentication | Admission/refusal, preauthentication expiry, authentication/renewal, replacement, mismatch and policy ceilings | A successful authenticated physical connection is distinct from permission for a later reservation or circuit. Preserve the accepted one-session-per-Peer-ID behavior. |
| Relay Sessions | Started, renewed, ended; fixed safe termination categories for expiry, disconnect, policy invalidation and drain | Use an ephemeral lifetime association only where the producer actually supplies it. Do not link a later connection to an earlier failed client by identity. |
| Reservations, Rendezvous and circuits | New/renewed reservation status, HOP refusal, Rendezvous outcomes/version, circuit request/open/close/duration | Preserve stock producer semantics. Aggregate-only callbacks remain aggregate evidence. Circuit acceptance and opaque byte forwarding do not prove endpoint acceptance or Clip delivery. |
| Quota and accounting | Funding accepted/exhausted/unavailable, reconciliation, discarded/expired credit, charged and unattributed traffic, reporter failure | Quota Committed and Charged Relay Traffic remain distinct. Do not report exact application payload or per-circuit overshoot. |
| Dependencies and maintenance | DB work and pool outcomes/latency, Google token/JWKS, publication API, deletion journal, cleanup/deletion backlog and quota clock | Observe outcome, latency, degradation and later recovery with fixed safe categories. Process/pool observations do not establish infrastructure health. |

### Quiet periods and severity

- Emit lifecycle and activity events only. The human explicitly declined a
  periodic aggregate stdout status row. An idle service can be quiet.
- Background sampling for private metric snapshots may still run on its agreed
  bounded schedule. Sampling must not create a recurring stdout heartbeat.
- Expected refusals, such as invalid credentials, exhausted quota and session
  limits, use INFO events or bounded summaries. Dependency degradation and
  service overload use WARN. Fatal failures and broken invariants use ERROR.
- Routine page and asset fetches do not produce successful activity rows.
  Relevant failures still belong to the safe failure contract.

### Completion and missing observations

Implement the current serving-process signal paths and expose absent
recovery/backup, certificate-convergence and DB/PVC infrastructure observations
explicitly as unavailable. Their real producer implementations remain attached
to the accepted recovery and certificate work. Missing observations must never
appear as healthy or measured zero. Distinguish unavailable producers from
stale/failed observations of a producer that does exist.

This boundary allows the local logging/metrics improvement to complete without
claiming that the full observability release ticket or its producer dependencies
are resolved. Local tests cover successful and failed current journeys and the
unavailable-state behavior. Existing release, live qualification, monitoring and
deployment gates remain outside this effort.

### Existing policy and remaining contract

Preserve ephemeral request/Relay Session correlation, fixed safe outcome
categories and bounded metric labels. Never include stable identities,
credentials, content, addresses, raw request material or arbitrary error text.
Server observations do not establish Direct Connection results, client receipt,
Clip delivery, or recovery of a specific client across different connections.

The next ticket fixes event/metric names, safe reason vocabulary, correlation
lifetime, numeric volume/cardinality bounds, stale-snapshot rules, and concrete
operator examples. Those details are not silently settled by this coverage
decision. Source evidence is linked in Comments above.
