# 33: Measure target-scale capacity

**What to build:** Measure the eventual target-scale behavior after implementation, reporting real limits without silently changing configuration or overstating capacity.

**Blocked by:** [29: Produce a verified multi-architecture candidate](29-produce-a-verified-multi-architecture-candidate.md).

**Status:** deferred

Repository scope: Both repositories; separately authorized load environment.
Source: [Accepted specification](../../managed-relay-service/spec.md), I11, I12; revised Q337; Testing.

## Contract and scope

Scheduling: deferred. This fully specified ticket is excluded from the initial release frontier and is not a blocker for the human release handoff. Its candidate dependency is necessary but not sufficient to start; request separate scheduling and authorize the isolated load environment before execution.

Retain the goal of 1,000 accounts/5,000 authenticated sessions and the accepted limits: relay 2GiB container, Go soft memory 1,536MiB, overlapping RM accounting 512MiB, RM FD ceiling 8,192 and verified process nofile ≥16,384. Do not lower the goal, silently raise budgets, miscount memory or restrict transports merely to claim success.

Known stock WebRTC Direct reservation is 2,631,680 bytes per connection at medium RM priority; only 121 such reservations fit within the medium-priority allowance under a 512MiB RM budget before other use. This is an accounting constraint, not measured RSS or practical capacity. Yamux windows and other scopes also consume memory; count maxima cannot all saturate simultaneously. A 5,000-all-WebRTC test is not promised by the selected profile.

Use a recorded representative Linux ARM64 transport/account/workload mix with idle, active and churn phases, normal portal/DB/quota/control work and actual relayed traffic. Ramp in bounded stages with explicit safety stops. Measure accepted/refused sessions, latency/deadlines, Go/native/kernel/RM memory, CPU, FD, streams, quota allocation, DB work and resource cleanup. Full-goal success needs actual enforced representative evidence, not mocks/cross-compilation/summed count limits. Record unmet targets honestly; recommendations to retune configuration require human approval and new relevant qualification, not automatic tuning or a protocol fork.

## Acceptance criteria

- [ ] Confirm explicit deferred-work scheduling, candidate applicability and authorized isolated load targets before starting; do not target production or unrelated third parties.
- [ ] Publish exact resource/profile/library/architecture/transport/account/workload settings and safety-stop thresholds without modifying accepted production defaults.
- [ ] Run staged idle/active/churn load with real relay paths and normal control/DB work, observing bounded admission/refusal and recovery at each stage.
- [ ] Measure RSS/Go/native/kernel/RM memory, CPU/FD/streams/deadlines/DB/quota and post-load live-state release; distinguish reservations/accounting limits from real memory capacity.
- [ ] Report which representative profiles meet or miss 1,000-account/5,000-session goals, with explicit WebRTC Direct and mixed-scope constraints and no all-transport saturation guarantee.
- [ ] If target cannot be demonstrated, preserve goal/configuration, record measured gap and propose separately approved tuning/qualification work; no dishonest undercount or silent transport restriction.
- [ ] Keep initial release gates independent of this result and make no full-scale success claim without actual enforced evidence.

## Demonstration

After separate scheduling, ramp an authorized Linux ARM64 workload through representative mixed-transport stages. Publish measured saturation and safe rejection points, explicitly showing whether the eventual goal is met or remains unproven.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

- Kept explicitly deferred on 2026-09-27 during managed-relay implementation coordination. The candidate dependency and separately authorized load environment are not available; this ticket is outside the initial release frontier and has not been claimed or run.
