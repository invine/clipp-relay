# 05: Log Relay Authentication and Relay Session lifetimes

**What to build:** An operator can follow admission, preauthentication,
authentication, renewal and the end of one physical connection without logging
the Device Identity or Relay Account.

**Blocked by:** [3: Log startup, readiness and bounded shutdown](03-implement-signal-foundations.md)

**Status:** resolved

Type: task

## Acceptance criteria

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
- 2026-10-11: Claimed by coordinator for `/root/relay_sessions` from relay integration `5fe74ef2a3a089100a5bb66d7e6f97bf2b63ef2e`. Preserve one-session-per-Peer-ID admission, authenticated session expiry, quota authority and stock relay behavior; source seams in `/private/tmp/clipp-relay-logging-exploration.md`.

## Answer

Implemented catalog-bounded Relay Authentication and Relay Session observations at the existing protocol/connection seams, including independent ephemeral request and physical connection IDs, actual transport evidence, admission, renewal, expiry, replacement, policy and drain outcomes. Protocol tests cover TCP/WSS/WebRTC, canaries, repeated refusals and session cleanup. The existing one-session-per-Peer-ID path remains authoritative. Source branch `codex/relay-service-visibility-05` committed `68e9b0bb02ab40a91c5f80ea294d1ee530860b44` from baseline `5fe74ef2a3a089100a5bb66d7e6f97bf2b63ef2e`; it merged into relay integration at `d9caf928e10f10cc8e85923f23e9165b8bc99c17` with clean worktrees.

On the integrated revision, the focused 11-test protocol/privacy command, `GOCACHE=/private/tmp/clipp-relay-go-cache bash scripts/verify.sh routine`, and `CGO_ENABLED=1 GOCACHE=/private/tmp/clipp-relay-go-cache bash scripts/verify.sh race` passed on 2026-10-11 (Go 1.27.2 darwin/arm64). Merger `/root/merge_05` reviewed the ticket, fixed catalog, privacy canaries and session contract with no blocker. Real PostgreSQL was not run for this in-memory protocol slice; the routine suite's skipped SQL fixtures are not counted as durable-flow evidence. Native/live gates were not run. See [run evidence](../run.md).

Visibility remains bounded by truthful producers: pre-libp2p handshake rejection has no producer; `relay.authentication` lacks a quota-exhaustion pair, which ticket 09 will report through quota events; and `quota.ErrTemporary` does not prove a clock-specific cause at the session seam.
- Refines the unclaimed preliminary outline **Instrument relay and quota journeys**. This replaces its question-style build placeholder with approved acceptance criteria; no work was marked complete.
