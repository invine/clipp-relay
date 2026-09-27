# 28: Qualify clip and history interoperability

**What to build:** Verify actual cross-runtime clip and history behavior over every supported managed-relay transport without hiding failure behind direct fallback.

**Blocked by:** [13: Use managed relays from Electron](13-use-managed-relays-from-electron.md); [14: Use managed relays from Android](14-use-managed-relays-from-android.md); [15: Use managed relays from Chrome](15-use-managed-relays-from-chrome.md); [11: Rediscover and drain an ephemeral relay](11-rediscover-and-drain-an-ephemeral-relay.md).

**Status:** ready-for-agent

Repository scope: Both repositories.
Source: [Accepted specification](../../managed-relay-service/spec.md), I4, I7, I8, I12; Testing.

## Contract and scope

Force actual relay traffic on Electron TCP/WSS/WebRTC Direct and Android/Chrome WSS/WebRTC Direct. Cover same-runtime and cross-runtime pairs in both directions, cross-account circuits, Rendezvous v1/v2, multiple configurations and explicit unauthenticated relays. Direct/fallback success must not count as a passing forced path. Record exact OS/runtime/library/protocol versions rather than claiming every platform.

Include fresh relay Peer ID/WebRTC certificate after restart, stale discovery, publication loss, renewal, replacement/drain and Rendezvous-only degradation. All configured relays are independent; authentication never downgrades to unauthenticated access. No relay configuration still permits direct networking. Signed Peer Records remain intact; unconfigured relay routes are skipped without auto-configuration/login, and Device Identity stays stable.

Use normal clips including the maximum 256KiB accepted clip and multi-frame history exercising whole-circuit 2MiB per direction/120s limits. Existing history has no resume cursor; reconnection can replay a prefix. A fitting clip is not proof of arbitrary history completion. Preserve Clip Event identity/idempotency and history's no-current-clipboard mutation rule. Verify/report cutoff, bounded retry and any incomplete history honestly; no silent loss, duplicate application, uncontrolled retry, hidden limit change or resumable-protocol redesign. Known unavoidable cutoff must be visible and distinguished from an implementation integrity defect.

## Acceptance criteria

- [ ] Force and prove each supported runtime/transport path independently; capture transport evidence so direct fallback cannot mask failure.
- [ ] Exercise same/cross-runtime, both directions, cross-account, Rendezvous v1/v2, simultaneous managed configurations and explicit unauthenticated relay cases.
- [ ] Verify no-relay direct operation, intact Signed Peer Records, unchanged Device Identity and skipping unconfigured routes without browser login or auto-configuration.
- [ ] Restart/drain the relay and show fresh identity/certificate discovery and correct recovery, including stale endpoint rejection, reservations, renewal and Rendezvous-only degraded state.
- [ ] Transfer maximum-size clips and bounded history through actual stock circuits; assert exactly-once application effects and unchanged current clipboard for reconciled history.
- [ ] Hit 2MiB/direction and 120s cutoffs with multi-frame history, record interruption/replay behavior and prove no silent success, duplicates or uncontrolled retry loop.
- [ ] Publish a candidate-bound runtime/transport matrix with passed/failed/not-run and precise known limitations; run existing shared/runtime history conformance and applicable builds/checks.

## Demonstration

Run a three-runtime relay-only exchange matrix, then cut a history synchronization at a stock circuit limit. Show preserved clip identities, bounded recovery and an honest incomplete result where the unchanged history protocol cannot resume.

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

