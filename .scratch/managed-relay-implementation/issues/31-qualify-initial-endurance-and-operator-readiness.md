# 31: Qualify initial endurance and operator readiness

**What to build:** Verify the initial twenty-session endurance profile and operator readiness without changing the eventual capacity goal or accepted resource limits.

**Blocked by:** [30: Qualify OCI deployment and recovery](30-qualify-oci-deployment-and-recovery.md).

**Status:** ready-for-agent

Repository scope: Both repositories; authorized isolated environment.
Source: [Accepted specification](../../managed-relay-service/spec.md), I11, I12; Testing.

## Contract and scope

Run four hours on enforced Linux ARM64 with twenty concurrent authenticated sessions across multiple accounts within their administrator-selected allowances. Record the TCP/WSS/WebRTC Direct mix, configuration and candidate digests. Exercise repeated session renewal, reservation renewal, Rendezvous refresh, stock circuits and bounded traffic alongside normal portal, DB, cleanup and backup work. Relay stays within accepted 1/2 CPU and 1/2GiB request/limit (Go soft 1,536MiB, RM 512MiB); all DB/helper/maintenance work retains its declared independent budgets. Twenty is a test profile, not a new cap/SLA or replacement for the 5,000-session goal.

Require no unexpected crash/OOM, stuck renewal, integrity error, deadline breach or accumulating unreleased live state. Distinguish retained Go heap from leaks using live object/goroutine/connection/resource evidence. Run separate isolated overload profiles that exercise every gate and bounded recovery; document any test-only overrides without presenting them as production capacity.

Before public serving verify the actual operator-owned monitoring/notification destination outside the process. It must detect critical conditions and lost observations/availability, deliver an already-detected critical condition within 5m and route warnings. Prior simulated fixtures are insufficient. No Prometheus requirement or built-in paging system; missing operator destination blocks readiness.

Test operator runbooks for install/upgrade/schema, credentials/CA, degraded dependencies, pressure/failed Jobs/compromise, backup/restore/journal repair/account review and uninstall. Each names the signal, safe action, forbidden shortcuts and reopening verification. All operational exercises remain within explicitly authorized isolated targets. Findings affecting safety/recovery/quota/schema are blockers, not cosmetic deferrals.

## Acceptance criteria

- [ ] Complete the full four-hour Linux ARM64 twenty-session run with recorded mixed transports, multiple accounts and normal portal/DB/backup activity under enforced accepted limits.
- [ ] Show repeated auth/reservation/Rendezvous lifecycle work and actual circuit traffic throughout, with allowance-respecting load and no direct-path substitution.
- [ ] Record memory/CPU/FD/goroutine/live-state/deadline trends; explain heap retention and prove no accumulating unreleased state, OOM, stuck renewal or integrity failure.
- [ ] Execute separate bounded overload/recovery profiles for all admission/resource gates, private health and diagnostic queues; clearly record test-only overrides.
- [ ] Verify real external operator monitoring, critical delivery within 5m after detection, warning routing and loss-of-observations/availability detection with timestamped evidence.
- [ ] Exercise every required runbook on authorized isolated resources and record safe reopening checks, least privilege and preserved failure evidence.
- [ ] Publish candidate-bound results with no 5,000-session or all-platform claim; failed/missing endurance or monitoring evidence blocks the initial release handoff.

## Demonstration

Run the mixed-transport four-hour workload while a normal backup and portal operations occur, then simulate a critical signal and observation loss through the actual operator monitor. Collect the resulting resource trends, notifications and runbook evidence.

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

