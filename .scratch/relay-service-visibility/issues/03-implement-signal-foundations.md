# 03: Log startup, readiness and bounded shutdown

**What to build:** A real service start distinguishes startup validation,
listeners serving and usable publication, while readiness loss, recovery and
drain produce safe events. The same stdout path stays bounded even when the
output writer stops responding.

**Blocked by:** None (can start immediately).

**Status:** resolved

Type: task

## Acceptance criteria

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
- Refines the unclaimed preliminary outline **Implement the shared signal foundations**. This replaces its question-style build placeholder with approved acceptance criteria; no work was marked complete.
- 2026-10-11: Claimed by the coordinator for implementer `/root/foundation`; baseline relay `origin/main` at `19d00ae94379724d5d07f5f1998177f70125d45d`, preserving the approved logging contract and existing Relay Session, quota, audit, stock relay, and drain invariants.

## Answer

Implemented the fixed-catalog, single bounded diagnostic writer and separate ephemeral IDs; wired startup validation, listeners serving, readiness/publication transitions, and bounded drain/stop outcomes. Added behavior, privacy, blocked-writer, queue-loss, and operator-example checks. The relay source branch `codex/relay-service-visibility-03` committed `05bc94029006ed7a7d16d59294d2f0db1fdb3fcb` from baseline `19d00ae94379724d5d07f5f1998177f70125d45d`; it merged into `codex/relay-service-visibility-integration` at `5fe74ef2a3a089100a5bb66d7e6f97bf2b63ef2e` with both worktrees clean.

On the integrated revision, `GOCACHE=/tmp/clipp-relay-ticket03-go-build go test -count=1 ./internal/diagnostic ./internal/service ./cmd/clipp-relay`, `GOCACHE=/tmp/clipp-relay-ticket03-go-build bash scripts/verify.sh routine`, `CGO_ENABLED=1 GOCACHE=/tmp/clipp-relay-ticket03-go-build bash scripts/verify.sh race`, and `git diff HEAD^1..HEAD --check` passed on 2026-10-11 (Go 1.27.2 darwin/arm64, Helm 4.3.0). The embedded catalog matched the approved catalog byte for byte. Merger `/root/merge_03` found no blocking acceptance or privacy issue. Real PostgreSQL was not run for this slice because it adds no durable mutation flow; native image and live qualification were not run. See [run evidence](../run.md).

The fixed catalog has no truthful pair for migration success, the nonfatal retained-pepper warning, or an unexpected drain error. Those paths retain their business behavior; the latter emits `service.stopped failed/internal_failure` without inventing a `service.drain` outcome. This limitation is retained for the final guide.
