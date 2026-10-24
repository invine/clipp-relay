# 03: Account for live traffic periodically

**What to build:** Carry live reporter observations through Quota into durable Recorded Relay Usage on a one-second target cadence. All sessions of an account share eligibility and exhaustion decisions, without Relay coordinating prepaid blocks.

**Blocked by:** 02 — Record usage with safe retries.

**Status:** resolved

Parent: [Relay, Quota and Auth specification](../spec.md), especially I1, I3–I5 and I6. User stories: 1, 2, 10–15, 18, 19, 22, 30, 38. Breakdown approved 2026-10-24.

- [x] Relay supplies current report-time account attribution to Quota. HOP and STOP endpoints count independently, including twice for one account; unattributed tails remain aggregate observations. Preserve the stock reporter's replacement-tail limitation.
- [x] Observation ingestion performs no database round trip and uses checked counters. Quota owns bounded aggregation and scheduling across all account sessions, with no worker or durable row per callback.
- [x] Nonempty usage settles on a one-second target cadence through ticket 02's command. Pending observations and immutable in-flight batches remain separate; an acknowledgment removes only the confirmed batch.
- [x] Observations arriving during persistence are retained. Each batch carries its original processing week, and per-week state does not mix observations across Monday, providing the basis for ticket 05's complete rollover behavior.
- [x] Admission uses authoritative eligibility. Confirmed usage plus locally known unsettled usage counts exactly once, including after a refreshed total incorporates the in-flight batch.
- [x] Confirmed exhaustion detaches and closes all account sessions. Record full attributable overrun; do not claim a hard byte bound or silently clamp totals.
- [x] Implement the default three-second unhealthy-account cutoff and deny admission until eligibility recovers. Balance reads cannot mask pending write uncertainty. Ticket 04 completes configurability and policy/failure interleavings.
- [x] Accounting continues after ordinary disconnection or revocation while pending observations need settlement. Stale outcomes cannot restore session authority.
- [x] The new composed relay path removes Relay's balance-read/fund/split/consume sequence. Quota owns timing, retries and closure decisions; Relay owns physical detachment and closure.
- [x] Controlled-time Quota tests and composed libp2p/PostgreSQL tests cover shared usage, endpoint attribution, concurrent observations, in-flight acknowledgment, exhaustion, default cutoff and lost settlement replies. Tests use production composition.
- [x] Exercise the new path in test composition while leaving serving-path cutover to ticket 08. Do not create a permanent public mode switch or deploy a partially completed accounting lifecycle.

## Implementation notes

Cadence is a target, not a maximum transaction duration or crash-loss guarantee. Durable totals survive; unsettled observations may be lost on crash. This ticket must not weaken protected admission or introduce Login Grant interpretation into Quota.

## Answer

Quota-owned bounded periodic observations/settlement and independent default cutoff implemented. Source `cd9dc6a6223f6cd5b6f531db1d911d0b9095312f`; integration `a53594a869be7cea30a9ddbd4451b0101f4b2fcd`. PG18 periodic/composed acceptance, focused race, full Relay/diagnostic suites and vet passed; integrated periodic/composed PG checks passed. Focused Standards/Spec assessment found no issues. Artifacts `/private/tmp/clipp-periodic-03-handoff.md`, `/private/tmp/clipp-periodic-03-integration.md`. Staged constructor remains until 08; final review/verification still required.
