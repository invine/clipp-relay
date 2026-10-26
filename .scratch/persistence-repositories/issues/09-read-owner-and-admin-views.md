# 09: Read coherent owner and administrator views

**What to build:** Owners and administrators see coherent account, quota and history views through typed read interfaces, while live capacity remains a separately sampled observation.

**Blocked by:** 01: Introduce repository scopes through credential validation.

**Status:** ready-for-agent

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I3, I7, I9–I10; T1, T7–T8.

- [ ] Migrate owner profile, administrator lists/details, quota history, aggregate read signals and active-grant sampling to narrow backend-neutral read interfaces.
- [ ] Keep joined reads behind the PostgreSQL adapter and preserve the coherent account/plan/session/usage snapshots currently returned by each operation. Do not assemble them from independent per-entity reads.
- [ ] Preserve Portal Session/administrator authorization checks, pagination, bounded work, current plus twelve preceding weekly totals, overrun presentation and unavailable-versus-zero distinctions.
- [ ] Keep live Relay Session counts separate from durable snapshots. Presentation state grants no relay admission authority and contains no Device Identity/session registry.
- [ ] Preserve aggregate signal privacy and fixed-label diagnostics/metrics, with no raw credentials or stable private identity labels introduced by the new projections.
- [ ] Verify views during concurrent account/plan/usage mutation, pagination, absent samples, database refusal and authorization expiry using controlled read adapters and real PostgreSQL snapshots. Mutation workflows may continue on their migration schedule without gating this read slice.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.
