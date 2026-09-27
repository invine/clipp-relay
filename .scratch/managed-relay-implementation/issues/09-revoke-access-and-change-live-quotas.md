# 09: Revoke access and change live quotas

**What to build:** An owner or administrator can invalidate account access and change policy with predictable immediate effects on live clients.

**Blocked by:** [07: Relay authenticated TCP traffic](07-relay-authenticated-tcp-traffic.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I2, I3, I5, I6.

## Contract and scope

Complete state transitions Pending→Active with plan or Denied; Active→Suspended/
Denied; Suspended→Active/Denied; Denied→Pending then separate approval. No automatic
transition. Blocking revokes all grants/access/portal sessions and closes live relay
sessions within10s. Reactivation restores none. Owner Sign out everywhere and
admin Revoke credentials do same without changing status/plan/usage; portal logout
remains current-session-only. No individual grant API or live grant association.
Grant-replay revocation leaves preauthenticated sessions until fixed expiry.

All admin mutations recent Google≤10m/current authoritative allowlist, revision/
Origin/CSRF and enumerated reason. Owner revocation valid Portal Session.
Commit mutation+audit before success/invalidation; unknown commit closes/fences
conservatively, reports uncertainty, reconciles before admission, never resurrects.
Identity guard OR peer guard (not nested), sorted involved account guards/rows,
brief registry update with no I/O; DB work admission before guards.
Advance credential generation for security mutation, not ordinary quota edit.
Fence older known/unknown identity login attempts, overflow cancels unfinished
flows before reclaiming required fences, no unrelated established invalidation.

Quota plan reassign/override applies current week, no reset. Below committed:
discard unused credit and close all; equality: allow funded remainder, no more
allocation. Session cap below count closes all then reconnect up to cap; zero
not unlimited. Close≤10s, don't revoke credentials for quota change, no later refund.
Show exact owner state/generic explanation, keep admin reason/audit private.

## Acceptance criteria

- [ ] Portal actions drive real clients to closure within10s for revoke/suspend/deny, with committed audit and no remaining valid old credential/flow.
- [ ] All allowed/forbidden state transitions and self-admin approval work; stale revision/fresh-auth/CSRF failures mutate nothing.
- [ ] Quota reduction below/equal committed and below current sessions follows distinct specified behavior, preserves usage and grants, and never selects arbitrary survivor.
- [ ] Concurrent issuance/authentication/replacement versus security mutation cannot install authority after committed invalidation; Go race tests and real SQL locks verify ordering.
- [ ] Lost mutation commit response produces uncertain UI plus conservative fencing; reconciliation never revives a closed session or reports false success.
- [ ] Initiating owner cookie clears after committed Sign out everywhere; normal logout and automatic one-grant replay response remain distinct.

## Demonstration

Open two account sessions, lower the session cap, reconnect, suspend/reactivate and perform Sign out everywhere while observing portal state, DB audit and live connection closure.

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

- Claimed centrally on 2026-09-27 after ticket 07 resolution (`49eed87`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.
