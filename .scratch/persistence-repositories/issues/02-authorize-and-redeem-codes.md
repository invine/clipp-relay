# 02: Authorize native clients and redeem authorization codes

**What to build:** Native clients complete consent, authorization-code issuance and PKCE redemption through entity repositories and one shared transaction, receiving credentials only after confirmed completion.

**Blocked by:** 01: Introduce repository scopes through credential validation.

**Status:** claimed — persistence 02 (coordinator: root), 2026-10-26

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I6, I9–I10; T1–T4, applicable T8.

- [ ] Migrate the complete authorization and code-redemption workflows, including preliminary token/account reads, consent authority checks and the narrow account/Portal Session persistence reads required by those workflows.
- [ ] Code consumption, Login Grant creation, initial refresh/access-token records and required audit participate in the same outer Auth-owned transaction. Fosite callbacks use its abstract transaction-scoped repositories and cannot commit independently.
- [ ] Preserve registered public clients, redirect matching, S256 PKCE, credential formats, grant caps, stable Grant ID association and the existing account/credential-generation rechecks after waits.
- [ ] All migrated preliminary reads cross admitted work scopes. Retain guard ordering and independent confirmation envelopes without importing SQL, pool handles or driver types into the migrated functional path.
- [ ] Invalid PKCE and other ordinary refusals roll back; a subsequent valid redemption of the same code remains possible. A failed later write or audit cannot leave partially issued credentials.
- [ ] Distinguish confirmed commit, rollback, conflict, unavailable work and unresolved commit; do not return successful credentials before confirmation or automatically replay transaction callbacks.
- [ ] Verify real code/PKCE exchanges, concurrent redemption, rollback, lost acknowledgements and shared-budget refusal with controlled functional adapters and real PostgreSQL. Existing refresh and portal workflows remain operational during migration.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
