# 07: Recover Retained Quota Usage outside Auth

**What to build:** Offline recovery restores the approved accounting consequences of deletion inside its existing transaction. Quota owns retained-usage decisions and coverage checks, while recovery owns the writer fence, journal proof and account operations.

**Blocked by:** [05: Complete Relay Account Deletion accounting outside Auth](05-deletion-accounting-handoff.md)

**Status:** ready-for-agent

- [ ] Through offline recovery against disposable PostgreSQL and a controlled Recovery Journal, verify deleted identities stay detached and confirmed current-week usage is retained according to the existing rules.
- [ ] Exercise retained-usage key coverage and duplicate logical records. Preserve fail-closed outcomes for unavailable keys and maintain the distinction between accounting key coverage and Auth's credential requirements.
- [ ] Exercise uncertain completion and retry. Preserve atomic recovery accounting, identity changes, audit and run receipt. Retain writer fencing, proof recheck and Recovery Review Hold coverage.
- [ ] Move recovered usage reads and retention decisions out of Auth while preserving the existing recovery operation and transaction lifetime.

**Spec coverage:** I1, I7–I8 · T10 (recovery and key coverage). See the [parent specification](../spec.md) and [decision record](../decisions.md).
