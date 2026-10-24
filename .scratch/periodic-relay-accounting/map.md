# Periodic relay accounting

Parent: [spec](spec.md). Ticket Status/Blocked by fields own the task graph.

## Notes

All eight tickets requested on 2026-10-24. See [run](run.md) for local implementation evidence and limits.

## Decisions-so-far

- [01](issues/01-protected-account-operations.md): protected production composition integrated and verified; required collaboration fails closed.

- [02](issues/02-recorded-usage-safe-retries.md): authoritative observed totals and immutable ordered retry receipts integrated; PG18 and race passed.

- [03](issues/03-periodic-live-traffic-accounting.md): bounded periodic observations and independent cutoff integrated; composed PG18 and race checks passed.

- [05](issues/05-weekly-accounting-and-presentation.md): weekly readiness,13-window retention and confirmed query models integrated; focused PG18/race evidence passed.

- [04](issues/04-accounting-health-and-policy.md): deadline/config propagation and immediate recorded+pending policy effects integrated; combined readiness retains security fence.

- [06](issues/06-deletion-and-retained-usage.md): retirement, bounded final settlement and serialized capture/seal/import integrated; isolated PG18/race passed.

- [07](issues/07-drain-restart-and-recovery.md): bounded lifecycle/restart and concrete local recovery safeguards integrated; forced-drain correction verified with original mixed/race tests.

## Fog

No product decision remains open. Native/live acceptance is outside local implementation.
