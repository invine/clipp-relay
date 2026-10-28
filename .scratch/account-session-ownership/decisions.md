# Accepted account and session ownership decisions

Status: ready-for-agent

Date: 2026-10-28 (Asia/Dubai).  
Source: this architecture-review conversation. The user selected candidates 1 and 2, answered both grilling rounds, and requested synthesis with the to-spec skill inside clipp-relay.

| Decision | Accepted choice | User evidence | Spec coverage |
| --- | --- | --- | --- |
| Q1 | Keep administrator account changes, Account-wide Revocation, and deletion admission distinct; share private Auth completion machinery. | “q1 yes, q2 yes” | I1–I4; T2, T7 |
| Q2 | Privately own connections, attempts, indexes, timers, reservations, and Rendezvous Leases inside the existing Relay Session module, without the whole-Server back-reference. | “q1 yes, q2 yes” | I5–I8; T3–T6 |
| Q3 | Return classified mutation outcomes for HTTP rendering, preserving refusal/conflict, completion, uncertainty, incomplete invalidation, and pending deletion. | “q3 ok, q4 yes, q5 ok” | I2–I4; T2 |
| Q4 | Consolidate session-ending causes into one manager-owned bounded cleanup mechanism; acknowledge logical invalidation synchronously and retain physical-close ownership in Relay. | “q3 ok, q4 yes, q5 ok” | I4–I6, I9; T3, T4, T6 |
| Q5 | Own complete semantic Rendezvous operations and all ownership rechecks in the session module; adapters retain framing and encoding. | “q3 ok, q4 yes, q5 ok” | I5, I7; T5 |
| Testing | Use existing composed HTTP and real libp2p operations, controlled adapters for deterministic races/delayed closes, and real PostgreSQL for storage guarantees. | “Yes, use these existing seams” in response to the to-spec testing check. | T1–T8 |

## Preserved decisions

- Persistence remains entity-oriented and storage-neutral to functional modules, with PostgreSQL as the production adapter. Auth retains functional transaction ownership and policy.
- Auth validates credentials without receiving physical connections; the Relay Session module owns exact-connection lifecycle and cleanup.
- Successful revocation includes logical invalidation, while physical closure may complete later. Closing remains terminal for permission.
- One effective authenticated Relay Session per Peer ID remains required, as do fixed expiry, renewal/replacement semantics, and client transport fallback.
- Quota remains separate from Login Grant lifecycle. Revocation does not reset pending or recorded usage.
- Deletion retains mandatory committed Recovery Journal proof, best-effort accounting, Retained Quota Usage, and sealing against late writes.

No accepted product invariant is intentionally changed. Concrete type declarations, method names, private organization, and worker counts remain implementation choices.

## Authorization and evidence

The final instruction was to prepare a specification with to-spec inside the relay repository. This record captures accepted design choices and testing seams; it does not assert that the prior request to begin implementation was confirmed. No implementation, test execution, commit, push, hosted run, deployment, or live qualification is established by this record.
