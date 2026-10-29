# Narrow the database runtime interface while preserving admission control

Status: ready-for-agent

Owning repository: relay (clipp-relay).  
Date: 2026-10-29.  
Planning baseline: `main` at `06d1847aa350c9f6fafcd362969f1958aab2a653`.  
Decision source: the user requested a specification for the cleanup discussed in this conversation and confirmed the existing persistence and database runtime test interfaces. This specification does not start implementation.

## Problem Statement

Following the persistence repository extraction, the database runtime still exposes three overlapping entry points: `StartUnit`, `WithUnit`, and `Unit`. Maintainers must distinguish admission ownership, callback lifetime and connection ownership even though production code outside the database module only needs `StartUnit` from this group. `WithUnit` serves `Unit`, and `Unit` serves `Exec`; their other callers are tests. The generic name `Unit` also obscures that it owns a connection without providing transaction atomicity.

The redundant interface makes it harder to understand which abstraction is necessary. Admission control itself remains useful: the connection pool bounds occupied connections, while admission bounds outstanding storage operations, including those waiting on application guards. Removing admission would discard an overload guarantee that the repository extraction did not replace.

## Solution

Reduce the exported database runtime interface while retaining its existing resource policy. Keep `StartUnit` as the PostgreSQL adapter's admission entry point. Remove exported `WithUnit` and `Unit`, either retaining private helpers where they improve locality or folding their single-caller behavior into the remaining execution path.

Functional modules continue to use existing persistence operations and transaction interfaces. The PostgreSQL adapter continues to hide resource ownership and database mechanics. Tests exercise these surviving interfaces instead of requiring generic exported callbacks solely for test setup. The result is less interface knowledge for maintainers with the same runtime behavior for Relay Accounts, Relay Authentication, Recorded Relay Usage and maintenance work.

## User Stories

1. As a maintainer, I want fewer overlapping database entry points, so that I can identify the supported execution paths quickly.
2. As a maintainer, I want admission and connection ownership to remain distinct, so that waiting on an application guard does not occupy a database connection.
3. As a maintainer, I want generic callback helpers to remain private when useful, so that internal composition does not enlarge the exported interface.
4. As a PostgreSQL adapter author, I want to start admission independently of a transaction, so that existing workflow ordering remains possible.
5. As a functional module author, I want to keep using storage-neutral persistence operations, so that database mechanics remain hidden.
6. As a relay operator, I want excessive storage work rejected promptly, so that overload does not create an unbounded population of admitted waiters.
7. As a relay operator, I want account, credential, quota and maintenance work to share the same admission budget, so that the total remains bounded across workflows.
8. As a caller, I want an earlier parent cancellation or deadline respected, so that canceled work does not continue through a fresh budget.
9. As a caller, I want nested work to reuse its admitted operation, so that it neither consumes another slot nor restarts the deadline.
10. As a caller, I want connection acquisition to remain bounded, so that an exhausted or unavailable pool produces a timely failure.
11. As a caller, I want all completion and failure paths to release their owned resources, so that later work can recover capacity.
12. As a caller, I want rows and transactions to retain resources until their existing completion points, so that a refactor cannot release resources while they are still in use.
13. As a caller, I want the existing execution path to report an expired operation instead of a successful result when applicable, so that deadlines remain meaningful after helper consolidation.
14. As a relay operator, I want database failure and recovery diagnostics preserved, so that operational visibility remains trustworthy.
15. As a relay operator, I want policy refusals and caller cancellation to retain their existing diagnostic classification, so that they do not create false database failure or recovery reports.
16. As a test author, I want to reserve capacity through existing persistence operations, so that tests do not require obsolete exported callbacks.
17. As a test author, I want real PostgreSQL checks for connection and transaction behavior, so that passing controlled tests is not mistaken for storage correctness.
18. As a reviewer, I want a bounded structural cleanup with explicit preserved behavior, so that I can assess it without revisiting admission policy or unrelated persistence design.

## Implementation Decisions

1. **Scope and dependency direction.** Modify the database runtime module and tests affected by narrowing its interface. Keep the persistence contract module, PostgreSQL adapter and functional module dependency direction established by the accepted repository design. No new production adapter, interface or module is needed.
2. **Exported surface.** Retain `StartUnit` with its current contract. Remove exported `WithUnit` and `Unit`; do not retain compatibility aliases or introduce equivalent exported callback methods under other names. Existing SQL execution and transaction methods remain available to their current consumers.
3. **Internal composition.** Private helpers are permitted when they concentrate resource ownership, deadline handling or diagnostics. Folding the single-caller chain into `Exec` is also permitted. Choose the simpler implementation after tracing behavior; do not require a helper count or rename unrelated types. The cleanup must reduce the interface without duplicating lifecycle logic across callers.
4. **Admission remains.** Preserve one shared serving runtime with 64 admitted operations and immediate overflow refusal. Keep the ordinary three-second operation deadline, the 500 ms pool-acquisition limit, the eight-connection serving pool and existing database statement, lock and idle limits. These are preserved policy values, not newly validated capacity recommendations.
5. **Ordering and ownership.** Preserve admission before application guards on paths that use this ordering, and acquire a physical connection only when database work begins. Nested work using the same runtime and admitted context reuses the slot and deadline. Releasing a nested scope must not release its outer owner's slot. Preserve idempotent owner release and earlier parent cancellation.
6. **Completion behavior.** Preserve the execution path's post-work deadline check and error precedence: an expired context replaces an otherwise successful result; an existing work error is not overwritten. Retain resource cleanup on success, failure and panic unwinding. Context cancellation remains cooperative; do not introduce goroutines that return early while SQL work still owns resources.
7. **Result and transaction lifetimes.** Retain existing row scanning, row iteration/closure and transaction commit/rollback resource lifetimes. Preserve transaction statement contexts, cancellation and uncertain-commit classification. This cleanup must not change transaction atomicity, add callback retries or treat cancellation as rollback proof.
8. **Diagnostics.** Preserve existing admission, acquisition, SQL and deadline observations on the surviving execution paths. Policy errors do not establish database failure or recovery; successful admission alone does not establish recovery. Preserve caller-cancellation handling, no-row classification and diagnostic privacy. Do not keep a general callback interface merely to inject policy errors in tests.
9. **Test migration.** Replace external test uses of removed callbacks with existing persistence operations for admission setup and surviving runtime SQL methods for actual database behavior. Rewrite helper-shaped assertions around observable outcomes. Preserve meaningful coverage before removing obsolete tests; tests must not require the new private helper names or structure.
10. **No behavior expansion.** Preserve current resource policy and functional outcomes. Findings unrelated to this cleanup should be recorded separately rather than changing fairness, limits, error contracts or workflow sequencing during the refactor.

