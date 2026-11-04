# 02: Move final admission and live counts into Quota

**What to build:** Quota makes the final account admission decision and maintains live Relay Session counts across installation and removal. Relay still owns the exact connection and the global Safety Limit. The existing admin cap path continues to work until ticket 03 replaces it.

**Blocked by:** [01: Reject stale Quota assessments](01-fence-stale-quota-assessments.md)

**Status:** resolved

- [x] Use the spec's opaque assessment, final admission and count publication contracts. Relay holds its registry lock through final admission. Quota checks the assessment and commits the in-memory registry change and aggregate counts together.
- [x] Through real Relay Authentication, admit the first eligible session, refuse a new slot at capacity, and prove that refusal or a stale assessment creates no slot and leaves the previous connection intact when replacement is refused.
- [x] Through real wire tests, renew on the same connection and replace a same-account connection at capacity without growing its count. On cross-account replacement, verify the source and target counts change together and one effective session remains for the Peer ID.
- [x] Exercise disconnect, expiry, grant/account invalidation and accounting cutoff. Verify they release the live count as permission ends, including when socket closure is delayed. Preserve the global Safety Limit.
- [x] Exercise admission overlapping idle accounting eviction and a stale assessment after state recreation. Verify live counts survive and an old assessment cannot authorize against a new state identity.
- [x] Keep token verification connection-independent and prove the account admission path takes its limits from Quota. Any old cap field still needed by other callers is removed after migration.

**Spec coverage:** I1–I2, I4–I5, I8 · T1, T2, T5. See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-02`, source commit `b6bab9fbb024b82db521390624b395537b6b0cc8`; integrated unchanged into `codex/quota-ownership-integration` at the same commit. Baseline was integration `1e8b1da678a48223f1d649bb0d33f980f343d3bf`. Focused Quota, Auth and Relay tests, focused race tests, `go vet ./internal/relay ./internal/quota`, and integrated `go test -count=1 ./internal/quota ./internal/auth ./internal/relay` passed; `git diff --check` passed. PostgreSQL-backed composed coverage was not run because no serving fixture was available. Final Standards/Spec review will cover the combined revision.
