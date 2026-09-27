# 14: Use managed relays from Android

**What to build:** Connect Android to managed relays using browser authorization and native Keystore-protected credentials.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md); [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** ready-for-agent

Repository scope: Clipp.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I7, I11.

## Contract and scope

Use a Custom Tab and the exactly registered private application redirect URI, fresh state and 32-byte S256 PKCE scoped to the initiating relay. No Google login in an embedded WebView, no domain ownership/App Links requirement for every self-hosted relay and no arbitrary callback acceptance.

Native Android owns a non-exportable Keystore key and authenticated encrypted app-private renewable credentials. Exclude ciphertext and related credential material from cloud backup and device transfer; no plaintext Preferences/localStorage fallback. Access Tokens, PKCE, discovered managed Peer IDs and network resources are transient. Missing/unreadable keys or ciphertext require erasure and explicit login. A post-rotation save failure may retain the new credential in RAM with a restart warning, never old-token reuse.

The Activity/WebView owns networking and the shared controller; a foreground service does not recreate the host after process loss. Reopening the app recreates transient networking and fresh discovery/authentication with the existing Device Identity. Shared settings/UI persist all new-model configurations, initially empty with no legacy import. Android prefers WSS then WebRTC Direct. Only user action starts browser login; account-management navigation is not evidence of a server mutation.

## Acceptance criteria

- [ ] Run browser login → exact app callback → exchange → reservation/Rendezvous → relayed transfer on an actual supported Android runtime.
- [ ] Reject wrong state, mismatched relay, wrong redirect and duplicate/late callbacks; token exchange and libp2p auth remain scoped to the initiating endpoint.
- [ ] Verify authenticated Keystore ciphertext persistence and actual backup/transfer exclusions; missing protection never silently stores plaintext.
- [ ] Test process death/reopen, key invalidation, corrupt ciphertext, successful rotation/save failure and single-flight refresh without Device Identity replacement or credential replay.
- [ ] Prove the foreground service does not reconstruct networking after process loss, and reopening resumes discovery with no persisted managed Peer ID or Access Token.
- [ ] Exercise shared configuration/UI edit/remove/retry/manage actions and force WSS and WebRTC Direct separately while direct peers and other relays remain independent.
- [ ] Run npm run check and Android build/native checks; record device/emulator, Android version and browser evidence, distinguishing native tests from bridge mocks.

## Demonstration

On Android, explicitly authorize a configured relay, exchange a clip, kill the process and reopen. Demonstrate fresh relay discovery using securely retained renewable credentials, then show explicit login after invalidating the local encryption key.

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

