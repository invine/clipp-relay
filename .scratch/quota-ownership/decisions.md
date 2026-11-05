# Quota ownership decisions

Status: ready-for-agent

Date: 2026-11-05 (Asia/Dubai)  
Source: the Clipp Relay architecture-review conversation and the request to publish the final design using to-spec.

| Decision | Source | Disposition | Specification coverage |
| --- | --- | --- | --- |
| D1. Quota owns weekly usage and aggregate live Relay Session counts. | User said “I want quota to be responsible for both usage and live session count management. auth should not have any usage related data”. | Accepted. Auth retains account policy and authentication. Relay retains its exact connection registry. | I1, I2, I5, I6, I7 |
| D2. Auth hands committed limits to Quota. Quota decides admission and both quota-driven retirements. | User asked why Auth should reconcile through the session manager and requested the final interfaces after the handoff was revised to Auth → Quota. | Accepted for the spec. Quota uses a narrow Relay operation for live registry synchronization. Relay executes logical retirement and deferred cleanup. | I1–I4, T3–T5, T7 |
| D3. Protect both freshness windows. | User requested the final interfaces and tests for both race windows after reviewing cap reconciliation and final admission sequences. | Included. The design uses an in-memory policy epoch across the database read and Relay's existing registry lock to order application of a committed cap against final admission. The two possible registry lock winners each have a regression test. | I2–I5, T1–T6 |
| D4. Move Portal usage and deletion accounting out of Auth. | User explicitly requested both moves in the final design and in the specification. | Accepted. The Portal owns its protected usage read path. The deletion workflow and Quota own accounting handoff. Durable usage retention stays inside the existing account transaction. | I6, I7, T8–T10 |
| D5. Keep this effort focused on proposal 1. | User asked to target proposal 1 first and set the other improvements aside. | The terminal malformed-authentication and assignment picker proposals remain separate. Public Address Snapshot, native token exchange and the registry split remain skipped in this effort. | Out of Scope |

## Superseded directions

- The first sketch put cap application on the session manager. D1 and D2 place it in Quota and keep exact connection effects in Relay.
- The later sketch routed Auth through Relay to obtain the registry lock. D2 routes committed limits to Quota and has Quota call the narrow Relay reconciliation operation.
- The minimal stale-read fix alone would retain Auth's live count comparison. It supplies part of D3. D1 and D2 also remove that decision and data from Auth.
- The [earlier account-session ownership spec](../account-session-ownership/spec.md#i8-auth-quota-and-relay-collaboration) assigned live session-cap effects to Relay. This revision changes the owner of the account-level decision while preserving Relay's session lifecycle and execution duties. The persistence and transport decisions remain applicable.

## Evidence and authorization

The design and test requirements are recorded in the [specification](spec.md). The proposed test seams reuse the established composed HTTP and libp2p operations, controlled adapters and disposable PostgreSQL. On 2026-11-05, the user explicitly requested implementation of the reviewed ticket breakdown via `/implement-spec`; this authorizes local implementation, commits and verification for this scope. It does not authorize push, PR, publication, deployment, hosted CI or live acceptance.
