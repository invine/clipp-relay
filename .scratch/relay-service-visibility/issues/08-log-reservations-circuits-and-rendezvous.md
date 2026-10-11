# 08: Log reservations, circuits and Rendezvous

**What to build:** Authentication success is distinguishable from reservation
refusal, circuit setup/lifetime and Rendezvous lookup/lease outcomes.

**Blocked by:** [5: Log Relay Authentication and Relay Session lifetimes](05-instrument-relay-journeys.md)

**Status:** resolved

Type: task

## Acceptance criteria

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
- 2026-10-11: Claimed by coordinator for `/root/protocols` from relay integration `d9caf928e10f10cc8e85923f23e9165b8bc99c17`. Preserve stock HOP and Rendezvous behavior, aggregate callback correlation limits and accepted privacy; the later approved duration criterion permits a narrow catalog correction for measured stock circuit close duration.

## Answer

Implemented catalog-backed reservation/new-versus-renewed, protected HOP refusal, aggregate circuit request/open/close and Rendezvous v1/v2 outcomes at actual wire and stock callback seams. Aggregate callbacks omit request/session correlation and transport; a completed circuit carries duration only from the stock close callback, with no byte or payload row. Real protocol tests distinguish authentication success from later reservation refusal and exercise canaries and repeated events. Source branch `codex/relay-service-visibility-08` committed `aa68591` and merged the then-current integration tip at `2a192c295ebc0e9e4df3917e9f41cafc38543689`; its work merged into relay integration at `399bd60e5f59160bd236f8345e5ff679a965af18` with clean worktrees.

On the integrated revision, four focused real wire tests, `GOCACHE=/private/tmp/clipp-go-cache bash scripts/verify.sh routine`, `GOCACHE=/private/tmp/clipp-go-cache CGO_ENABLED=1 bash scripts/verify.sh race`, and `git diff --check` passed on 2026-10-11 (Go 1.27.2 darwin/arm64). Merger `/root/merge_08` reviewed stock go-libp2p v0.49.0 callbacks, catalog pairs, correlation limits, protocol behavior and privacy with no finding. Real PostgreSQL was not run because this slice changes no durable flow; native/live gates were not run. See [run evidence](../run.md).

The approved ticket's measured close-duration criterion corrected `relay.circuit.duration_measurable` to `true` in the runtime and canonical catalogs; the finite field/pair allowlist and all other catalog entries remain unchanged. Protocol success does not establish client acceptance, Direct Connection or Clip delivery.
