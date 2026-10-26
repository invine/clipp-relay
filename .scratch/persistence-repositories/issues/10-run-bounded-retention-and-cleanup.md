# 10: Run bounded retention and cleanup through repositories

**What to build:** Background maintenance expires eligible records and reports cleanup health through bounded repository batch operations while preserving fair progress and privacy retention.

**Blocked by:** 01: Introduce repository scopes through credential validation.

**Status:** claimed — persistence 10 (coordinator: root), 2026-10-26

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I3, I5, I8–I10; T1, applicable T3/T6, T7–T8.

- [ ] Migrate maintenance writes and bounded batch queries for expiry, hidden-account children, eligible parent removal, required expiry audit and cleanup breach state through a maintenance persistence seam.
- [ ] Preserve fair cursors, bounded batch sizes, retry cadence, cancellation and separate per-operation/pass deadlines. Do not replace set-based batches with unbounded per-row repository calls or one transaction for the whole pass.
- [ ] Preserve confirmed usage/receipt co-retirement, retained usage expiry, credential/session privacy retention, parent-child ordering and required atomic audit on Pending account expiry.
- [ ] Move Portal Session and Retained Quota Usage key-coverage reads behind repositories while keeping missing-key policy in the functional module. Unavailable key material does not silently become an absent record.
- [ ] Preserve cleanup-state persistence and aggregate outcomes. Reuse narrow projection contracts where available; maintenance batch behavior remains independent of presentation-view migration.
- [ ] Verify several populated retention classes, hidden children, partial failures, cancellation, fair progress, missing keys and shared-budget refusal through controlled adapters and real PostgreSQL; no privileged or live cleanup operation is part of ticket publication.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
