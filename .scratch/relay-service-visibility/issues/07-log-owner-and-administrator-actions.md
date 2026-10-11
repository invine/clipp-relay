# 07: Log owner and administrator account actions

**What to build:** Owner logout/revocation/deletion and administrator account or
plan changes have safe completion, refusal, conflict and uncertainty events.

**Blocked by:** [3: Log startup, readiness and bounded shutdown](03-implement-signal-foundations.md)

**Status:** resolved

Type: task

## Acceptance criteria

- [x] Drive HTTP account/administrator workflows for portal-only logout,
  Account-wide Revocation, deletion acceptance and account/plan mutations.
  Authorization, Origin/CSRF, recent-auth and revision checks retain their authority.
- [x] Log only safe action/outcome/reason enums and generated request correlation.
  Never emit actor/target identities, email, plan names, free text, raw form data,
  operation proofs or deletion credentials.
- [x] Emit success only after commit/proof. Exercise rollback, conflict,
  unconfirmed commit and proven ambiguous-commit reconciliation without inventing
  success or duplicating a completed business operation.
- [x] Preserve restricted transactional audit and existing account-policy/session
  effects. Operational rows are not a replacement audit history.
- [x] Add hostile canary checks and real-PostgreSQL HTTP evidence for affected
  durable mutations. Provide owner/admin examples explaining what the result proves.

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
- 2026-10-11: Claimed by coordinator for `/root/owner_admin` from relay integration `df06649a227a3ff7e0f462c915f239dad26d52f7`. Preserve owner/admin authorization, audit, transaction proof, policy and session effects. A disposable Docker PostgreSQL 18 fixture with host-mounted data is available; stock Docker smoke remains storage-blocked.

## Answer

Implemented fixed owner logout, Account-wide Revocation and deletion acceptance outcomes, administrator account/plan actions, and per-deletion worker transitions. Rows use generated request correlation and safe catalog outcomes; restricted transactional audit, authorization, commit proof and account/session policy effects remain authoritative. The relay's `docs/operator/service-logging.md` gives examples and explains that a deletion acceptance row is not deletion completion. The fixed catalog has no `admin.review` or deletion invariant pair, so those paths do not invent one.

Initial implementation `3942d0ba76ca230af7408b33304b8f91ad7c4cfc` and truthful session-failure correction `a3ba1662b5f5c6c963d4bdce51fa91b12bad322e` were followed by review fixes `dc0a0aa293dc3b5914fc74f7195e7c8c6fff83da` and `0b6e18552341cf1ab5b59af0ed18ede60d08e9f0`. The fixes cover early administrator guard refusals, no-row versus SQL failures, transactional rechecks, ambiguous deletion preparation, worker pre-commit DB errors, quiet handled `review` 404, typed admission overload and caller cancellation. The clean final branch merged into relay integration at `71b02629fedfe34dca7fa457f2afed94a4379a39`; source and merge share tree `e82c12cef42089b8d3d6e6ee2a4fa751241f798c`. Merger `/root/merge_07` found no remaining slice blocker.

On that exact tree, `GOCACHE=/private/tmp/clipp-ticket07-gocache bash scripts/verify.sh routine` and `GOCACHE=/private/tmp/clipp-ticket07-gocache CGO_ENABLED=1 bash scripts/verify.sh race` passed. The final focused `/private/tmp/clipp-ticket07-pg18-focused.sh` fixture passed HTTP auth and focused race against a fresh disposable Docker PostgreSQL 18 TLS/SCRAM container with host-mounted data and distinct migration/serving roles. Emitted-row tests cover commit/rollback/uncertainty, refusal/DB distinctions, canaries and worker outcomes. Exact results are in [run evidence](../run.md); final broad gates are captured in the implementer's tool transcript, not older pre-fix log files. Stock Docker 17/18 smoke could not run because the Docker VM lacked storage; native/live/deployed gates were not run. Local results do not establish deployed behavior.
