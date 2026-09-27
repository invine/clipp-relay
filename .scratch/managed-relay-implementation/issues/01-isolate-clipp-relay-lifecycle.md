# 01: Isolate Clipp relay lifecycle

**What to build:** Keep Clipp networking behavior intact while giving relay-owned work an explicit lifecycle independent of the single libp2p host.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

Repository scope: Clipp.
Source: [Accepted specification](../../managed-relay-service/spec.md), I7; Testing.

## Contract and scope

Work in shared core first. Extract the existing relay dialing, reservation and
Rendezvous ownership into a cohesive internal component without changing public
behavior. Preserve Device Identity, direct peers, trust/membership checks, signed
record forwarding and existing runtime bridges. Expose only the complete lifecycle
operations later dynamic configurations need; do not create a generic network
abstraction or mocks for every helper.

Keep current callers green throughout this prefactor. If compatibility adapters
are needed, retain them until each runtime switches in its own integration ticket.
Do not import old settings into the new model, remove defaults, change secure
storage or activate managed authentication here. Those are later feature changes,
not part of this behavior-preserving slice.

## Acceptance criteria

- [ ] Existing startup/shutdown, reservation retry and exact Rendezvous tests pass through the public networking interface before and after the change.
- [ ] Repeated start/stop and connection loss leave no duplicate timer/listener, leaked live relay work or duplicate registration.
- [ ] Relay cleanup does not close direct peers, replace the host/Device Identity or change unrelated runtime behavior.
- [ ] Characterization tests demonstrate signed records stay unchanged and membership/revocation checks are unaffected.
- [ ] Shared/runtime type checks and npm check pass; document the narrow lifecycle interface and any temporary compatibility adapter.

## Demonstration

Run the same existing relay/network harness before and after; compare observable connections, registration and shutdown, not private method calls.

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

