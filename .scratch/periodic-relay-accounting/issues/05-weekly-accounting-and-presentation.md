# 05: Complete weekly accounting and usage presentation

**What to build:** Keep traffic in the week in which it was observed, retain bounded weekly history and show owners and administrators consistent confirmed usage through dedicated query models.

**Blocked by:** 03 — Account for live traffic periodically.

**Status:** resolved

Parent: [Relay, Quota and Auth specification](../spec.md), especially I2–I4 and I8. User stories: 3–7, 11, 18, 41. Breakdown approved 2026-10-24.

- [x] Monday 00:00 UTC starts a new Weekly Quota Window without carrying unused allowance forward. Observations processed before the boundary settle against the old week even if a lock wait, retry or acknowledgment finishes afterward.
- [x] Old-week pending and in-flight batches stay distinct from new-week state. A successful new-week read cannot conceal unresolved old-week usage; acknowledgment never erases observations from either week.
- [x] Existing valid sessions need no new sign-in solely for rollover, but continuing use requires healthy authoritative new-week eligibility. Expiry and conservative clock rules remain intact.
- [x] Preserve the current week and twelve preceding weekly windows. Totals and bounded receipts expire together safely; stale retry, cleanup or retired pending work cannot recreate an expired window or debit the new one.
- [x] Pending-state cleanup is bounded and coordinated with settlement. Do not discard ordinary unsaved observations merely because sessions disappear; do not retain unbounded historical batches.
- [x] Dedicated owner/admin query models expose Recorded Relay Usage, historical weekly totals and remaining allowance as max(0, cap minus recorded usage). Preserve consistent account/usage snapshots and identify live capacity as a separate live sample.
- [x] Presentation explains confirmation lag and conservative endpoint counting. Pending observations are not presented as confirmed, historical totals do not become percentages against today's plan, and overrun never produces negative remaining allowance.
- [x] Presentation reads cannot authorize Relay Authentication or bypass protected command validation. CQRS uses the existing process and database, without an event store or separate read database.
- [x] Controlled-time and real PostgreSQL tests cover both sides of Monday with lock waits, lost replies, concurrent observations and cleanup. HTTP presentation tests verify history, lag wording, overrun, snapshot consistency and the separation from authority checks.
- [x] Update affected presentation/help text and retention documentation. Test the new composition without changing the currently serving accounting semantics before ticket 08.

## Implementation notes

Use the glossary's Recorded Relay Usage and Weekly Quota Window terms. Quota Committed remains a distinct historical prepaid measure, not an alternative label for the new total.

## Answer

Weekly readiness/retention and consistent confirmed owner/admin read models implemented. Source `8c45148d62236e587c21b0fde3f2907056eae409`; integration `1742988e6fcb19faf5edf50d1b7cb34933dcf3fc`. Focused PG18/race, full portal/Relay and vet passed; integrated periodic/presentation/retention PG checks passed. Focused Standards/Spec assessment found no issues. Artifacts `/private/tmp/clipp-periodic-05-handoff.md`, `/private/tmp/clipp-periodic-05-integration.md`. Combined invalidation flag check belongs04;08 removes staging/legacy DTO and runs fresh full fixtures. A legacy profile rerun hit residue in reused disposable fixture; full legacy DB suite not claimed here.
