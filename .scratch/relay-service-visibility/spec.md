# Relay service logging

Status: ready-for-agent

## Problem Statement

The relay currently emits a startup row without explaining ordinary portal and
relay activity. An operator cannot distinguish successful authentication from a
later reservation failure, identify the failing stage, recognize repeated refusals,
or observe dependency degradation and recovery from the application diagnostics.
The startup row also precedes serving and readiness, so it cannot establish that
the service became usable.

## Solution

Provide bounded structured stdout events for meaningful lifecycle and activity outcomes
throughout the portal, OAuth and relay service. Show distinct stages and safe failure
categories, retain honest uncertainty, and explain each signal in an operator
guide. Quiet periods produce no recurring stdout status row.

The local implementation covers observations the current serving process can
produce. Relevant diagnostic observation states without a real producer are explicitly unavailable. Actual metric measurements, future infrastructure producers and production qualification remain governed by their existing accepted work.

## User Stories

1. As an operator, I want startup, serving and readiness to be distinct, so that a startup row does not imply the service is usable.
2. As an operator, I want publication loss and verified recovery to be visible, so that I can diagnose unavailable discovery and routing.
3. As an operator, I want stop and drain outcomes, so that I can distinguish a completed drain from a deadline or forced stop.
4. As an operator, I want portal sign-in stages and outcomes, so that I can locate redirect, provider or session-issuance failures.
5. As an operator, I want Portal Session issuance and logout outcomes, so that I can observe server-side session changes.
6. As an operator, I want OAuth authorization, consent and code-exchange outcomes, so that I can locate client authorization failures.
7. As an operator, I want refresh, expiry and replay-refusal outcomes, so that I can diagnose credential failures without seeing credentials.
8. As an operator, I want owner revocation and deletion outcomes, so that I can distinguish accepted work, pending work, retries and completion.
9. As an operator, I want safe administrator action outcomes, so that I can identify rejection, revision conflict, completion and uncertainty.
10. As an operator, I want discovery outcomes, so that I can distinguish authorization refusal, drain and unavailable publication.
11. As an operator, I want connection admission and preauthentication outcomes, so that I can see failures before a Relay Session exists.
12. As an operator, I want Relay Authentication and renewal outcomes, so that authentication is distinguishable from later protected operations.
13. As an operator, I want Relay Session start and end categories, so that expiry, policy invalidation and drain are distinguishable.
14. As an operator, I want reservation and renewal outcomes, so that authenticated connections that cannot reserve capacity are visible.
15. As an operator, I want Rendezvous operation outcomes and protocol-version context, so that discovery-stage failures can be separated from relay traffic.
16. As an operator, I want circuit request, open, close and per-event duration observations, so that I can diagnose forwarding setup and lifetime.
17. As an operator, I want funding and quota-refusal outcomes, so that expected account limits are distinguishable from dependency failures.
18. As an operator, I want quota allocation and accounting outcomes to be described accurately, so that a funding success is not mistaken for exact payload delivery.
19. As an operator, I want accounting-reporter failures to be visible, so that I can diagnose termination caused by conservative accounting failures.
20. As an operator, I want DB, provider, publication API and journal failure/recovery outcomes and per-operation duration, so that dependency degradation and recovery are visible.
21. As an operator, I want cleanup and pending-deletion observations, so that backlog and accepted threshold breaches can be recognized.
22. As an operator, I want missing and stale observations to be explicit, so that absence is not mistaken for health or a measured zero.
23. As an operator, I want best-effort diagnostic-loss summaries, so that I can tell when logging omitted individual outcomes.
24. As an operator, I want repeated high-volume events to be limited, so that diagnostics do not overwhelm the service or stdout.
25. As an operator, I want quiet periods to stay quiet, so that stdout contains meaningful lifecycle and activity events.
26. As an operator, I want severity to reflect operational impact, so that ordinary refusals are distinguishable from degradation and fatal failure.
27. As an account owner, I want diagnostics to exclude identities, credentials and content, so that operation of the relay does not create a traffic history linked to me or my devices.
28. As an operator, I want serving paths and shutdown to remain bounded when stdout is blocked, so that logging cannot stall the service.
29. As an operator, I want examples and visibility limits in the guide, so that relay-side success is not mistaken for client acceptance or Clip delivery.

## Implementation Decisions

- The [logging contract](logging-contract.md) and [fixed logging catalog](logging-catalog.json) are normative parts of this specification. They define exact fields, event/outcome/reason combinations, finite rate-limit state and freshness semantics.
- Keep the existing single serving process, stock Circuit Relay behavior,
  one-session-per-Peer-ID contract, and account/device identity separation.
- Use one structured application logging path to stdout. Operational events are distinct from restricted transactional audit.
- Instrument committed or proven outcomes at the existing business boundary.
  A submitted request, redirect or started operation is not a completed mutation.
  Preserve ambiguous results until the existing flow can prove the result.
- Observe the complete journey catalog in the linked decision. Distinguish
  connection admission, Relay Authentication, reservations, and circuits. Preserve
  aggregate-only producer semantics where safe correlation is unavailable.
