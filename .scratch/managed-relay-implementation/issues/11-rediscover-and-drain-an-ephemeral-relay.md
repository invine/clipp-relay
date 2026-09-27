# 11: Rediscover and drain an ephemeral relay

**What to build:** Clients can discover the sole current relay generation through address changes and the process drains predictably before replacement.

**Blocked by:** [08: Register and find peers](08-register-and-find-peers.md); [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I8, I11.

## Contract and scope

Starting→Serving→Draining→Stopped; fresh identity/cert each start, no durable
sessions/reservations/leases/epochs. Namespace-scoped read-only named-Service
watch supplies TCP/UDP, config supplies WSS/ports; per-transport explicit override
removes that Service dependency. Include all valid addresses/dedupe/internal sort.
Atomically publish only complete enabled-listener/current-peer/certhash snapshot;
disabled transports omitted, missing required transport withdraws all.

Discovery TTL60s capped by last-verified snapshot5m staleness; bounded API resync
at least60s verifies unchanged state. Readiness listeners/routing/publication/
not-draining, not DB/free capacity/external self-dial. Publication loss recoverable
while Serving/live. Discovery429 only active-session hard cap,503 dependency/
publication/drain; no slot reservation, queue or hysteresis.

First SIGTERM/SIGINT withdraws discovery/readiness and refuses auth including
renewal, reserve create/renew, HOP CONNECT and RV register. Existing circuits
may complete; active Ping/RV lookup/unregister allowed. Stock tracer aggregate
atomic circuit count only; zero closes sessions/exits, second signal or30s forces.
Ops available until final HTTP shutdown, ready503; K8s grace45s. Actual network
removal may interrupt sooner, no guarantee full30s.
Startup readyz5s/2s/120fails; ready5s/2s/1fail+1success; live10s/2s/3fails.

## Acceptance criteria

- [ ] Service-status changes republish complete deterministic snapshots; missing enabled address never produces a partial successful response.
- [ ] API outage uses bounded last-good state then withdraws ready/discovery at5m; unchanged successful resync refreshes validity, overrides remove only their transport watch dependency.
- [ ] DB failure/session saturation changes admission, not readiness/liveness; startup publication failure and subsequent recoverable loss have different probe consequences.
- [ ] Signal tests prove immediate rejection of each new protected operation, retained permitted drain work, zero-count completion, deadline and second-signal force.
- [ ] Restart creates different Peer ID/certhash and empty live state; stale document/client mismatch triggers fresh discovery without credential/device reset.
- [ ] No per-circuit ownership registry, DB tombstone/epoch or public drain/debug API is introduced; probe/private-metric budgets stay bounded.

## Demonstration

Drive a fake Kubernetes API through publish/change/outage/recovery while real clients use the relay, then signal drain and restart and demonstrate a new discovery generation.

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

- Claimed centrally on 2026-09-27 after ticket 08 resolution (`d6e8916`) and ticket 10 resolution (`074b842`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.
