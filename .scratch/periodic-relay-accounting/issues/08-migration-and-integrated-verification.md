# 08: Complete migration and verify the integrated change

**What to build:** Make periodic observed-usage accounting the single serving behavior, provide a tested offline migration from prepaid test accounting, and deliver coherent documentation and verification evidence for the complete change.

**Blocked by:** 04 — Enforce configurable accounting health and policy changes; 05 — Complete weekly accounting and usage presentation; 07 — Preserve accounting through drain, restart and recovery.

**Status:** resolved

Parent: [Relay, Quota and Auth specification](../spec.md), especially I8 and Testing Decisions. User stories: 43; integrated acceptance of all 43 stories. Breakdown approved 2026-10-24.

- [x] Activate the completed production composition as the single accounting path. Remove the obsolete prepaid implementation, unused-credit assumptions and transitional test-only wiring; no permanent dual-accounting mode remains.
- [x] Provide a versioned offline migration under the existing stop/migrate/serve procedure. Reset obsolete prepaid totals, receipts and related retained test usage rather than trying to translate reserved credit into observed traffic.
- [x] Preserve unrelated accounts, credentials, plans, audit records and Recovery Journal data. Reset occurs only as the explicit migration action, not on ordinary startup or subsequent restart.
- [x] Verify fresh installation and migration from the original prepaid schema, including migration rerun behavior and ordinary restart. A partially completed or failed migration cannot leave a serving process using mixed semantics.
- [x] Prevent the old prepaid binary from serving against the new schema and prevent the new binary from serving against an unmigrated schema. Document compatibility and operator failure/recovery behavior.
- [x] Reconcile service requirements, README, configuration, diagnostics, portal wording, retention, deletion and recovery documentation. Recorded Relay Usage and Quota Committed remain distinct; original thoughts do not override accepted decisions.
- [x] Preserve the original feature specification as the parent record. Record ticket completion/evidence without silently rewriting its accepted scope; raise an actual conflict if implementation exposes one.
- [x] Run routine verification, the separate race profile and the real PostgreSQL coverage required by the specification against the integrated revision. Record environment, revision, dirty state and checks not run; fix failures rather than replacing checks with narrower substitutes.
- [x] Verify integrated HTTP/libp2p outcomes across accounting, policy mutations, rollover, deletion and lifecycle behavior using production composition. Include migration followed by relay use, settlement, portal query and restart.
- [x] Confirm exact-connection gating, one-session-per-Peer-ID replacement, failed renewal, grant-local behavior, preauthentication allowances and accepted transport fallback remain compatible. No client feature expansion is included.
- [x] Document accepted limits: soft allowance, possible loss of unsettled observations, reporter tail attribution, one serving instance and no historical replay. No hard overrun, capacity or live-environment qualification claim is added without evidence.

## Implementation notes

The blocker list is minimal: ticket 07 includes ticket 06, and all three immediate blockers include tickets 01–03 transitively. Each preceding ticket owns its behavior tests; this ticket verifies their integration rather than postponing all testing until the end. Preparing and testing migration does not authorize deployment, live data purge, provider/cluster mutation or release publication.

## Answer

Sole observed accounting and explicit offline migration13 delivered. Initial08 source `7255351462bca52a38ebfec0126bb7aaeb85f6a3`; review-fix source `93c9f05d049fbbcc5b256b54b89584c14176a6e6`, `0954af9abf142de34a9e711a77822341817142fe`; final reviewed integration `b66b06456e9419fac86f69b87b89ad231f0c7d27`, exact tested tree `49d2bd5516382f1d5d68d25912cda72d14841e4e`. Final routine, complete race, fresh PostgreSQL18/17 smoke with extra DB race/PG17 quota profiles, and integrated focused regression checks passed. Independent Standards and Spec re-reviews report zero remaining findings. Original migrations1–12 and accepted parent spec preserved. See [final review](../review.md) and [run evidence](../run.md). Artifact basenames `clipp-periodic-review-fixes-{handoff,integration}.md`, final review reports and `clipp-periodic-review-{routine,race,smoke}-final.log` are preserved in the relay-local evidence directory. Native/provider/cluster/physical-backup/live acceptance remains separately unqualified.
