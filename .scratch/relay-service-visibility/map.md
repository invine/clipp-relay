# Relay service logging

Label: wayfinder:map

## Destination

Locally implement and verify privacy-safe structured logging for the portal, OAuth, relay activity, dependencies, publication, and lifecycle. Give operators a concise guide to what the log events establish and where the relay has no visibility.

## Notes

- This map deliberately carries execution through local implementation and checks, as requested on 2026-10-11. Charting this map does not resolve a ticket. Claim and resolve at most one ticket per later session, except research tickets.
- The specification and eight-slice graph were approved, implemented and locally verified on 2026-10-11. The [run record](run.md) preserves the completed ticket evidence and qualification limits.
- For grilling tickets, consult the `grilling` and `domain-modeling` skills together. Use the `prototype` skill for the sample signal contract. Record newly settled domain terms in the relay glossary only if a genuine term needs defining; do not reopen accepted privacy or identity policy by implication.
- Implementation and this local Markdown issue tracker now live in the `clipp-relay` repository. Use its `AGENTS.md`, `GLOSSARY.md`, `docs/agents/operations.md`, accepted service specification, and existing observability ticket before changing code. Preserve unrelated work in both repositories.
- The accepted relay privacy boundary governs this effort: stdout events use ephemeral request or Relay Session correlation and safe enumerated outcomes; no stable Relay Account, External Identity, email, Peer ID, public address, credential, or content. Restricted audit is separate from operational diagnostics. Do not log raw URL/query, headers, body, SQL, or arbitrary upstream errors.
- The operator wants meaningful success and failure outcomes for key portal flows and the whole relay service, rather than a row for every page or asset request. Include privacy tests with injected canaries and a signal interpretation guide.
- The relay's `.scratch/relay-observability/design.md` is an unfinished earlier interview about ordinary connection diagnostics. Treat its dated source findings as leads to recheck, not as a completed decision. The relay's `.scratch/managed-relay-implementation/issues/26-observe-failures-and-expose-alert-signals.md` is the accepted full observability implementation contract. This map advances its logging portion but does not mark that ticket resolved or waive its dependencies and release evidence.

## Decisions so far

<!-- Closed ticket titles link to their answers; each answer lives only in its ticket. -->

- [Decide the relay's operator diagnostic journeys](issues/01-decide-operator-diagnostic-journeys.md#answer): separate stage outcomes, activity/lifecycle stdout without a heartbeat, severity by operational impact, and explicit unavailable states for absent producers.
- [Prototype the structured logging contract](issues/02-prototype-the-signal-contract.md#answer): fixed log fields/catalog, accepted bounded writer and freshness policy; metric work excluded, with a durable logging-only prototype and completed specification.
- [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md#answer): integrated fixed-catalog bounded writer and lifecycle rows, with blocked-output and privacy checks at relay integration `5fe74ef2a3a089100a5bb66d7e6f97bf2b63ef2e`.
- [Log Relay Authentication and Relay Session lifetimes](issues/05-instrument-relay-journeys.md#answer): integrated protocol-level stage and session end rows with ephemeral connection correlation, preserving one-session-per-Peer-ID at relay integration `d9caf928e10f10cc8e85923f23e9165b8bc99c17`.
- [Log reservations, circuits and Rendezvous](issues/08-log-reservations-circuits-and-rendezvous.md#answer): integrated truthful stock aggregate callbacks and versioned Rendezvous rows, with measured circuit-close duration and no inferred correlation at relay integration `399bd60e5f59160bd236f8345e5ff679a965af18`.
- [Log portal sign-in and OAuth credentials](issues/04-instrument-portal-and-oauth.md#answer): integrated fixed, private HTTP/provider outcomes and verified committed credential/lost-ack behavior with real PostgreSQL 18 at relay integration `df06649a227a3ff7e0f462c915f239dad26d52f7`.
- [Log quota and accounting failure outcomes](issues/09-log-quota-and-accounting-outcomes.md#answer): integrated funding/refusal, uncertain credit proof, retirement and conservative reporter cutoff with full real PostgreSQL 18 quota evidence at relay integration `8234b07b00b7f0cee380e056e2158ccb59b8e734`.
- [Log dependency and maintenance state transitions](issues/10-log-dependency-and-maintenance-transitions.md#answer): integrated qualified dependency failures/recovery and bounded maintenance freshness/threshold rows, with real PostgreSQL 18 evidence at relay integration `bfc590c4b4139ac3234d8baea80fc66e6d119e98`.
- [Log owner and administrator account actions](issues/07-log-owner-and-administrator-actions.md#answer): integrated private owner/admin outcomes and per-deletion worker transitions with real PostgreSQL 18 HTTP evidence at relay integration `71b02629fedfe34dca7fa457f2afed94a4379a39`.
- [Qualify integrated logging and publish the operator guide](issues/11-qualify-integrated-logging-and-operator-guide.md#answer): combined catalog, privacy, output-bounds and five real PostgreSQL 18 fixture checks, with actual local row examples and explicit visibility limits at relay integration `4c75deca275eaa6df841f617165e0f251184899b`.
- [Final two-axis review](run.md#final-two-axis-review): the JWKS classification and initially unavailable process signals were corrected at relay integration `e7bb2fcc22f40f516767909958661fda9cb5c7a0`.

## Not yet specified

<!-- The missing-producer boundary is settled in the diagnostic-journeys answer;
its exact representation belongs to the signal-contract ticket. No further fog
has been identified at this checkpoint. -->

## Out of scope

- New or changed metric endpoints, metric families, scrape snapshots and metric-collector integration. The human explicitly removed metric work from this effort on 2026-10-11; the full observability contract retains that work for later.
- Cluster rollout, production publication, live qualification, operator-owned monitor or notification provisioning, and closure of the full observability release ticket. Those retain their separate gates.
- Durable account- or device-linked traffic histories, routine page/asset access logs, and automatic client telemetry.
- Client-observed Direct Connection results and Clip-delivery claims; the relay cannot establish either from its own signals.
