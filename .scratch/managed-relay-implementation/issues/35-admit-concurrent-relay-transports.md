# 35: Admit concurrent relay transport connections

**What to build:** Admit concurrent relay transport connections.

**Blocked by:** [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** wontfix

Repository scope: clipp-relay.
Source: coordinator interpretation of the 2026-10-05 instruction, superseded by the user clarification that persistent concurrency was not requested. This is now a conditional candidate gated by proposed Clipp ADR-0012; [accepted specification](../../managed-relay-service/spec.md), I4/I7, retains its original replacement contract.

**Decision:** ADR-0012 accepted single-session fallback. This concurrency candidate is superseded and is not required delivery.

## Candidate contract and scope

Allow the same Peer ID and account to authenticate simultaneously through distinct TCP, WSS and WebRTC Direct physical connections. Every connection independently crosses authentication and keeps its own expiry and renewal. Preserve existing physical-session account/global limits, quota funding, resource caps, account-generation guards and fail-closed authorization; do not multiply capacity or introduce peer-global authentication. Same-family stale replacement and cross-account replacement must not mix authority or leak sessions. Stock per-peer reservation and Rendezvous ownership must tolerate transport loss without stale cleanup removing healthy authority; revocation closes all affected physical connections. Verify behavior through actual libp2p connections and complete Account/Quota/Relay interfaces, including limits and races.

## Acceptance criteria

- [ ] Demonstrate simultaneous supported transport connections and independent failure recovery at the approved public seams.
- [ ] Preserve identity, credentials, admission limits and unrelated connections.
- [ ] Pass focused TDD checks, repository verification and independent Standards/Spec reviews.
- [ ] Integrate and verify locally before central resolution; distinguish local protocol evidence from external deployment acceptance.

## Comments

- Centrally claimed 2026-10-05 after the listed predecessor was verified resolved. Fresh context and isolated worktree required. Coordinator owns canonical claims, integration and resolution. No push, publication, deployment or shared-cluster mutation; capacity ticket 33 remains deferred.

- User clarification 2026-10-05: candidate admission code is preserved on `codex/managed-relay-concurrency-candidate-20261005` and in its implementation worktree. No further integration or resolution until ADR-0012 decides persistent concurrency. If fallback is selected, this candidate is unnecessary and must not be represented as required delivery.

- Superseded on 2026-10-05 by the user's acceptance of ADR-0012: retain one connection and try alternatives before failure. Status `wontfix` records a rejected architecture candidate, not passed implementation acceptance. Preserve experimental branches; restore original same-Peer-ID replacement in local main through reviewed reconciliation.
