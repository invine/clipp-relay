# Clipp Relay persistence repositories

**Status:** implementation complete on `codex/persistence-repositories-integration`; local verification and PostgreSQL 17/18 qualification passed on 2026-10-27.
**Owning repository:** relay.  
**Canonical feature directory:** relay:`.scratch/persistence-repositories/`.  
**Participating roles:** relay only; client transport behavior is preserved.  
**Inspected baseline:** `4e8239cdb6ddc87a5baca56b0522726e0015ca83`, 2026-10-26.

Functional modules depend on entity repositories and a storage-neutral transaction scope. The PostgreSQL adapter owns SQL, driver types, connection management and transaction mechanics. Business rules remain in Auth, Quota and their existing functional modules.

- [Specification](spec.md): ownership, complete entity coverage, transaction semantics and acceptance.
- [Decision coverage](decisions.md): the five user-approved choices and their relation to earlier decisions.
- [Implementation plan](implementation-plan.md): staged extraction, verification and completion requirements.

This effort expands architecture-review candidate 1 beyond escaped native OAuth reads to all application persistence, including presentation queries, Fosite storage, cleanup, deletion, recovery and credential reset. It preserves the existing database schema and observable behavior. The other architecture candidates are separate work.

The five design choices were approved in this conversation on 2026-10-26. All eleven implementation tickets are resolved in the integration branch. Verification evidence is recorded in [the ticket tracker](issues/README.md). This is local implementation and qualification evidence; it does not authorize publication or deployment. Keep this relay-owned feature directory canonical rather than duplicating its specification in the client repository.
