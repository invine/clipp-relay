# 06: Record and reconcile Relay Usage

**What to build:** Relay Accounts share correctly recorded original-week usage through repository-backed eligibility and settlement, retaining safe retries and independent exhaustion/accounting-health cutoffs.

**Blocked by:** 01: Introduce repository scopes through credential validation.

**Status:** resolved

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I5, I9–I10; T1–T3, T5, applicable T6/T8.

- [ ] Migrate Quota persistence for authoritative time, policy/eligibility reads, confirmed usage, settlement receipts and retirement checks to backend-neutral interfaces. SQL and driver-shaped hooks stay in the PostgreSQL adapter.
- [ ] Keep observation aggregation, pending/in-flight state, cadence, retries, monotonic accounting health and cutoff decisions in Quota; retain report-time endpoint attribution and physical closure ownership in Relay.
- [ ] Update usage total and compact retry receipt atomically for the original Weekly Quota Window. Identical retries reconcile once; changed payloads or invalid sequence are rejected without duplicate charging.
- [ ] Preserve concurrency protection shared with the existing deletion seal throughout migration. Late settlement cannot recreate a deleted account, debit a replacement account or recreate retired weeks.
- [ ] Preserve shared account usage, full attributable overrun, remaining-allowance presentation, Monday rollover, accepted crash loss and separate health checks for settlement progress versus authoritative eligibility.
- [ ] Preserve work/guard/transaction ordering, authoritative after-wait time, independent clock-safety behavior, existing budgets and final settlement within the remaining original drain context.
- [ ] Verify concurrent observations during in-flight writes, lost commit acknowledgements, conflicts, rollover, deletion races, exhaustion, failed-write/successful-read combinations and blocked-worker cutoff using controlled adapters and real PostgreSQL plus relay-wire outcomes.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.


## Resolution

Coordinator integrated source commit a9b4e21ca69f93693d89ff26eab0d434c6281f28 on codex/persistence-repositories-integration at aeed88f1726d41d06382271d44a8fe2dc15f8bfd. Quota now uses the accounting contract and PostgreSQL adapter for eligibility, recorded usage, admission and atomic total/receipt settlement while retaining policy, locks, cutoffs and retry reconciliation. Integrated Quota/Auth/issuer/persistence/service tests passed; full go vet ./... and go build ./... passed; formatting and diff checks passed. Real disposable PostgreSQL was unavailable because Docker is not running; final qualification remains ticket 11.
