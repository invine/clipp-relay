# 04: Register Relay Accounts and manage Portal Sessions

**What to build:** Google sign-in creates or opens the correct Relay Account and Portal Session through repositories; re-registration imports eligible Retained Quota Usage exactly once without restoring deleted account state.

**Blocked by:** 01: Introduce repository scopes through credential validation.

**Status:** claimed — persistence 04 (coordinator: root), 2026-10-26

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I5, I8–I10; T1–T3, applicable T6–T8.

- [ ] Migrate complete Google callback persistence, account lookup/creation, Portal Session creation/validation/renewal and portal-only logout to backend-neutral repository operations.
- [ ] Preserve immutable issuer-and-subject identity, verified presentation metadata, Pending-account eligibility, existing provider verification and RAM-only unfinished continuations. Google network effects stay outside repository transactions.
- [ ] Create the account, required account audit, Portal Session and applicable retained-usage opening total atomically, with existing identity serialization and authoritative checks after waits.
- [ ] Retained usage checks all required key versions, rejects duplicate logical matches and consumes the retained record exactly once in the fresh Pending account transaction. Old plan, credentials and presentation do not return.
- [ ] Preserve Portal Session generation/expiry/CSRF semantics and portal-only logout scope. This ticket does not turn portal logout into Account-wide Revocation.
- [ ] Preserve classified commit uncertainty and existing account/portal confirmation behavior without leaking raw SQL, driver hooks or database mechanics into the migrated workflows.
- [ ] Verify new and returning sign-in, concurrent registration, session expiry/logout, later-write rollback, lost commit acknowledgement and one-time retained-usage import using controlled provider/repository adapters and real PostgreSQL.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