- Permit only ephemeral request or Relay Session correlation and safe fixed
  outcome categories. Exclude stable Relay Account and External Identity values,
  emails, Peer IDs, addresses, credentials, content, raw request material, SQL
  and arbitrary upstream errors. The fixed event catalog prevents arbitrary fields or event categories.
- Use INFO for expected refusals, WARN for dependency degradation and service
  overload, and ERROR for fatal failures or broken invariants.
- Emit meaningful successful activity and lifecycle events. Successful routine
  page/asset fetches and a periodic stdout heartbeat are excluded.
- Activity output is limited to 20 rows/sec, burst 40; each fixed tuple has a
  1 row/sec, burst 5 limit. Lifecycle/observation transitions, ERROR and diagnostic
  loss summaries use a separate 4 row/sec, burst 8 reserved budget. Loss summaries
  appear at most once per 10 seconds and only after loss.
- One stdout writer has a non-blocking 256-record queue and a 2 KiB maximum row.
  Producers never wait for stdout. Shutdown allows at most 100 ms to flush.
- Observe existing diagnostic maintenance states on a 60-second cadence with a
  5-second deadline. A failed sample invalidates the current observation immediately;
  a successful observation older than 180 seconds is stale. Preserve the existing
  stricter quota-clock safety timing. Log state and accepted threshold transitions,
  with no recurring gauge or snapshot row.
- Keep only bounded process-local logger state for rate limits and diagnostic-loss summaries. Logging provides no exact, durable traffic-frequency store. Suppression, output failure or process exit can erase individual outcomes and pending summaries.
- Do not add metric families, histogram/traffic counters, metric scrape snapshots,
  endpoint changes or collector integration in this effort. Those belong to the
  accepted broader observability work.
- Observe current DB/pool and relay-process behavior as such. Do not describe
  these measurements as DB host, PVC, backup, WAL or provider infrastructure health.
- Represent absent producers as unavailable, not-yet-obtained observations as
  unknown, failed attempts as failed, and expired observations as stale. Never
  present missing evidence as healthy or measured zero. Preserve accepted
  maintenance and recovery threshold meanings.
- Use current serving-process producers and bounded diagnostic observations for existing maintenance states. Actual recovery, certificate and
  infrastructure producer implementations remain in their accepted work.
- Server observations cannot establish Direct Connection results, browser/client
  receipt, endpoint acceptance, Clip delivery or identity-linked client recovery
  across different connections.
- Make the interpretation guide describe event meanings, limits, suppression,
  uncertainty and the remaining observability release gates.

## Testing Decisions

- Prefer existing HTTP, libp2p and complete Account/Quota/Relay behavior seams.
  Tests assert emitted rows for externally driven successful
  and failed journeys, rather than mirroring logging helper internals.
- Follow existing portal/OAuth and relay protocol fixtures. SQL-backed mutation,
  cleanup and diagnostic observation correctness requires the existing real PostgreSQL fixtures;
  record those results separately from routine checks that may skip them.
- Inject synthetic identity, credential, URL, header, body, content and arbitrary
  error canaries. Assert they never reach stdout or unauthorized retained
  diagnostics, including refusal and dependency-error paths.
- Verify distinct authentication/reservation/circuit outcomes and committed,
  rejected and uncertain mutation outcomes. Test the no-heartbeat and no-successful
  page/asset-row requirements.
- Verify unavailable and stale observations without fabricating a producer or
  external measurement. Exercise accepted threshold transitions with controlled
  observations and distinguish fixtures from live infrastructure evidence.
- Verify the finite event catalog, diagnostic storage/output limits and loss summaries under repeated events and concurrent load. Block stdout and prove producers, serving requests and shutdown do not wait indefinitely. Unavailable and stale states must not generate periodic gauge rows.
- Run the repository's routine and separate race verification profiles for the
  integrated implementation. Record exact revision, environment, commands and
  passed/failed/not-run evidence. Local checks do not establish deployed behavior.

## Out of Scope

- Cluster rollout, publication, live or native qualification, and production
  observability-release completion.
- New or changed metric endpoints, metric families, scrape snapshots and metric-collector integration.
- Monitor/notification provisioning, an in-process paging service, and a required
  Prometheus installation.
- Actual backup/recovery, certificate-convergence or DB/PVC infrastructure
  producers that belong to unfinished accepted work.
- Stable identity-bearing traffic histories, automatic client telemetry, and
  Direct Connection or Clip-delivery claims from relay-side observations.

## Further Notes

The [operator journey decision](issues/01-decide-operator-diagnostic-journeys.md#answer)
is settled. The [source inventory](source-inventory.md) records the inspected source
baseline and distinguishes available hooks from absent producers.

The [structured logging contract decision](issues/02-prototype-the-signal-contract.md#answer)
records the prototype review, the narrowed logging-only scope, accepted budgets,
freshness policy and durable prototype source. No further signal-design decision
is pending at this checkpoint.

The specification is ready to be split into implementation tickets. The existing
execution issues are preliminary outlines, not an approved implementation task
graph. Refine them through `to-tickets` before running `implement-spec`.
