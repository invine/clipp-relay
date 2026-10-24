# 01: Make protected account operations explicit

**What to build:** Preserve existing authentication and account-policy behavior while making Auth own complete protected mutations and Relay own physical session operations. Callers and tests use the same production composition, with required cooperation explicit instead of silently optional.

**Blocked by:** None (can start immediately).

**Status:** claimed

Parent: [Relay, Quota and Auth specification](../spec.md), especially I1, I2 and I6. User stories: 20, 23–29, 39–42. Breakdown approved 2026-10-24.

- [ ] Auth owns mutation completion: authoritative rechecks, shared account protection, commit outcomes, uncertainty fencing, local invalidation and deferred cleanup. Owner revocation, administrative mutations and deletion admission use that workflow.
- [ ] Required database work admission, account protection and invalidation collaborators are explicit and available before serving. Remove optional discovery paths that can skip correctness requirements.
- [ ] Database work is admitted before logical-guard waits, and nested work reuses its budget. Preserve established lock ordering, including sorted protection of both accounts during cross-account same-peer replacement.
- [ ] Account protection spans the authoritative recheck, quota eligibility and session installation. An invalidation defeats authentication already in flight; an outer admission failure removes any newly installed session.
- [ ] Relay detaches session authority under protection and closes connections after releasing logical guards. No provider, object-storage or connection-close I/O occurs under database locks or the registry mutex.
- [ ] Startup wiring connects complete operations without deciding quota or session-cap policy. No generic forwarding coordinator is introduced.
- [ ] Commands encapsulate the protected operation; presentation queries cannot replace authoritative admission checks. Preserve exact-connection gating, failed renewal, one-session-per-Peer-ID replacement, grant-local termination and existing preauthentication allowances.
- [ ] HTTP/libp2p tests with real PostgreSQL exercise uncertain mutation, authentication racing invalidation, session-cap reduction and replacement at capacity using production composition rather than copied policy wiring.
- [ ] Existing prepaid accounting behavior remains intact in this prefactor. Do not start a separate prepaid Quota redesign. Relevant checks pass and verification evidence distinguishes database tests from routine checks.

## Implementation notes

Use the existing Auth/Relay seams and controlled authority adapters. Concrete Go declarations are implementation choices. This ticket establishes the protected collaboration used by subsequent settlement work; it does not change the accepted product policy or authorize deployment.
