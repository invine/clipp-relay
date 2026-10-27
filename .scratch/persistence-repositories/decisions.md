# Approved persistence decisions

**Status:** agreed design, 2026-10-26.  
**Source:** this architecture-review conversation. The user expanded candidate 1, accepted Q1 and Q3, requested an explanation of Q2, then accepted Q2 and subsequently Q4–Q5.

| ID | User decision | Specification coverage |
| --- | --- | --- |
| D1 | Repositories follow domain entities; supporting records have explicit ownership without requiring a CRUD module for every table. User: “1 and 3 agree.” | I1–I2; ownership inventory |
| D2 | Functional modules coordinate several repositories in a shared storage-neutral transaction scope; the adapter owns locking, commit, rollback and uncertain-commit classification. Accepted after the suspension example and transaction explanation: “Agree.” | I3–I5; T1–T4 |
| D3 | Scope includes presentation queries, cleanup, deletion, recovery and Fosite persistence, alongside normal account/quota operations. User: “1 and 3 agree.” | I2, I6–I9; T5–T8 |
| D4 | Repository operations express meaningful persistence intent and preserve entity invariants; business policy stays in functional modules. User accepted Q4–Q5: “Agree.” | I1, I4, I6; T1–T8 |
| D5 | PostgreSQL is the production adapter. Controlled repository adapters test functional behavior; real PostgreSQL establishes storage correctness. No complete second datastore is required just for testing. User accepted Q4–Q5: “Agree.” | I10; T1–T9 |

## Effect on earlier decisions

The earlier [module and PostgreSQL decision](../managed-relay-service/issues/14-define-go-modules-and-postgresql-persistence.md), Q215–Q216, already places SQL in a concrete PostgreSQL adapter and keeps business behavior in deep modules. This new user instruction authorizes backend-neutral entity repository and transaction interfaces, superseding the earlier restriction against a production datastore abstraction only to that extent. It does not adopt generic CRUD per table, an ORM, a second production backend or independent repository commits.

The [native authentication specification](../auth-modernization/spec.md) keeps the complete Auth operation responsible for the transaction and prohibits independent Fosite callback commits. Repository extraction changes the representation of that collaboration, not the atomicity or protocol contract.

The newer [Relay Session lifecycle specification](../relay-session-lifecycle/spec.md) continues to govern grant/account invalidation, pending attempt cancellation, session application and Auth/Quota separation. Its notification contract supersedes conflicting older admission/lifetime clauses in the [periodic accounting design](../periodic-relay-accounting/design.md). This persistence refactor does not restore those older clauses.

The accepted client ADR `client:docs/adr/0012-relay-transport-fallback-and-concurrent-sessions.md` remains unchanged. Resolve the selected client checkout explicitly. Relay Accounts remain separate from Device Identities and Device Network membership; one effective Relay Session per Peer ID remains the admission contract.

Repository, transaction scope and adapter are implementation concepts. No new business concept or glossary definition is introduced. This coverage record is the linked decision record for the expanded persistence direction; earlier historical records remain intact.
