# 08: Remove transitional Auth usage paths and qualify the result

**What to build:** The integrated relay exposes account and credential facts through Auth, while Quota, Relay, the Portal and deletion/recovery workflows use their final interfaces. The combined behavior is verified on one revision.

**Blocked by:** [04: Serve Portal usage and live counts outside Auth](04-portal-quota-reads.md), [06: Import Retained Quota Usage outside Auth on re-registration](06-reregistration-retained-quota-usage.md), [07: Recover Retained Quota Usage outside Auth](07-recovery-retained-quota-usage.md)

**Status:** ready-for-agent

- [ ] Remove compatibility forwarding and unused Auth interfaces or response fields after their last callers migrate. Auth handles configured policy and credential facts without reading usage, history, live counts or retained accounting records.
- [ ] Verify production wiring and supported fixtures use the final interfaces. Keep exact connection ownership, global Safety Limits, existing Relay Authentication responses and the client transport decision.
- [ ] Audit I1–I8 and T1–T10. Record fresh evidence for the captured-read race, both final-admission orderings, delayed close, Portal snapshot protection, deletion sealing, re-registration and recovery.
- [ ] Run the documented routine and race profiles and applicable disposable PostgreSQL tests on the integrated revision. Record unavailable prerequisites and skipped coverage explicitly.
- [ ] Update current ownership documentation to agree with the accepted design and complete the project's Standards and Spec reviews. Verification evidence stays scoped to the exercised environment.

**Spec coverage:** I1–I8 · T1–T10 (integration verification). See the [parent specification](../spec.md) and [decision record](../decisions.md).
