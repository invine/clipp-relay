# 05: Complete Relay Account Deletion accounting outside Auth

**What to build:** The deletion workflow and Quota handle accounting from deletion admission through finalization. Auth still authorizes deletion and invalidates credentials. The existing retention and deletion transaction guarantees remain observable end to end.

**Blocked by:** [03: Let Quota reconcile committed caps against admission](03-reconcile-committed-quota-limits.md)

**Status:** resolved

- [x] Through authenticated deletion admission, verify credential invalidation and immediate logical retirement. Keep the deletion accounting handoff and physical-close release in the established post-guard order.
- [x] Hold Recovery Journal proof and verify the bounded final accounting flush starts only after proof and outside account and row protection. Inject an unavailable or unresolved flush and verify finalization can complete.
- [x] With disposable PostgreSQL, verify one finalization transaction retains confirmed current-week usage, seals late settlement, detaches identity and records completion and audit. A late settlement cannot charge the replacement account.
- [x] Exercise a lost commit acknowledgement. Confirm finalization independently before forgetting in-memory accounting. Verify retry and worker failure classifications remain observable.
- [x] Give the accounting side ownership of deletion's transaction-scoped retention and keyed-identity rules. Leave a seam reusable by registration and offline recovery without exposing usage figures to Auth or creating a second transaction.

**Spec coverage:** I1, I5, I7–I8 · T7 (deletion completion), T9. See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-05`, source commit `a1a1985404d62c542170d627ba1b92e78fe77716`; integrated unchanged into `codex/quota-ownership-integration` at the same commit. Baseline was integration `2b4b1d6f9915a0fc905c026bcf0494471e1f2182`. `go test -count=1 ./internal/quota ./internal/persistence/... ./internal/auth`, `go vet ./internal/quota ./internal/persistence/... ./internal/auth`, and `git diff --check HEAD^ HEAD` passed. PostgreSQL-backed deletion transaction scenarios were not run because no serving fixture was available. Final Standards/Spec review will cover the combined revision.
