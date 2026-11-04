# Quota ownership tickets

Status: resolved

Parent: [Quota ownership specification](../spec.md)  
Decisions: [Quota ownership decisions](../decisions.md)

| Ticket | Blocked by | Spec coverage |
| --- | --- | --- |
| [01: Reject stale Quota assessments](01-fence-stale-quota-assessments.md) | None | I4–I5, I8 · T1, T2, T6 |
| [02: Move final admission and live counts into Quota](02-quota-admission-live-counts.md) | 01 | I1–I2, I4–I5, I8 · T1, T2, T5 |
| [03: Let Quota reconcile committed caps against admission](03-reconcile-committed-quota-limits.md) | 02 | I1, I3–I5, I8 · T3, T4, T7 |
| [04: Serve Portal usage and live counts outside Auth](04-portal-quota-reads.md) | 02 | I1, I5–I6, I8 · T8 |
| [05: Complete Relay Account Deletion accounting outside Auth](05-deletion-accounting-handoff.md) | 03 | I1, I5, I7–I8 · T7 (deletion completion), T9 |
| [06: Import Retained Quota Usage outside Auth on re-registration](06-reregistration-retained-quota-usage.md) | 05 | I1, I7–I8 · T10 (registration and expiry) |
| [07: Recover Retained Quota Usage outside Auth](07-recovery-retained-quota-usage.md) | 05 | I1, I7–I8 · T10 (recovery and key coverage) |
| [08: Remove transitional Auth usage paths and qualify the result](08-contract-and-verify-quota-ownership.md) | 04, 06, 07 | I1–I8 · T1–T10 (integration verification) |

## Dependency rationale

- 01 hardens a complete existing Relay Authentication path. 02 builds on its assessment provenance. 03 requires 02's final admission and count publication seam to serialize committed cap application with installation.
- 04 needs 02's Quota live count. It can proceed alongside 03 because it changes Portal consumers.
- 05 uses 03's final separation between cap application and access invalidation for deletion admission. It establishes accounting-owned retention rules during actual deletion finalization.
- 06 and 07 reuse those rules in distinct account transactions. Neither depends on the other.
- 08 depends on the three remaining branch tips. It removes transitional Auth interfaces only after their consumers have moved.

The canonical ticket breakdown was supplied at `/private/tmp/clipp-relay-quota-tickets.M4XPHt2M/issues/README.md`. The user authorized implementation on 2026-11-05. The coordinator owns ticket claims, integration and resolution.
