# 13: Use managed relays from Electron

**What to build:** Connect Electron to configured managed relays using the system browser and secure main-process credential storage.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md); [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** claimed

Repository scope: Clipp.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I7, I11.

## Contract and scope

Electron main owns browser authorization, PKCE, refresh and OS-backed safeStorage. Use the registered loopback redirect at 127.0.0.1 with a random port and /oauth/callback, varying only the permitted port. Accept one valid callback for the initiating relay, validate state, close the listener, and use a fresh cryptographically random 32-byte PKCE verifier with S256. Interactive browser login starts only after user action; the renderer receives no token strings.

Persist renewable credentials encrypted only with a usable OS provider; insecure basic_text or unavailable protection means RAM-only with clear restart warning. Access Tokens, PKCE and discovered managed Peer IDs remain transient. Unreadable credentials are erased and require login. If rotation succeeds but secure saving fails, keep only the new credential in memory, warn, and never replay the old one or fall back to plaintext.

Wire the shared configuration/UI actions through the runtime bridge and persist new-model settings independently of secrets. First adoption is empty rather than a legacy import. Prefer TCP, WSS, then WebRTC Direct, trying all supported addresses through the shared bounded controller. Changing a relay must not recreate the libp2p host or Device Identity. Manage account opens the configured portal and does not infer the browser's account or committed server action.

## Acceptance criteria

- [ ] Complete explicit login → callback → code exchange → authenticated reservation/Rendezvous → relayed transfer in the actual Electron runtime against the service.
- [ ] Reject wrong state, wrong relay/redirect, duplicate or late callbacks and unexpected peer identity without granting or exposing credentials; the one-shot loopback listener closes on completion/cancel/timeout.
- [ ] Verify refresh tokens stay in main and encrypted storage, never renderer IPC, logs or persisted access-token state; prove memory-only behavior for unavailable/insecure OS storage.
- [ ] Exercise restart, unreadable ciphertext and successful rotation followed by save failure; only a usable new credential is retained and old credentials are never retried.
- [ ] Persist all configured entries and their independent UI states/actions; removal and replacement erase only relevant local credentials and preserve direct peers/other relays.
- [ ] Force TCP, WSS and WebRTC Direct individually; test certificate/Peer ID rediscovery and blocked/quota/login-needed behavior without browser loops.
- [ ] Run npm run check and Electron build; record actual tested OS/version/provider and runtime evidence without implying untested platforms passed.

## Demonstration

From a fresh Electron profile add a managed relay, sign in and exchange a clip. Restart to renew through secure storage, then remove it while a direct peer and another configured relay remain connected.

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

- Claimed centrally on 2026-09-27 after ticket 12 resolved (`72a7dd4`) and ticket 10 remained resolved. Fresh-context implementation will use an isolated Clipp worktree based on integrated ticket 12 (`ab56937`); review, runtime evidence and integration checks are required before resolution.
