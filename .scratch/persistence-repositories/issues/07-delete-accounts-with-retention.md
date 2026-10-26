# 07: Delete Relay Accounts without losing retention guarantees

**What to build:** Owners delete Relay Accounts through repository-backed admission, journal progress and atomic finalization, preserving privacy-minimal usage and refusing late charges against deleted identities.

**Blocked by:** 05: Manage account access and Quota Plans atomically; 06: Record and reconcile Relay Usage.

**Status:** ready-for-agent

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I5, I8–I10; T1–T3, T5–T6, applicable T7–T8.

- [ ] Migrate complete deletion admission/status, bounded pending capacity, retry/progress persistence, journal-proof coordination and finalization through the relevant entity repositories.
- [ ] Keep credential invalidation and required local session effects, independent Recovery Journal proof, bounded best-effort final accounting and finalization locks in their accepted order. Journal effects never execute inside SQL transactions.
- [ ] After proof, final accounting occurs outside finalization row locks. Failed or unresolved final accounting alone cannot prevent deletion completion.
- [ ] Capture confirmed current-week retained usage, detach identity, record required audit, complete the deletion operation and update pending capacity in one transaction serialized against settlement.
- [ ] Preserve journal authority and proof rules, retained-key matching, original account identity isolation and the already migrated fresh-registration import contract. Never preserve a durable Device Identity registry.
- [ ] Reconcile lost preparation/finalization acknowledgements with stable operation identity and bounded independent confirmation. Do not double-complete capacity, erase unrelated data or claim an unresolved deletion succeeded.
- [ ] Verify proof failure/retry, later-write rollback, failed final flush, committed settlement with a lost reply, finalization uncertainty, concurrent registration and late old-account settlement using controlled journal/repository faults and real PostgreSQL.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
