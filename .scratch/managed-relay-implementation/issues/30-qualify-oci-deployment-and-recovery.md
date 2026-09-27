# 30: Qualify OCI deployment and recovery

**What to build:** Qualify the recorded candidate in an explicitly authorized isolated OCI environment, including real network, storage and recovery behavior.

**Blocked by:** [29: Produce a verified multi-architecture candidate](29-produce-a-verified-multi-architecture-candidate.md).

**Status:** ready-for-agent

Repository scope: Both repositories; authorized isolated OCI environment.
Source: [Accepted specification](../../managed-relay-service/spec.md), I9–I12; Testing.

## Contract and scope

Obtain operator-provided isolated namespace, architecture/nodes, OCI NLB/NSG, F5, DNS/TLS, enforcing NetworkPolicy engine, storage/trust, database, repository/IAM, registered clients and test credentials before environment work. This ticket authorizes no infrastructure provisioning or production fault injection. Missing inputs are not-run blockers, not permission to relax TLS, broaden network policy or invent evidence.

Exercise external TCP, WSS and WebRTC Direct from actual clients; force paths so direct fallback cannot mask failure. Verify exact-host portal/WSS separation, private operations, proxy/Origin/F5 logging controls, direct-Pod TCP/UDP NLB health, instant-failover setting, publication ordering and address-watch permissions. Prove default-deny enforcement including NAT/host-network behavior and required egress only. Readiness is local routing/publication rather than public reachability or DB health. Exercise nonoverlapping restart/drain and uncertain-node fencing without treating forced deletion as proof.

Cover supported external PostgreSQL 17/18 and bundled PostgreSQL 18: verify-full TLS/SCRAM, role/PUBLIC permissions, first-init/bootstrap/watcher/backup cycle, wrong/empty expected PVC refusal, real Longhorn non-root permissions and retention, leaf/password/CA rotation, and sidecar-denied PGDATA writes/superuser/bootstrap-secret access. Validate per-container resources, node allocatable capacity and actual log/writable-layer/shared-volume accounting; don't automatically expand or alter production budgets.

Perform timed off-cluster full/WAL/journal recovery to an isolated separate target after simulated DB-volume/cluster loss with OCI region/repositories/current operator access surviving. Measure RPO from proven recoverable state (target 15m), RTO through safe reopening including account review (target 4h), reporting portal/ready/held accounts separately. Exercise missing WAL/keys/events, delete-then-re-register, ambiguous journal boundaries and interrupted cleanup/late-worker cases. Verify first/pre-change/post-restore baselines and protected retained copies. Failure stays closed even beyond target; region-loss/privileged-compromise recovery is not claimed.

## Acceptance criteria

- [ ] Attach authorization/environment inventory and candidate digest/schema/config identities; all fault targets are isolated and explicitly within scope.
- [ ] Prove every enabled public transport externally, exact F5/host/proxy separation, private operations, watch RBAC and enforcing ingress/egress/NAT behavior.
- [ ] Execute publication/restart/drain/fencing and maintenance routing cases without overlapping relay processes, automatic public self-dial or false healthy dependency evidence.
- [ ] Verify real PostgreSQL versions/modes, bootstrap/role/TLS failures, cert/password/CA rotations, wrong/empty PVC refusal and Longhorn permission/retention behavior.
- [ ] Prove backup/watcher least privilege and per-container SQL/resource/storage budgets in the actual Pod/node environment, including log rotation and scratch accounting.
- [ ] Perform timed real off-cluster backup/WAL/journal restore with credential invalidation, deletion reconciliation, once-only recovery usage classification and explicit review holds; record actual RPO/RTO.
- [ ] Fault missing evidence and interrupted cleanup/restore safely; show fail-closed behavior and fresh baseline before any isolated cutover.
- [ ] Publish passed/failed/not-run evidence and residual operator inputs; recurring restore exercises are documented before production, quarterly and after material backup/storage/key/schema changes.

## Demonstration

In the isolated OCI deployment, force each external transport, rotate the database leaf and perform a timed restore into a separate target. Show that a deleted account cannot return and an intentionally missing WAL/event keeps reopening blocked.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

