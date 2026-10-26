# Approved persistence repository tickets

**Status:** implementation in progress on `codex/persistence-repositories-integration`.  
**Owning repository:** relay.  
**Approval:** on 2026-10-26 the user accepted the 11-ticket breakdown and blocking edges, then instructed: “I agree. Publish tickets, don't start implementation”.

The [approved specification](../spec.md) and [decision record](../decisions.md) govern every ticket. The [implementation plan](../implementation-plan.md) describes the broader extraction sequence; the approved ticket graph below is the publishable breakdown of that plan. Publication is explicitly authorized before an implementation run and does not establish one. Parent documents are preserved.

## Tickets and blocking edges

| Ticket | Blocked by | Delivers |
| --- | --- | --- |
| [01: Introduce repository scopes through credential validation](01-introduce-repository-scopes.md) | None | Relay Discovery and Relay Authentication validate existing Relay Access Tokens through backend-neutral repository and transaction interfaces, with the production adapter enforcing shared work limits and classified outcomes while existing workflows remain operational. |
| [02: Authorize native clients and redeem authorization codes](02-authorize-and-redeem-codes.md) | 01 | Native clients complete consent, authorization-code issuance and PKCE redemption through entity repositories and one shared transaction, receiving credentials only after confirmed completion. |
| [03: Rotate Login Grants and handle refresh replay](03-rotate-grants-and-handle-replay.md) | 02 | Clipp clients refresh credentials through transaction-scoped repositories, with ordinary rotation preserving the Login Grant and authenticated replay revoking only the affected grant after durable confirmation. |
| [04: Register Relay Accounts and manage Portal Sessions](04-register-accounts-and-portal-sessions.md) | 01 | Google sign-in creates or opens the correct Relay Account and Portal Session through repositories; re-registration imports eligible Retained Quota Usage exactly once without restoring deleted account state. |
| [05: Manage account access and Quota Plans atomically](05-manage-account-access-and-plans.md) | 03, 04 | Administrators manage Relay Account access and Quota Plans, and owners invoke Account-wide Revocation, through atomic repository operations with required audit and live invalidation. |
| [06: Record and reconcile Relay Usage](06-record-and-reconcile-relay-usage.md) | 01 | Relay Accounts share correctly recorded original-week usage through repository-backed eligibility and settlement, retaining safe retries and independent exhaustion/accounting-health cutoffs. |
| [07: Delete Relay Accounts without losing retention guarantees](07-delete-accounts-with-retention.md) | 05, 06 | Owners delete Relay Accounts through repository-backed admission, journal progress and atomic finalization, preserving privacy-minimal usage and refusing late charges against deleted identities. |
| [08: Recover accounts and reset credentials through repositories](08-recover-accounts-and-reset-credentials.md) | 07 | Operators run offline account recovery, issuer-key verification and explicit credential reset through persistence interfaces without changing recovery authority, idempotency or maintenance privileges. |
| [09: Read coherent owner and administrator views](09-read-owner-and-admin-views.md) | 01 | Owners and administrators see coherent account, quota and history views through typed read interfaces, while live capacity remains a separately sampled observation. |
| [10: Run bounded retention and cleanup through repositories](10-run-bounded-retention-and-cleanup.md) | 01 | Background maintenance expires eligible records and reports cleanup health through bounded repository batch operations while preserving fair progress and privacy retention. |
| [11: Remove compatibility routes and qualify the final composition](11-remove-compatibility-and-qualify.md) | 08, 09, 10 | The complete application starts with the finished PostgreSQL adapter and every scoped functional workflow is storage-independent, with final integrated evidence and no remaining compatibility escape route. |

## Reading the graph

Only ticket 01 has no predecessor. After 01, tickets 02, 04, 06, 09 and 10 have their prerequisite satisfied; 03 follows 02, 05 joins 03 and 04, 07 joins 05 and 06, 08 follows 07, and 11 joins 08, 09 and 10. These are prerequisite edges, not dispatch instructions. Dependencies inherited transitively are not repeated.

The sequence expands the persistence seam through a real credential-validation path, migrates complete workflows while compatibility keeps the application executable, then contracts the old routes. One operation must not mix physical transactions or independent work budgets from old and new implementations. Shared source edits require coordination rather than invented dependency edges.

## Verification and scope

Every ticket preserves the existing schema, credential protocols, business policy, single-process topology and one-effective-session-per-Peer-ID contract. Controlled repository adapters verify functional decisions; real disposable PostgreSQL establishes persistence correctness. Preserve composed HTTP/libp2p tests and classify actual PostgreSQL execution separately from fixture-optional skips.

| Specification acceptance | Ticket coverage |
| --- | --- |
| T1 Shared resource admission | 01–07, 09–11 as applicable |
| T2 Atomic entity changes | 02–08, 11 |
| T3 Commit uncertainty | 01–08, 10 where applicable, 11 |
| T4 Credential and revocation behavior | 01–03, 05, 11 |
| T5 Accounting | 06–07, 11 |
| T6 Deletion and recovery | 04, 06–08, 10 where applicable, 11 |
| T7 Queries and maintenance | 05, 07–11 as applicable |
| T8 Structural completion | Migrated paths in 01–10; complete inventory in 11 |
| T9 Integrated verification | 11; focused real verification accompanies each slice |

Tickets 01–06 and 09–10 are resolved. Ticket 07 is active; tickets 08 and 11 remain ready subject to their blocking edges. The user's implementation instruction supersedes the earlier publication-only request. PostgreSQL 17/18 qualification remains explicitly pending in ticket 11.
