# 06: Import Retained Quota Usage outside Auth on re-registration

**What to build:** A returning External Identity receives a fresh Pending Relay Account and imports confirmed usage for the current Weekly Quota Window exactly once through transaction-scoped accounting.

**Blocked by:** [05: Complete Relay Account Deletion accounting outside Auth](05-deletion-accounting-handoff.md)

**Status:** resolved

- [ ] Through the protected registration path with disposable PostgreSQL, delete and re-register within the same Weekly Quota Window. Confirm usage appears as the fresh account's opening total and is imported once, including concurrent registration or an uncertain retry. This scenario was not executed without a serving fixture.
- [ ] Exercise a later Weekly Quota Window and bounded retention cleanup. Expired Retained Quota Usage is not imported, and cleanup keeps its existing bounds and observable outcomes. This database-backed scenario was not executed without a serving fixture.
- [x] Exercise duplicate logical retained usage and unavailable required key material. Preserve existing fail-closed outcomes and registration transaction rollback.
- [x] Keep registration, import and credential issuance in the existing transaction. Auth receives the registration result without retained records, usage figures or accounting key selection.

**Spec coverage:** I1, I7–I8 · T10 (registration and expiry). See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-06`, source commit `2881fd076ab3f8c5186cf84020ba4fff2e63a654`; integrated unchanged into `codex/quota-ownership-integration` at the same commit. Baseline was integration `538b7e95bac901b8460223042cce936b7ebab677`. `go test -count=1 ./internal/quota ./internal/auth ./internal/persistence/...`, `go vet` for those packages, and `git diff --check HEAD^ HEAD` passed. PostgreSQL-backed registration coverage was not exercised because no serving fixture was available. Final Standards/Spec review will cover the combined revision.
