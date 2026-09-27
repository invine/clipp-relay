# 25: Survive dependency loss and overload

**What to build:** Prove the implemented service fails safely under database/provider/clock/publication loss and bounded overload.

**Blocked by:** [09: Revoke access and change live quotas](09-revoke-access-and-change-live-quotas.md); [11: Rediscover and drain an ephemeral relay](11-rediscover-and-drain-an-ephemeral-relay.md); [17: Enforce retention and pepper rotation](17-enforce-retention-and-pepper-rotation.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3–I6, I8, I11, I12; Testing.

## Contract and scope

This is integrated fault qualification plus correction of defects found, not permission to defer safety controls from earlier slices. Drive the real HTTP/libp2p surface and complete Account/Quota/Relay interfaces with real PostgreSQL 17/18. Deterministic clocks/commit faults may enter at complete module seams; no mocked-SQL correctness claims or test-only internal-helper API.

Assert runtime DB max eight connections including cleanup and 64 active+waiting work units acquired before logical guards. Pool acquisition 500ms, ordinary unit 3s including guards/SQL/commit, statement 2s, lock 250ms, idle-in-transaction 5s; parent deadline wins. Pool min zero, idle 5m, lifetime 30m +uniform 0–5m, health each 60s and new connect ≤3s. Never kill a borrowed transaction solely on pool age or assume cancellation proves rollback.

Compare DB/app time with request/response uncertainty at startup/every 60s and after-lock samples. Interval not contained in ±1s warns; outside ±5s, uncertainty >1s or last usable sample >5m closes time-sensitive issuance/admission/allocation. Three valid ordinary-cadence samples recover without extending deadlines/reviving credit/resetting quota. Existing work keeps only confirmed credit/deadlines; unsafe week transition/regression fails closed even under nominal thresholds. Clock health is not readiness/liveness. Shared wrong UTC remains an operator synchronization concern.

Provider/key-cache faults must preserve still-valid independent grants without accepting expired keys/claims or retrying ambiguous one-use exchange. DB loss refuses unconfirmed spending/new auth, permits only already confirmed local credit/deadlines. API publication snapshot expires after 5m without verified resync even if addresses look unchanged; DB outage alone does not make readiness fail.

Exercise every accepted fixed Resource Manager scope, control/body/time/rate gate and private-health isolation with its existing owner implementation. Public HTTP 512 connections/128 active requests, no unbounded queue; slow readers and unknown attacker keys cannot grow state. No IP throttling, default RM IP buckets, valid-idle eviction or hidden resource increase. Use isolated smaller profiles where necessary and record overrides, not production capacity claims. Stock/report-time byte accounting keeps its accepted attribution/overshoot caveats.

## Acceptance criteria

- [ ] Run real PostgreSQL rollback, ambiguous commit, lost allocation response, late receipt, crash/restart, Monday-crossing guard waits and outage scenarios; never double-install credit or spend unconfirmed allocation.
- [ ] Verify clock warning/closed/recovery boundaries, jittered timing, unsafe week regression and cancellation without refund/deadline extension or readiness dependency.
- [ ] Fault Google endpoint/key refresh/cooldown/oversize/slow I/O and ambiguous exchanges; prove strict validation, bounded work and existing-grant behavior.
- [ ] Fault Service watch/resync and DB independently, proving publication expiry and correct readiness/drain behavior with no public self-dial dependency.
- [ ] Saturate each HTTP/auth/Rendezvous/session/RM/DB gate with malformed inputs and slow readers; measure bounded active/waiting state, rejection/retry recovery and resource release.
- [ ] Check exact resolved RM inventory—including zero/block-all semantics, no hidden autoscaling/allowlist/IP/subnet defaults—and per-handler service/buffer accounting.
- [ ] Run Go race detection for replacement/revocation/cleanup/drain/concurrent allocation scenarios; report memory/FD/goroutine behavior and bounded private health/diagnostic queues without claiming reserved fairness.

## Demonstration

Run a reproducible fault matrix that faults one dependency at a time, then overloads selected gates using an explicitly recorded test profile. Restore dependencies and verify safe recovery, intact quota and no stale authorization.

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

