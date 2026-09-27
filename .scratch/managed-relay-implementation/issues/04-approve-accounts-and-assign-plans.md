# 04: Approve accounts and assign plans

**What to build:** An allowlisted administrator approves a Pending account with a plan and users can see their assigned allowance.

**Blocked by:** [03: Register and sign in](03-register-and-sign-in.md).

**Status:** resolved

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

- [x] Authoritative allowlisted admin can approve itself and another Pending account with a plan; ordinary/third-party non-authoritative email is denied.
- [x] Removing/malforming allowlist affects the next request after observation without restarting relay; revision is observable and no old permission retained.
- [x] Approval without plan, stale revision, stale Google auth, invalid Origin/CSRF and invalid allowance are refused without mutation.
- [x] Create/assign/archive and replacement-plan behavior are demonstrated through portal with real DB and immutable assigned values.
- [x] Mutation and audit rollback together on injected audit failure; audit fields contain only permitted actor/target/reason data.
- [x] Account/plan lists obey stable bounded pagination, owners see no admin identities/reasons, and default allowance is exactly 1,073,741,824 bytes/5 sessions.

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

- Claimed centrally on 2026-09-27 after ticket 03 resolution (`b0a3bb6`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.

- Resolved on 2026-09-27 after local implementation commits `3aa24b3`, `528d0d8`, and `1015114`, independent Spec and Standards review passes, merge `19e1c99`, and service README correction `c138bc6`. On the merged result, Go 1.27.1 darwin/arm64: `go test -count=1 ./...`, `go vet ./...`, `go build ./cmd/clipp-relay`, and `git diff --check` passed. Disposable `bash scripts/smoke-postgres.sh` passed with verified-TLS/SCRAM PostgreSQL 18.6 and 17.11, rejected 16.15, ran real PostgreSQL browser/admin tests on 18, and checked migration revision 4, ownership guards, health, and outage behavior. Tests cover live allowlist policy, self/other approval, exact baseline plan, immutable/replacement/archived plans, bounded lists, stale revisions and Google auth after lock wait, CSRF/Origin, atomic audit rollback, one-statement owner policy read, and one-plan/one-audit recovery from ambiguous or canceled commit acknowledgment. No production target was used.
