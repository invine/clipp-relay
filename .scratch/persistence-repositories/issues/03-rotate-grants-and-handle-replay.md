# 03: Rotate Login Grants and handle refresh replay

**What to build:** Clipp clients refresh credentials through transaction-scoped repositories, with ordinary rotation preserving the Login Grant and authenticated replay revoking only the affected grant after durable confirmation.

**Blocked by:** 02: Authorize native clients and redeem authorization codes.

**Status:** claimed — persistence 03 (coordinator: root), 2026-10-26

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I6, I9–I10; T1–T4, applicable T8.

- [ ] Migrate preliminary refresh lookup, authoritative account/grant rechecks, refresh-generation consumption, successor credentials and required audit to the same abstract transaction and shared admission scope.
- [ ] Preserve ordinary rotation semantics, previous access-token validity until expiry, stable Grant ID association, client binding, idle/absolute expiry and concurrent refresh protections.
- [ ] Represent ordinary protocol refusal separately from authenticated replay whose revocation and audit must commit. Fosite callback refusal must not accidentally roll back required replay consequences.
- [ ] Retire the failed transaction before bounded independent reconciliation, preserving required logical protection and the distinct confirmation envelope. Do not blindly retry ambiguous refreshes or treat cancellation as proof of rollback.
- [ ] After confirmed replay revocation, process required grant invalidation independently of the initiating request cancellation. Unrelated established grants/sessions remain usable, and grant invalidation neither changes quota nor discards observations.
- [ ] Preserve both authentication/revocation race orderings, returned-but-unapplied validation cancellation and original expiry on canceled unrelated renewal through the existing Session Manager contract.
- [ ] Verify successful rotation, concurrent refresh, replay commit/audit, known rollback, committed-but-lost acknowledgement, unresolved confirmation and incomplete notification through functional adapters and real PostgreSQL/relay composition.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
