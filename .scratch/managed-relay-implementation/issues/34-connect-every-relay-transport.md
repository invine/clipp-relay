# 34: Connect every available relay transport

**What to build:** Connect every available relay transport.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md).

**Status:** claimed

Repository scope: Clipp.
Source: user instruction on 2026-10-05, "application should connect to every available relay transport, not just to the first one"; [accepted specification](../../managed-relay-service/spec.md), I4/I7, amended by this instruction.

## Contract and scope

Maintain an authenticated physical connection for each supported, advertised relay transport family: TCP, WSS and WebRTC Direct. Runtime support determines eligibility; acceptance-only forced selection still isolates one family. Try address alternatives within a family with bounded concurrency; a failed family retries independently and never closes healthy families. Keep one host and Device Identity, endpoint-scoped single-flight credential refresh, expected-peer verification before tokens, and owned-resource cleanup. Stock per-peer Circuit Relay reservation and Rendezvous ownership must remain coherent: healthy connections remain available and owner loss can promote another authenticated transport. Preserve aggregate ready/degraded semantics and expose safe per-transport state. Config removal, replacement, revocation and stop release every owned connection without affecting direct peers or other relays.

## Acceptance criteria

- [ ] Demonstrate simultaneous supported transport connections and independent failure recovery at the approved public seams.
- [ ] Preserve identity, credentials, admission limits and unrelated connections.
- [ ] Pass focused TDD checks, repository verification and independent Standards/Spec reviews.
- [ ] Integrate and verify locally before central resolution; distinguish local protocol evidence from external deployment acceptance.

## Comments

- Centrally claimed 2026-10-05 after the listed predecessor was verified resolved. Fresh context and isolated worktree required. Coordinator owns canonical claims, integration and resolution. No push, publication, deployment or shared-cluster mutation; capacity ticket 33 remains deferred.
