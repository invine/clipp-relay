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

## Fog

No product decision remains open. Native/live acceptance is outside local implementation.
