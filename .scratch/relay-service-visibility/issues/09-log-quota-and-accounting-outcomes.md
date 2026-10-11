# 09: Log quota and accounting failure outcomes

**What to build:** Quota funding, expected exhaustion, uncertain allocation,
credit retirement and accounting failure are visible, including their observed
Relay Session consequences.

**Blocked by:** [5: Log Relay Authentication and Relay Session lifetimes](05-instrument-relay-journeys.md)

**Status:** resolved

Type: task

## Acceptance criteria

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
- 2026-10-11: Claimed by coordinator for `/root/quota_accounting` from relay integration `399bd60e5f59160bd236f8345e5ff679a965af18`. Preserve conservative allocation/charging, one-session cutoff and accounting authority; current Docker VM storage blocks stock smoke, but a disposable Docker PostgreSQL 18 fixture with host-mounted data is available for required durable evidence.

## Answer

Implemented fixed, private quota funding/refusal, uncertain allocation/reconciliation, credit retirement, and reporter-failure outcomes at the complete Quota/Relay seams. Successful routine byte callbacks remain quiet; allocation is not described as exact payload transfer. Protocol tests show conservative account-wide session cutoff on reporting failure, and durable tests cover known rollback, lost acknowledgment, outage, clock, refusal, expiry/discard, privacy and bounds. Source feature commit `d353663` merged the then-current relay integration tip at `c152fad9893be3b6441d58a4f1a3785383b67e3e`; the result merged into relay integration at `8234b07b00b7f0cee380e056e2158ccb59b8e734`, with clean worktrees.

The source and integration trees are identical at `b1ba2c707a3807062e1a2e1e10f4fead27d9d7ad`. On that tree, `GOCACHE=/private/tmp/clipp-ticket09-gocache bash scripts/verify.sh routine`, `CGO_ENABLED=1 GOCACHE=/private/tmp/clipp-ticket09-gocache bash scripts/verify.sh race`, and fresh disposable Docker PostgreSQL 18 TLS/SCRAM with host-mounted data for full quota plus focused quota race via `bash /private/tmp/clipp-ticket09-pg18.sh` passed on 2026-10-11. The merged revision also passed focused Relay cutoff checks and a full routine profile. Merger `/root/merge_09` found no scope, privacy, allocation or conservative-cutoff issue. Exact logs are in [run evidence](../run.md).

The stock Docker PostgreSQL smoke was not run because Docker VM storage remains full; its absence is not counted as a pass. Native/live gates were not run. Catalog stays at 499 finite tuples with no new quota pair and circuit close duration enabled.
