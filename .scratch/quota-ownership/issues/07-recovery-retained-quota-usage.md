# 07: Recover Retained Quota Usage outside Auth

**What to build:** Offline recovery restores the approved accounting consequences of deletion inside its existing transaction. Quota owns retained-usage decisions and coverage checks, while recovery owns the writer fence, journal proof and account operations.

**Blocked by:** [05: Complete Relay Account Deletion accounting outside Auth](05-deletion-accounting-handoff.md)

**Status:** resolved

- [x] Through offline recovery against disposable PostgreSQL and a controlled Recovery Journal, verify deleted identities stay detached and confirmed current-week usage is retained according to the existing rules.
- [x] Exercise retained-usage key coverage and duplicate logical records. Preserve fail-closed outcomes for unavailable keys and maintain the distinction between accounting key coverage and Auth's credential requirements.
- [x] Exercise uncertain completion and retry. Preserve atomic recovery accounting, identity changes, audit and run receipt. Retain writer fencing, proof recheck and Recovery Review Hold coverage.
- [x] Move recovered usage reads and retention decisions out of Auth while preserving the existing recovery operation and transaction lifetime.

**Spec coverage:** I1, I7–I8 · T10 (recovery and key coverage). See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-07`; implementation commit `a8016a2` is included in branch tip `e4ed978e00c78f79a0b05b074a29e10570d2cba2`, which integrated into `codex/quota-ownership-integration` unchanged. Baseline was integration `538b7e95bac901b8460223042cce936b7ebab677`; branch also included later integration tip `292fb8a42cb8f6cd975f45f530e274b55fdfe193`. `go test -count=1 ./internal/quota ./internal/auth ./internal/persistence/...`, matching `go vet`, and `git diff --check HEAD^ HEAD` passed. PostgreSQL-backed recovery scenarios were not run because no serving fixture was available. Final Standards/Spec review will cover the combined revision.
