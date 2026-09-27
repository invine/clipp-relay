# 01: Isolate Clipp relay lifecycle

**What to build:** Keep Clipp networking behavior intact while giving relay-owned work an explicit lifecycle independent of the single libp2p host.

**Blocked by:** None (can start immediately).

**Status:** resolved

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

- [x] Existing startup/shutdown, reservation retry and exact Rendezvous tests pass through the public networking interface before and after the change.
- [x] Repeated start/stop and connection loss leave no duplicate timer/listener, leaked live relay work or duplicate registration.
- [x] Relay cleanup does not close direct peers, replace the host/Device Identity or change unrelated runtime behavior.
- [x] Characterization tests demonstrate signed records stay unchanged and membership/revocation checks are unaffected.
- [x] Shared/runtime type checks and npm check pass; document the narrow lifecycle interface and any temporary compatibility adapter.

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

- Claimed centrally on 2026-09-27 for Clipp implementation agent; isolated starting commit `44ddde1`. Resolution awaits integration review and acceptance evidence.

- Reviewed implementation `b2c3b6d` was merged into isolated Clipp integration branch as `c377c28`. Independent Spec and Standards reviews found no remaining material issue. On the merged result, Node.js 26.10.0/npm 11.19.1: full Jest 456/456 across 56 suites, lint, `git diff --check`, and the localhost pairing harness all passed. The same harness passed on an isolated `44ddde1` baseline after correcting its two stale `FaultTolerance` imports to the already installed package. Detailed commands and the stock Circuit Relay v2 ownership limit are in `packages/core/network/relay-lifecycle.md` in the Clipp integration worktree.
- Before baseline approval, completion remained pending the required shared/runtime type checks and `npm run check`: the pinned committed Clipp baseline `44ddde1` had no `check` script, and its broad direct TypeScript check already failed on existing errors. The earlier merged result had no `RelayLifecycle` TypeScript errors, but the ticket could not be marked resolved on that baseline. The primary Clipp checkout and its unrelated uncommitted changes remained untouched.

- The operator approved the exact isolated snapshot of the original 52 uncommitted Clipp changes as the starting baseline. Its tracked diff and untracked-file hashes matched the original byte for byte; `npm run check` passed 439 tests before the snapshot commit `abc751a`. The previously reviewed ticket branch was merged onto it as `be506bc`. A fresh Spec/Standards integration review found stale validation prose and a peer-store tag-removal failure that could interrupt first-call shutdown. A failing test reproduced the shutdown bug; the fix and both regression paths passed focused rechecks and were committed as `64c87b6`. On the final integrated branch with Node.js 26.10.0/npm 11.19.1, `npm run check` passed all shared/Electron/Android/extension type checks and 462/462 Jest tests across 56 suites; `npm run lint` passed with zero errors; the localhost pairing/network harness passed with bind permission; formatting and diff checks passed. The original Clipp checkout remains at `44ddde1` with its 52 changes untouched. Ticket 01 is resolved, unblocking ticket 12. No push, publication or deployment occurred.
