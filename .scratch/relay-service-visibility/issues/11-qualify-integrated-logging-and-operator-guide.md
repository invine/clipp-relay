# 11: Qualify integrated logging and publish the operator guide

**What to build:** One locally verified logging implementation and a complete
operator guide with evidence covering cross-service journeys and their limits.

**Blocked by:** [4: Log portal sign-in and OAuth credentials](04-instrument-portal-and-oauth.md); [7: Log owner and administrator account actions](07-log-owner-and-administrator-actions.md); [8: Log reservations, circuits and Rendezvous](08-log-reservations-circuits-and-rendezvous.md); [9: Log quota and accounting failure outcomes](09-log-quota-and-accounting-outcomes.md); [10: Log dependency and maintenance state transitions](10-log-dependency-and-maintenance-transitions.md)

**Status:** resolved

Type: task

## Acceptance criteria

- [x] Exercise representative combined portal/OAuth/account/relay/quota/dependency/
  publication/drain journeys at the integrated revision and compare captured rows
  with the agreed catalog. No relevant serving path retains a raw second logger.
- [x] Verify shared output bounds under concurrent public activity, repeated
  refusals, blocked/failed stdout and shutdown. Demonstrate that loss summaries
  are best effort, no idle heartbeat appears and the logging path cannot stall
  serving indefinitely.
- [x] Sweep synthetic identity, credential, address, URL, header, body, content and
  arbitrary error canaries across integrated paths. Classifications are fixed;
  the implementation does not claim to sanitize arbitrary upstream text.
- [x] Run routine and separate race profiles and required real-PostgreSQL fixtures;
  record exact revision, commands/environment and passed/failed/not-run evidence.
  Resolve required missing evidence before claiming local implementation complete.
- [x] Publish the guide using actual final row examples, safe severity/reason
  meanings, correlation lifetime, suppression/freshness behavior and server
  visibility limits. Clearly distinguish logs from audit and future metrics.
- [x] Record metric/collector, infrastructure producer and deployment/release
  exclusions. Do not close the accepted full observability release ticket or
  claim deployed behavior based on local fixture results.

## Context and standing constraints

Repository scope: this `clipp-relay` checkout. This relay directory owns
the completed tracker and evidence. Read the [accepted specification](../spec.md),
[logging contract](../logging-contract.md), [fixed event catalog](../logging-catalog.json)
and [dated source inventory](../source-inventory.md) before implementation.
The selected relay repository's agent instructions, glossary, operations and
verification contract govern code work; dated source findings are not current
runtime evidence.

Use the existing HTTP, libp2p or complete Account/Quota/Relay seams. Test emitted
behavior rather than private logging helper mechanics. Ship privacy and bounded
output tests with the behavior. Prefer the shared diagnostic seam introduced by
the lifecycle slice; do not add a second logger, unbounded queue, arbitrary field
API or identity-linked diagnostic store.

Preserve accepted authorization, transactional audit, conservative quota,
one-session-per-Peer-ID, stock relay, expiry and shutdown semantics. Diagnostic
IDs and observation state confer no authority. Expected refusals are INFO,
dependency degradation/global overload are WARN, and fatal failures/invariants
are ERROR according to the fixed contract. Quiet intervals and successful
page/asset reads create no periodic activity or gauge row.

This effort excludes metric families/endpoints/scrape snapshots/collectors,
actual missing infrastructure producers, image/release publication, deployment, live
qualification and closure of the full observability release ticket. Do not
introduce those as dependencies for this local logging slice. Preserve unrelated
work, profiles and private configuration.

## Verification and completion evidence

Run relevant focused behavior checks and the relay's required verification gates
for the candidate. Record the exact revision/environment, commands and separate
passed/failed/not-run results. Required real-PostgreSQL evidence for durable flows
must pass before resolving its ticket; a skipped fixture is not a pass. The final
integration slice aggregates evidence without deferring per-slice safety checks.
Local results do not establish deployed behavior. Add or update the slice's
operator examples so its outcome is independently demonstrable.

## Comments

- 2026-10-11: Human approved the eight-slice breakdown and its blocking edges. Published as `ready-for-agent`; unclaimed, with no implementation evidence yet.
- 2026-10-11: Claimed by coordinator for the final integrated slice from relay integration `71b02629fedfe34dca7fa457f2afed94a4379a39`. Qualify catalog rows, bounds, privacy and real PostgreSQL evidence on one final tree; document operator limits. Preserve logging-only scope and do not close the full observability release ticket.

## Answer

Qualified the integrated logging slice against the fixed catalog and completed the relay's `docs/operator/service-logging.md` with actual captured local rows, separately labeled illustrative outcomes, correlation/severity/loss meanings, and server visibility limits. Combined HTTP/libp2p tests use one bounded logger and validate emitted tuples and fields while exercising concurrent refusals, blocked/failed stdout, drain and shutdown. Discovery and cleanup were the two missing producers found by this cross-slice check: Discovery now logs only proved HTTP outcomes; cleanup logs a failed pass and one proven recovery, while ordinary successful passes stay quiet. Untyped Discovery backend failure does not assert database cause, and shared invalid access does not assert account blocking.

Source commits `cb1a8f622ea825bbafa11589b667f04670979f6b` and liveness-test review fix `ca2fa425b3bc0d1208e153742dce5d8ed4ad0211` merged into relay integration at `4c75deca275eaa6df841f617165e0f251184899b`. The clean merge and source have identical tree `ef2db4f922b29883e46479852c02f32b7cbab48a`. Merger `/root/merge_11` found no remaining ticket-11 blocker; focused integrated capture/blocked-output tests passed on the merge. Runtime and canonical catalogs are byte-identical at 499 finite tuples, and a source scan found no raw second logger under serving `cmd`/`internal` code.

Exact clean source/merge-tree checks passed: `GOCACHE=/private/tmp/clipp-relay-ticket11-go-cache bash scripts/verify.sh routine`, separate `CGO_ENABLED=1 GOCACHE=/private/tmp/clipp-relay-ticket11-go-cache bash scripts/verify.sh race`, and five fresh disposable Docker PostgreSQL 18 TLS/SCRAM fixtures with host-mounted data: portal/OAuth, owner/admin/deletion, quota/accounting, dependency/maintenance, and cleanup focused race. See the [run evidence](../run.md) for exact commands, logs, test matrix and limits. Stock PostgreSQL 17/18 image smoke, native images, hosted CI, live/deployed qualification and absent infrastructure producers were not run or created; metric and collector behavior was untouched. Local evidence does not establish deployed behavior or close the accepted full observability release ticket.
