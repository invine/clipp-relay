# 06: Import Retained Quota Usage outside Auth on re-registration

**What to build:** A returning External Identity receives a fresh Pending Relay Account and imports confirmed usage for the current Weekly Quota Window exactly once through transaction-scoped accounting.

**Blocked by:** [05: Complete Relay Account Deletion accounting outside Auth](05-deletion-accounting-handoff.md)

**Status:** ready-for-agent

- [ ] Through the protected registration path with disposable PostgreSQL, delete and re-register within the same Weekly Quota Window. Confirm usage appears as the fresh account's opening total and is imported once, including concurrent registration or an uncertain retry.
- [ ] Exercise a later Weekly Quota Window and bounded retention cleanup. Expired Retained Quota Usage is not imported, and cleanup keeps its existing bounds and observable outcomes.
- [ ] Exercise duplicate logical retained usage and unavailable required key material. Preserve existing fail-closed outcomes and registration transaction rollback.
- [ ] Keep registration, import and credential issuance in the existing transaction. Auth receives the registration result without retained records, usage figures or accounting key selection.

**Spec coverage:** I1, I7–I8 · T10 (registration and expiry). See the [parent specification](../spec.md) and [decision record](../decisions.md).
