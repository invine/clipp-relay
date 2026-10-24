# 02: Record usage with safe retries

**What to build:** Provide a complete Quota command that records an observed account-week usage batch and a query that returns confirmed usage. Lost replies and retries must produce one durable debit, without retaining an event history.

**Blocked by:** 01 — Make protected account operations explicit.

**Status:** claimed

Parent: [Relay, Quota and Auth specification](../spec.md), especially I1–I4 and I8. User stories: 8, 9, 12, 33, 34, 38, 41. Breakdown approved 2026-10-24.

- [ ] Add the persistence shape and module operations for nonnegative authoritative Recorded Relay Usage totals per immutable account identity and Weekly Quota Window. Deltas may exceed the former 64 KiB allocation size and may take totals above the allowance.
- [ ] A stable operation identifier, immutable positive delta and ordered sequence identify each batch. There is at most one unresolved settlement per account-week.
- [ ] Total and latest receipt update atomically under the protected account/database ordering established by ticket 01. A matching retry confirms the outcome without adding bytes.
- [ ] Conflicting payloads, stale sequences and unexplained gaps cannot become new debits. A stale retry is never automatically renumbered. Replacing the latest receipt remains safe because the monotonic sequence rejects older work.
- [ ] A lost commit reply is reconciled using the same operation before a successor is accepted. Known rollback and unknown outcome are distinguished; ambiguous writes are not blindly retried as fresh operations.
- [ ] A fresh process-local instance reads durable state without reconstructing lost observations. Delayed operations obey database serialization and sequence checks.
- [ ] Already observed usage can settle despite revocation, suspension, generation changes or exhaustion. Authorization to continue is a separate concern. Absent, finalized-deleted or retired account-week state cannot be recreated by settlement.
- [ ] Retained-usage opening totals can be represented without a fake settlement history. Totals and bounded receipt state support the later import, retention and recovery slices.
- [ ] Real PostgreSQL tests through the Quota interface cover success, rollback, lost reply, identical retry, payload conflict, stale retry after receipt replacement, restart and rejection after deletion/retirement. Assert durable outcomes, not incidental SQL text.
- [ ] The staged persistence and test composition coexist safely with the currently serving prepaid path. No deployment, live reset, permanent dual-accounting mode or production activation occurs here; ticket 08 completes cutover.

## Implementation notes

Use concrete PostgreSQL and existing module seams. The command/query split stays in-process and in the same database. Event sourcing, a replay contract, a message broker and a generic datastore abstraction are out of scope. The schema can evolve during these tickets; final migration must be tested from the original prepaid schema in ticket 08.
