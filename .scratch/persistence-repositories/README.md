# Clipp Relay persistence repositories

**Status:** design agreed; implementation not started.  
**Owning repository:** relay.  
**Canonical feature directory:** relay:`.scratch/persistence-repositories/`.  
**Participating roles:** relay only; client transport behavior is preserved.  
**Inspected baseline:** `4e8239cdb6ddc87a5baca56b0522726e0015ca83`, 2026-10-26.

Functional modules depend on entity repositories and a storage-neutral transaction scope. The PostgreSQL adapter owns SQL, driver types, connection management and transaction mechanics. Business rules remain in Auth, Quota and their existing functional modules.

- [Specification](spec.md): ownership, complete entity coverage, transaction semantics and acceptance.
- [Decision coverage](decisions.md): the five user-approved choices and their relation to earlier decisions.
- [Implementation plan](implementation-plan.md): staged extraction, verification and completion requirements.

This effort expands architecture-review candidate 1 beyond escaped native OAuth reads to all application persistence, including presentation queries, Fosite storage, cleanup, deletion, recovery and credential reset. It preserves the existing database schema and observable behavior. The other architecture candidates are separate work.

The five design choices were approved in this conversation on 2026-10-26. The plan contains implementation choices derived from those decisions; it is not evidence that code was implemented, tests passed or deployment occurred. No implementation tickets have been claimed or dispatched. Follow the selected client checkout's `docs/agents/workflow.md` for any later implementation run, keeping this relay-owned feature directory canonical rather than duplicating its specification in the client repository.
