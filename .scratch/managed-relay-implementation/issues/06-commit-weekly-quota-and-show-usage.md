# 06: Commit weekly quota and show usage

**What to build:** Account use reserves conservative weekly credit durably and the owner sees honest current and historical quota statistics.

**Blocked by:** [04: Approve accounts and assign plans](04-approve-accounts-and-assign-plans.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I2, I5, I11.

## Contract and scope

Quota Committed is debited on allocation, not exact measured traffic. Default
allowance 1GiB; fund one shared account-week balance in ≤64KiB blocks or remaining
allowance. One allocation in flight, no speculative next block. Complete Quota
operation is the test seam; do not add a public endpoint for users to allocate credit.

Persist account/week total, sequence and latest stable operation ID/granted receipt,
not per-block history. Commit before install, apply once, recheck account/week/
generation; reconcile same ambiguous operation before later allocation. Late
invalidated/cancelled workers cannot install; new process never restores old
unspent credit. Disconnect preserves live credit, no disconnect/shutdown/crash
refund. Funding uncertainty is temporary, not proven quota exhaustion.

Monday 00:00 UTC reset, no rollover; old local credit discarded, new funding
required. DB wall time after lock waits, conservative monotonic deadlines.
Clock probe startup/60s, warning interval outside ±1s; close affected time-sensitive
work outside ±5s, >1s uncertainty or stale >5m; recover 3 valid cadence samples,
never extend old deadline. One DB snapshot for policy/usage; live counts separate.

Profile doughnut labeled Quota committed explains unspent funded credit; zero/
exceeded explicit. Historical absolute totals current+12 completed windows, no
recomputed historical percentages. Short Read Committed/ordered account locks,
parameterized real PostgreSQL, one receipt per account/week, bounded DB work.

## Acceptance criteria

- [ ] Allocating 64KiB and observing 10KiB shows 64KiB committed/54KiB usable, not a durable 10KiB traffic claim; concurrent sessions share one balance.
- [ ] Lost reply, known rollback, duplicate/reordered operation and late receipt tests prove no duplicate debit/install and no unconfirmed spend.
- [ ] Disconnect/restart/graceful exit do not refund; old receipts never restore old process credit.
- [ ] Lock wait crossing Monday, clock regression/skew and DB outage preserve conservative deadlines and never spend old-week credit.
- [ ] Current doughnut and history render accurate committed totals/zero/exceeded states from a consistent snapshot, without device/session detail.
- [ ] Schema constraints permit committed usage above a later reduced allowance, contain only one bounded latest receipt and use indexed account-week queries.

## Demonstration

Fund through the complete Quota interface in an integration harness, view the resulting profile, simulate a lost commit response and restart, then verify the same committed total.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

- Claimed centrally on 2026-09-27 after ticket 04 resolution (`21ab747`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.
