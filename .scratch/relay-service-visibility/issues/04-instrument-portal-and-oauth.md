# 04: Log portal sign-in and OAuth credentials

**What to build:** Operators can locate browser sign-in, consent,
authorization-code exchange and refresh failures, and distinguish them from
successful server-side credential issuance.

**Blocked by:** [3: Log startup, readiness and bounded shutdown](03-implement-signal-foundations.md)

**Status:** resolved

Type: task

## Acceptance criteria

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
- 2026-10-11: Claimed by coordinator for `/root/portal_oauth` from relay integration `5fe74ef2a3a089100a5bb66d7e6f97bf2b63ef2e`. Preserve fixed catalog, independent request IDs, committed credential boundaries and existing portal/OAuth authority; source seams in `/private/tmp/clipp-relay-logging-exploration.md`.

## Answer

Implemented safe, request-scoped portal sign-in and OAuth authorization/consent/code/refresh observations, including independent callback IDs, committed credential boundaries, replay/expiry/refusal categories, quiet successful pages/assets, and fixed 404/405 refusal events without raw request material. A review fix distinguishes an unconfirmed Portal Session commit from a proven failure using the sole newly permitted `portal.login/uncertain/commit_unconfirmed` pair; the later approved graph supplies that narrow catalog decision. Source commits were `eee3ab7` and `89cf1a6e065c0ef1e9173f6ad6863eb1bbf81143`, with current-tip merges through clean ticket branch `99891c54611e6ba08c030e10d7abae1202e1ade5`. Relay integration merged the result at `df06649a227a3ff7e0f462c915f239dad26d52f7`.

The exact source tree and integration tree were byte-identical (`a8e9bebb7e8785c78afda705d09bfcacf6af34c9`). On that tree, `GOCACHE=/private/tmp/clipp-relay-ticket04-gocache bash scripts/verify.sh routine`, `GOCACHE=/private/tmp/clipp-relay-ticket04-gocache CGO_ENABLED=1 bash scripts/verify.sh race`, and fresh real-PostgreSQL 18 TLS/SCRAM focused durable-flow smoke via `/private/tmp/clipp-ticket04-pg18-focused.sh` passed on 2026-10-11. The database gate covered startup/health/roles, full database and quota suites, focused portal/OAuth provider, issuance, replay and lost-ack flows. Integrated HTTP/diagnostic focused checks passed; the unconfigured SQL tests in that local integrated run were skipped and not counted. Logs and fixture hashes are in [run evidence](../run.md). Merger `/root/merge_04_fix` found both earlier review gaps fixed with no new blocker.

Earlier full race and full auth database runs under concurrent load had timing-sensitive failures; the final routine/race and affected durable-flow checks passed after the review fix. Stock Docker PostgreSQL 17/18 smoke remains unavailable because the Docker VM storage is full; that separate profile is not claimed as passed. Native/live gates were not run. Server rows prove issuance or refusal at the service boundary, not browser/client receipt.
- Refines the unclaimed preliminary outline **Instrument portal and OAuth journeys**. This replaces its question-style build placeholder with approved acceptance criteria; no work was marked complete.
