# 05: Manage account access and Quota Plans atomically

**What to build:** Administrators manage Relay Account access and Quota Plans, and owners invoke Account-wide Revocation, through atomic repository operations with required audit and live invalidation.

**Blocked by:** 03: Rotate Login Grants and handle refresh replay; 04: Register Relay Accounts and manage Portal Sessions.

**Status:** ready-for-agent

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I5, I9–I10; T1–T4, applicable T7–T8.

- [ ] Migrate complete administrator account actions, Quota Plan creation/archival/assignment, quota overrides and owner Account-wide Revocation to shared transaction-scoped repositories.
- [ ] Preserve administrator identity/allowlist checks, recent authentication, Origin/CSRF requirements, enumerated reasons, account/plan revisions and authoritative rechecks after logical guard waits.
- [ ] Keep plan assignment and first-assignment immutability atomic. Concurrent assignment, archival or policy edits must preserve current conflict and allowance behavior.
- [ ] Required account status/generation changes, Login Grant termination, Portal Session removal and audit commit together. Injected later-write failures cannot leave partial security changes.
- [ ] Preserve operation-specific uncertain-commit reconciliation and conservative fencing. Do not interpret a lost acknowledgement as confirmed rollback or report incomplete notification as successful revocation.
- [ ] Process required account/grant invalidation at the established logical-guard completion points independently of canceled initiating requests. Physical connection-close I/O remains outside logical guards and SQL transactions.
- [ ] Verify concurrent revision/plan changes, audit rollback, uncertain commit, session-cap reduction, account-wide versus grant-local scope and request cancellation through functional adapters and real PostgreSQL/relay composition.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
