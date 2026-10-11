# 10: Log dependency and maintenance state transitions

**What to build:** Database/provider/publication/journal degradation and recovery,
cleanup/deletion backlog thresholds and diagnostic freshness produce meaningful
events while unavailable infrastructure producers remain honest.

**Blocked by:** [3: Log startup, readiness and bounded shutdown](03-implement-signal-foundations.md)

**Status:** resolved

Type: task

## Acceptance criteria

- [x] Use existing dependency and maintenance seams for DB work/acquisition,
  Google token/JWKS, publication API, journal and quota-clock outcomes. Successful
  internal queries do not produce individual routine stdout rows.
- [x] Use bounded diagnostic observations: 60-second cadence, a single in-flight
  pass with a 5-second deadline, immediate failed-sample invalidation and
  180-second staleness. Preserve the stricter quota-clock safety timing and
  maintenance ordering/cadence; diagnostic observation does not become authority.
- [x] Log initial state, actual changes and proven recovery only. Same-state
  retry/success produces no recurring row. Log warning/critical/breach crossings
  and clearance even if observation availability stays unchanged.
- [x] Verify accepted cleanup and pending-deletion thresholds, including a failed
  observation followed by fresh recovery and an observation aging into stale.
  Failed/stale/unavailable states carry no fabricated current value or periodic gauge.
- [x] Declare absent recovery/backup/repository/certificate/DB infrastructure
  producers unavailable. Do not implement those producers, infer infrastructure
  health from process/pool data, or modify metric endpoints and collector behavior.
- [x] Use existing real-PostgreSQL maintenance fixtures and controlled dependency
  doubles for failure/privacy/freshness checks. Include raw upstream/SQL/identity
  canaries and operator examples for uncertainty and threshold changes.

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
- 2026-10-11: Claimed by coordinator for `/root/dependency_maintenance` from relay integration `8234b07b00b7f0cee380e056e2158ccb59b8e734`. Preserve accepted maintenance cadence, quota-clock authority, absent-producer honesty and unchanged metric endpoints. Ticket 07 owns per-deletion worker `maintenance.deletion` action rows; this slice owns aggregate observation state/threshold and dependency degradation/recovery without duplicate action rows.

## Answer

Implemented bounded dependency and maintenance transition rows at database work/acquisition, provider/JWKS, publication, journal and quota-clock seams. Observations use a 60-second cadence, one pass in flight, a five-second deadline, immediate invalidation after failure and 180-second staleness. Cleanup and pending-deletion threshold crossings and clearances are emitted without a periodic gauge. Missing recovery, backup, repository, certificate and database-infrastructure producers remain explicitly unavailable. The existing quota-clock safety and maintenance ordering remain authoritative; metric endpoints are unchanged. The relay's `docs/operator/service-logging.md` describes transition, freshness and visibility limits.

Source commits `7caf94bf4ddb27cb876dd6f52062b91964f934a3` and `18026e121bd6883875679f1f694f49da743aa536` merged at `91cf332e9f647dcf231d07d318aad402cc4f77c2`. Merger review found that callback business errors and caller cancellation could falsely emit `dependency.database/failed`. The corrective commit `03ca4e315eb500f8b08892fac6ecab4ec4e807e7` merged at relay integration `bfc590c4b4139ac3234d8baea80fc66e6d119e98`; SQL/acquisition failure and proven recovery remain visible. The clean merge and source share tree `419822a2fbe8a6371cfb449b28d03ec9c9109891`.

On that exact tree, `GOCACHE=/private/tmp/clipp-relay-logging-go-build bash scripts/verify.sh routine`, `CGO_ENABLED=1 GOCACHE=/private/tmp/clipp-relay-logging-go-build bash scripts/verify.sh race`, and the copied `/private/tmp/clipp-ticket10-postmerge-pg18.sh` fixture all passed. The fixture used a fresh disposable Docker PostgreSQL 18 TLS/SCRAM container with host-mounted data and real database/auth/quota SQL plus focused auth race. Tests cover false business-error/cancellation transitions, true SQL and acquisition failure/recovery, privacy canaries, freshness and thresholds. Exact logs and the initial review finding are in [run evidence](../run.md). Stock Docker 17/18 smoke could not run because the Docker VM lacked storage; native/live/deployed checks were not run. Local checks do not establish deployed behavior.