## Testing Decisions

The user confirmed these seams on 2026-10-29: existing persistence operations for admission and cancellation, and the database runtime's remaining SQL methods for connection lifetime, deadlines and diagnostics. No new test interface is needed. Use the highest existing interface that can demonstrate each behavior.

- **Admission through persistence operations.** Extend or reuse the PostgreSQL adapter's operation admission tests. Hold 64 independent operations, verify immediate classified rejection of the next, release capacity and verify recovery. Check cancellation and idempotent closure. Exercise ordinary work and settlement operations against the same runtime to demonstrate a shared budget.
- **Nested ownership and ordering.** Through existing operation and transaction scopes, demonstrate that nested database work can use the last admitted slot without a second admission, retains the original deadline and leaves the outer slot held until closure. Preserve relevant identity-guard and settlement tests showing admission before waits and connection acquisition afterward. Avoid tests coupled to context keys, channel internals or polling implementation.
- **Database lifetimes and failure paths.** Reuse real PostgreSQL coverage for `Exec`, query results and transactions: unavailable or exhausted pools, SQL errors, release after failure, result lifetime, rollback, operation deadlines and narrower statement cancellation. Test the surviving execution path's deadline/error behavior without adding a production callback or test-only clock interface.
- **Diagnostics through observable events.** Preserve failure, recovery, cancellation and policy-neutrality assertions using recorded diagnostic events and existing persistence/runtime operations. Do not introduce dummy SQL solely to manufacture a policy-only outcome, because successful SQL can itself legitimately establish recovery. Admission without SQL must not emit a recovery event. Keep raw SQL, credentials and arbitrary policy messages out of diagnostics.
- **Prior art.** Existing operation admission/release and canceled-parent tests establish the persistence seam. Existing database work saturation, row/rows/transaction lifetime, transaction-context and diagnostic-transition tests establish resource behavior. The Auth quota-budget test demonstrates shared capacity across quota eligibility and cleanup reads; migrate its capacity setup to persistence operations.
- **Verification evidence.** Run focused affected tests, then the repository's routine and race verification profiles. Execute the relevant tests against disposable supported PostgreSQL 17 and 18 fixtures using existing qualification tooling. Record exact revisions, commands, results and skipped tests. An optional database test that skips does not establish PostgreSQL coverage. No new test harness or benchmark project is required.
- **Structural review.** Inspect production and test callers to confirm that the removed exported methods have no remaining users, no replacement exported generic callbacks appeared and no functional module acquired driver dependencies. Use code review and compilation rather than tests matching source text or helper names.

Acceptance requires the narrower exported interface, passing behavioral coverage at the agreed seams and explicit evidence that the resource and diagnostic guarantees above remain intact. Private helper layout is not an acceptance criterion.

## Out of Scope

- Removing admission control or replacing it with the connection pool or timeouts alone.
- Changing capacity, deadlines, retry policy, fairness, per-workflow reservations or priority scheduling.
- Measuring or claiming that 64 operations and eight connections are optimal for target load.
- Renaming `StartUnit`, relocating the entire database module, redesigning persistence operations or adding an alternative datastore.
- Changing schema, migrations, credentials, PostgreSQL privileges, Relay Quota accounting or Relay Session policy.
- Redesigning diagnostics or unrelated transaction and cancellation behavior.
- Modifying client runtimes, initiating implementation, creating a PR, publishing or deploying as part of this specification request.

## Further Notes

The admission mechanism passes the deletion test: deleting it would remove a real overload guarantee. The unnecessary exported callback forms do not provide comparable leverage. Keeping lifecycle policy inside the database module preserves locality while the PostgreSQL adapter presents the existing persistence seam to functional modules.

This specification preserves the accepted [persistence decisions](../persistence-repositories/decisions.md) and the [persistence specification](../persistence-repositories/spec.md), especially admitted operation lifetime, adapter ownership and testing. It does not reopen the client transport ADR or introduce a new domain concept.

Canonical publication is this relay-owned local tracker file. The current request authorizes specification creation only. Future implementation should use the repository's existing isolated spec workflow and verification procedure; this planning baseline must be rechecked before work starts.

Reference documents: [project operations](../../docs/agents/operations.md), [verification](../../docs/verification.md), and [glossary](../../GLOSSARY.md).
