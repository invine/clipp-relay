# 07: Preserve accounting through drain, restart and recovery

**What to build:** Preserve as much observed usage as the existing shutdown budget permits, restart safely from durable totals, and recover the database without reviving deleted accounts, stale settlements or credentials.

**Blocked by:** 06 — Delete accounts without waiting for failed accounting.

**Status:** resolved

Parent: [Relay, Quota and Auth specification](../spec.md), especially I4, I7 and I8. User stories: 9, 35–37. Breakdown approved 2026-10-24.

- [x] Relay Drain continues settlement for traffic already admitted and attempts a final flush within existing shutdown limits. Preserve discovery withdrawal, refusal of new work, bounded circuit drain and forced termination.
- [x] Shutdown never waits indefinitely for accounting. A second/forced termination retains existing behavior; no new unbounded retry or cleanup phase is introduced.
- [x] Pending usage survives ordinary session disappearance/revocation long enough for bounded settlement. Forced exit may lose unsettled observations without corrupting or subtracting confirmed totals.
- [x] Restart initializes accounting from authoritative durable totals/receipts. A committed write with a lost reply remains counted once; an abandoned operation is not reconstructed as a new debit. No live session or unused prepaid credit is restored.
- [x] Quota recovery tooling understands recorded totals, receipts and retained-usage imports. Stale operation identities and sequences cannot be replayed as fresh consumption after restoration.
- [x] Preserve independent Recovery Journal enforcement, restore-time credential invalidation and Recovery Review Hold. Restored old data cannot resurrect deleted accounts or override ticket 06's finalization/import protections.
- [x] Real-circuit drain tests observe settlement while traffic completes, bounded final flush and forced closure. Real PostgreSQL/restart fixtures distinguish committed writes, lost replies and accepted loss of observations that never committed.
- [x] Recovery tests exercise the documented restore workflow with new accounting data and deletion records. Provider/cluster qualification is reported separately; fixture success is not claimed as live recovery qualification.
- [x] Update shutdown/recovery instructions and evidence expectations. Do not activate the serving path, run a live restore or deploy; ticket 08 completes local integration and migration readiness.

## Implementation notes

Ticket 06 is a genuine prerequisite because recovery must preserve its account-finalization and retained-usage contract. This slice does not require the portal work to be complete and does not introduce event replay.

## Answer

Bounded drain/final flush, durable restart and concrete local offline recovery safeguards implemented. Source `0f604c74424194b5748b8d530706b06c8c455032`, correction `253461f9d1d796a1b164ea6ab91ac99fe592a30b`; final integration `a41456811cd8968c4495c7916bde1359c09f9abf`. PG18 real-circuit/crash/recovery and focused race passed; integrated original mixed and race checks passed after correction. Initial forced-drain failure was investigated: admitted server transport was closed but remote registry state lagged. Stronger exact two-endpoint/circuit-abort oracle and pending-close cleanup correction passed repeated tests. Artifacts `/private/tmp/clipp-periodic-07-handoff.md`, `/private/tmp/clipp-periodic-07-integration.md` retain failed and successful evidence. Schema12 adds local recovery holds/receipts. Physical backup/WAL/provider/cluster qualification not run.
