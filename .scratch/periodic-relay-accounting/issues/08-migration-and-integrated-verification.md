# 08: Complete migration and verify the integrated change

**What to build:** Make periodic observed-usage accounting the single serving behavior, provide a tested offline migration from prepaid test accounting, and deliver coherent documentation and verification evidence for the complete change.

**Blocked by:** 04 — Enforce configurable accounting health and policy changes; 05 — Complete weekly accounting and usage presentation; 07 — Preserve accounting through drain, restart and recovery.

**Status:** ready-for-agent

Parent: [Relay, Quota and Auth specification](../spec.md), especially I8 and Testing Decisions. User stories: 43; integrated acceptance of all 43 stories. Breakdown approved 2026-10-24.

- [ ] Activate the completed production composition as the single accounting path. Remove the obsolete prepaid implementation, unused-credit assumptions and transitional test-only wiring; no permanent dual-accounting mode remains.
- [ ] Provide a versioned offline migration under the existing stop/migrate/serve procedure. Reset obsolete prepaid totals, receipts and related retained test usage rather than trying to translate reserved credit into observed traffic.
- [ ] Preserve unrelated accounts, credentials, plans, audit records and Recovery Journal data. Reset occurs only as the explicit migration action, not on ordinary startup or subsequent restart.
- [ ] Verify fresh installation and migration from the original prepaid schema, including migration rerun behavior and ordinary restart. A partially completed or failed migration cannot leave a serving process using mixed semantics.
- [ ] Prevent the old prepaid binary from serving against the new schema and prevent the new binary from serving against an unmigrated schema. Document compatibility and operator failure/recovery behavior.
- [ ] Reconcile service requirements, README, configuration, diagnostics, portal wording, retention, deletion and recovery documentation. Recorded Relay Usage and Quota Committed remain distinct; original thoughts do not override accepted decisions.
- [ ] Preserve the original feature specification as the parent record. Record ticket completion/evidence without silently rewriting its accepted scope; raise an actual conflict if implementation exposes one.
- [ ] Run routine verification, the separate race profile and the real PostgreSQL coverage required by the specification against the integrated revision. Record environment, revision, dirty state and checks not run; fix failures rather than replacing checks with narrower substitutes.
- [ ] Verify integrated HTTP/libp2p outcomes across accounting, policy mutations, rollover, deletion and lifecycle behavior using production composition. Include migration followed by relay use, settlement, portal query and restart.
- [ ] Confirm exact-connection gating, one-session-per-Peer-ID replacement, failed renewal, grant-local behavior, preauthentication allowances and accepted transport fallback remain compatible. No client feature expansion is included.
- [ ] Document accepted limits: soft allowance, possible loss of unsettled observations, reporter tail attribution, one serving instance and no historical replay. No hard overrun, capacity or live-environment qualification claim is added without evidence.

## Implementation notes

The blocker list is minimal: ticket 07 includes ticket 06, and all three immediate blockers include tickets 01–03 transitively. Each preceding ticket owns its behavior tests; this ticket verifies their integration rather than postponing all testing until the end. Preparing and testing migration does not authorize deployment, live data purge, provider/cluster mutation or release publication.
