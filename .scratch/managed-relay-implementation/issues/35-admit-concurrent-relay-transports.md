# 35: Admit concurrent relay transport connections

**What to build:** Admit concurrent relay transport connections.

**Blocked by:** [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: user instruction on 2026-10-05, "application should connect to every available relay transport, not just to the first one"; [accepted specification](../../managed-relay-service/spec.md), I4/I7, amended by this instruction.

## Contract and scope

Allow the same Peer ID and account to authenticate simultaneously through distinct TCP, WSS and WebRTC Direct physical connections. Every connection independently crosses authentication and keeps its own expiry and renewal. Preserve existing physical-session account/global limits, quota funding, resource caps, account-generation guards and fail-closed authorization; do not multiply capacity or introduce peer-global authentication. Same-family stale replacement and cross-account replacement must not mix authority or leak sessions. Stock per-peer reservation and Rendezvous ownership must tolerate transport loss without stale cleanup removing healthy authority; revocation closes all affected physical connections. Verify behavior through actual libp2p connections and complete Account/Quota/Relay interfaces, including limits and races.

## Acceptance criteria

- [ ] Demonstrate simultaneous supported transport connections and independent failure recovery at the approved public seams.
- [ ] Preserve identity, credentials, admission limits and unrelated connections.
- [ ] Pass focused TDD checks, repository verification and independent Standards/Spec reviews.
- [ ] Integrate and verify locally before central resolution; distinguish local protocol evidence from external deployment acceptance.

## Comments

- Centrally claimed 2026-10-05 after the listed predecessor was verified resolved. Fresh context and isolated worktree required. Coordinator owns canonical claims, integration and resolution. No push, publication, deployment or shared-cluster mutation; capacity ticket 33 remains deferred.
