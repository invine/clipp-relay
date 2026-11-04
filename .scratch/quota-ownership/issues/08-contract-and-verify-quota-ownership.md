# 08: Remove transitional Auth usage paths and qualify the result

**What to build:** The integrated relay exposes account and credential facts through Auth, while Quota, Relay, the Portal and deletion/recovery workflows use their final interfaces. The combined behavior is verified on one revision.

**Blocked by:** [04: Serve Portal usage and live counts outside Auth](04-portal-quota-reads.md), [06: Import Retained Quota Usage outside Auth on re-registration](06-reregistration-retained-quota-usage.md), [07: Recover Retained Quota Usage outside Auth](07-recovery-retained-quota-usage.md)

**Status:** resolved

- [x] Remove compatibility forwarding and unused Auth interfaces or response fields after their last callers migrate. Auth handles configured policy and credential facts without reading usage, history, live counts or retained accounting records.
- [x] Verify production wiring and supported fixtures use the final interfaces. Keep exact connection ownership, global Safety Limits, existing Relay Authentication responses and the client transport decision.
- [x] Audit I1–I8 and T1–T10. Record fresh evidence for the captured-read race, both final-admission orderings, delayed close, Portal snapshot protection, deletion sealing, re-registration and recovery.
- [x] Run the documented routine and race profiles and applicable disposable PostgreSQL tests on the integrated revision. Record unavailable prerequisites and skipped coverage explicitly.
- [x] Update current ownership documentation to agree with the accepted design and complete the project's Standards and Spec reviews. Verification evidence stays scoped to the exercised environment.

**Spec coverage:** I1–I8 · T1–T10 (integration verification). See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-08`, source and integration commit `4a52992440a823d3cdbaddc72165464e8ea104ee`; baseline was integration `fd9175830afd611bc130a7ec11fc4db777791776`. The Standards review found no breaches or actionable smells. The Spec review's T5 finding was fixed by `TestQuotaBackedRenewalAndCrossAccountReplacementPublishBothCounts` at `e25aca0ce0b3439e1fd073fe7391322570e0c4a8`; final Standards and Spec reviews at that revision found no remaining issues. `bash scripts/verify.sh routine` and `CGO_ENABLED=1 bash scripts/verify.sh race` passed on `e25aca0ce0b3439e1fd073fe7391322570e0c4a8` with Go 1.27.3 darwin/arm64. Real PostgreSQL, native image, OCI/provider and live qualification were not run per documented boundaries; PostgreSQL-backed scenarios also lacked a serving fixture. Final documentation-only resolution commit follows; verification is renewed on that branch tip.
