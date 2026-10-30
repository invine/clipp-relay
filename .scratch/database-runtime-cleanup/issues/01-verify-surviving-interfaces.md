# 01: Verify admission and database behavior through surviving interfaces

**What to build:** A passing behavioral test suite that demonstrates the existing admission, lifetime and diagnostic guarantees through persistence operations and the database runtime's surviving SQL methods. Tests no longer need exported `WithUnit` or `Unit`, allowing the cleanup to proceed without losing meaningful coverage or changing production behavior.

**Blocked by:** None (can start immediately).

**Status:** resolved

Parent: [database runtime cleanup specification](../spec.md). Owning repository: relay. Coverage: Implementation Decisions 4–9 and all behavioral Testing Decisions. This is the test preparation slice; exported method removal belongs to ticket 02.

- [x] Migrate every test use of `WithUnit` and `Unit` to the highest applicable existing interface: persistence operations for admission and cancellation, and surviving runtime SQL methods for database execution and resource lifetime. Keep production behavior and exported methods unchanged in this ticket.
- [x] Demonstrate that ordinary and settlement operations share one 64-operation budget: the next independent operation is promptly rejected with the existing classification, and capacity becomes available after closure. Preserve coverage for idempotent release and canceled parents.
- [x] Demonstrate nested ownership through existing operation and transaction scopes: nested work uses the final admitted slot without reserving another, retains the original deadline and cannot release its outer owner's slot. Preserve earlier parent cancellation and the established admission-before-guard, connection-after-wait ordering.
- [x] Preserve meaningful real PostgreSQL coverage for connection acquisition failure or exhaustion, SQL failure, release after failure, row scanning, row iteration/closure, transaction completion, operation deadlines and narrower statement cancellation. Preserve coverage of shared capacity across quota eligibility and cleanup reads.
- [x] Exercise observable diagnostic events through surviving interfaces. Verify database failure/recovery, caller-cancellation handling, no-row classification, policy neutrality and privacy. Admission alone must not establish database recovery. Do not add dummy successful SQL to a policy-only scenario, since that SQL can legitimately establish recovery.
- [x] Preserve coverage of the execution path's deadline and error behavior without introducing exported callbacks, production test hooks, a new clock interface or assertions tied to private helpers, context keys, channel internals or polling structure.
- [x] Replace helper-shaped assertions only after equivalent observable coverage exists. Reuse existing fixtures and tests; do not add a new harness or duplicate already sufficient coverage.
- [x] Run focused affected tests against the unchanged production implementation, including the database-backed tests whose setup or assertions changed. Record the exact revision, commands, fixture/version coverage and any failures or skips. Fixture-optional skips do not establish storage correctness; ticket 02 owns final routine/race and PostgreSQL 17/18 qualification of the completed cleanup.
- [x] Confirm by caller review and compilation that no test still depends on `WithUnit` or `Unit`, and no production behavior or resource policy was changed. Record any unrelated finding separately.

## Comments

- 2026-10-30: The user approved this two-ticket breakdown and explicitly requested publication. The existing persistence and runtime test seams were confirmed on 2026-10-29. Publication does not claim or start implementation.


- 2026-10-30: Integrated source `863d2d428eafe4545da768f8359deddf4e5a332c` unchanged on `codex/database-runtime-cleanup-integration`. The three changed test files contain no `WithUnit` or `.Unit(` callers; production is unchanged. With the existing disposable `scripts/smoke-postgres.sh` fixture (temporary copy filtering the auth invocation to the changed test), all `internal/database` tests passed on PostgreSQL 18.6; `TestQuotaEligibilityUsesItsOwnDatabaseBudget` and the full `internal/quota` package passed on PostgreSQL 18.6; PostgreSQL 17.11 migration/startup and TLS rejection checks passed. The unfiltered PostgreSQL 18.6 auth run failed two existing owner-revocation confirmation assertions (`TestOwnerAccountRevocationInvalidatesOnlyThatAccountsSessions`, `TestOwnerRevocationDeliverySurvivesRequestCancellationAfterCommit`); the focused changed auth test passed. Fixture-free `go test -count=1 ./internal/database ./internal/auth` could not complete because the sandbox blocks an existing httptest loopback bind. These unrelated failures and unavailable fixture-free profile are retained in the coordinator run record; ticket 02 owns final routine/race and PostgreSQL 17/18 qualification.
