# 04: Approve accounts and assign plans

**What to build:** An allowlisted administrator approves a Pending account with a plan and users can see their assigned allowance.

**Blocked by:** [03: Register and sign in](03-register-and-sign-in.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I2, I6, I11.

## Contract and scope

Admin role is current Secret-backed authoritative-email allowlist, not DB roles
or IdP custom claims. Require verified Google email and Gmail or hd-present
Google-authoritative address; trim/ASCII-fold exact matching only, no dot/plus
aliasing. Check latest validated metadata and current list on every request.
Admin authority independent of account status; self-approval allowed.
Live reload: empty grants nobody, observed malformed/unreadable disables admin,
expose applied nonsecret revision, no immediate-projection promise.

Recent Google auth ≤10m for mutations, Origin/CSRF, optimistic revision conflicts
and structured reasons only: routine_administration, policy_enforcement,
suspected_abuse, security_response, support_correction. Account policy and audit
commit atomically. Allow Pending→Active only with plan, or Pending→Denied;
later live-control ticket completes all blocked/reactivation transitions.

Plan allowances are nonnegative weekly bytes/session count, zero not unlimited.
Seed/provide initial administrator-managed baseline 1GiB/week and 5 sessions.
Once assigned allowances immutable; replacement/explicit assignment rather than
mass edit, archive prevents new assignments while old assignments remain valid.
No client-selected quota. Accepted admin layout: service totals/account table/
selected editor in common light shell, not final visual polish. Stable pagination
50 default/100 max and 1MiB encoded dynamic body, bounded query/serialization.

## Acceptance criteria

- [ ] Authoritative allowlisted admin can approve itself and another Pending account with a plan; ordinary/third-party non-authoritative email is denied.
- [ ] Removing/malforming allowlist affects the next request after observation without restarting relay; revision is observable and no old permission retained.
- [ ] Approval without plan, stale revision, stale Google auth, invalid Origin/CSRF and invalid allowance are refused without mutation.
- [ ] Create/assign/archive and replacement-plan behavior are demonstrated through portal with real DB and immutable assigned values.
- [ ] Mutation and audit rollback together on injected audit failure; audit fields contain only permitted actor/target/reason data.
- [ ] Account/plan lists obey stable bounded pagination, owners see no admin identities/reasons, and default allowance is exactly 1,073,741,824 bytes/5 sessions.

## Demonstration

Register a Pending user, sign in as allowlisted admin, create/select a plan and approve; reload owner profile and demonstrate a conflicting concurrent admin edit.

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

