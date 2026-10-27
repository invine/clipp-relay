# Account mutation completion and Relay Session ownership

Status: ready-for-agent

Owning repository: relay.  
Canonical feature directory: relay:`.scratch/account-session-ownership/`.  
Scope: architecture-review candidates 1 and 2.  
Implementation: not started; eight approved tickets published, all unclaimed.

- [Specification](spec.md): problem, complete user stories, implementation decisions, testing decisions, and scope.
- [Accepted decisions](decisions.md): Q1–Q5 and the explicit testing confirmation.
- [Specification run](run.md): baseline, branch/worktree lineage, and evidence limits.
- [Approved tickets](issues/README.md): eight implementation slices, blocking edges, and spec coverage.

## Source wayfinding

These are inspected baseline anchors, not fixed file-placement requirements:

| Concern | Current source |
| --- | --- |
| Auth protection and account-wide completion | `internal/auth/auth.go` |
| Administrator account mutations | `internal/auth/admin.go` |
| Deletion admission and later finalization | `internal/auth/deletion.go` |
| Credential validation | `internal/auth/relay.go` |
| Session state, invalidation, authentication, drain, cleanup | `internal/relay/server.go` |
| Reservation and Rendezvous ownership | `internal/relay/rendezvous.go` |
| Traffic attribution and account detachment | `internal/relay/reporter.go` |
| Accounting ownership and health | `internal/quota/` |
| Repository contracts and PostgreSQL adapter | `internal/persistence/` |
| Mutation outcomes and invalidation coverage | `internal/auth/admin_consistency_test.go`, `internal/auth/protected_wire_test.go` |
| Session races, protocol compatibility, attribution | `internal/relay/auth_wire_test.go`, `internal/relay/rendezvous_wire_test.go`, `internal/relay/reporter_wire_test.go` |
| Deletion and graceful accounting coverage | `internal/auth/deletion_test.go`, `internal/auth/drain_accounting_test.go` |

## Related authority

- [Domain glossary](../../GLOSSARY.md).
- [Relay contract](../../README.md).
- [Accepted persistence decisions](../persistence-repositories/decisions.md).
- [Persistence specification](../persistence-repositories/spec.md).
- [Accepted recovery contract](../managed-relay-service/recovery-contract.md).
- [Periodic accounting design](../periodic-relay-accounting/design.md), read with the newer Relay Session decisions: older grant-polling and caller-managed admission clauses are superseded.
- [Project operations](../../docs/agents/operations.md) and [verification](../../docs/verification.md).
- Relay:`.scratch/relay-session-lifecycle/spec.md` is the accepted prior Relay Session specification. It was present as an untracked source in the selected primary checkout at preparation time, so it is not inherited by this clean worktree. Resolve it through the explicit local root mapping in the run record; do not assume a nonexistent relative link or copy a competing canonical specification.
- Client:`docs/adr/0012-relay-transport-fallback-and-concurrent-sessions.md` preserves single-session transport fallback. Resolve the client role explicitly using the run record.
- Client:`docs/agents/issue-tracker.md` and `docs/agents/triage-labels.md` define local Markdown publication and the `ready-for-agent` status; client:`docs/agents/workflow.md` defines spec isolation and later implementation coordination.

Implementation should use this spec's integration lineage and the approved ticket dependencies. The earlier architecture review's process-lifecycle and Recovery Journal proof proposals are not part of this feature.
