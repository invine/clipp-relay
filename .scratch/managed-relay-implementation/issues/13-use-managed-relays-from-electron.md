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

- Local implementation commits `e69b49c` and `77ff056` were merged into the isolated Clipp integration branch on 2026-09-27. Independent spec and standards reviews found credential, reservation ownership, address-filter, legacy-default, callback and cleanup defects; the second commit added test-first fixes. Integrated verification on Node 26.10.0/npm 11.19.1: `npm run check` (61 suites, 517 tests), `npm run lint -- --quiet` (0 errors), Electron build and localhost pairing harness passed. The first unprivileged build could not overwrite generated `dist` output under sandbox rules; the permitted rerun passed. Electron 43.4.0 on macOS 26.4.1 reported `safeStorage.isEncryptionAvailable() === true`, without exposing a backend name. Actual service login → reservation/Rendezvous → clip transfer, restart renewal and forced TCP/WSS/WebRTC Direct remain **not run** because no approved attested OCI journal is available; this ticket remains claimed, not resolved.

- Follow-up local runtime acceptance on 2026-09-28 used the integrated Clipp baseline `153afb7` with no source edits. `npm run check` passed 70 suites/553 tests after rerunning with permitted loopback access (the restricted first run failed four callback tests on `listen EPERM 127.0.0.1`); `npm --workspace apps/electron run build` and `npm run lint -- --quiet` passed. `bash scripts/smoke-postgres.sh` on the integrated Go branch passed disposable PostgreSQL 18/17 TLS migration/startup, service health/public isolation, DB outage and role/CA/hostname rejection with a synthetic Google provider and read-only local journal head proxy. A disposable Electron main-process probe (`node /private/tmp/clipp13-electron-runtime.cjs`) on macOS 26.4.1 arm64, Electron 43.4.0, Node 26.10.0/npm 11.19.1, Go 1.27.1 and Docker 29.8.0 showed an initially empty relay list, two independent entries surviving restart with stable Device Identity, isolated removal, `login_needed` state, and the correct Manage Account portal URL. The explicit login IPC generated S256 with a random IPv4 loopback callback; wrong state returned 400, a valid `access_denied` callback returned 200, then the listener closed without authenticating. The probe intercepted browser opening, used no servers/tokens, and did not write to OCI. `safeStorage` reported encryption available but no backend name. The temporary profiles and smoke containers were removed. **Still not run:** real Google code exchange, authenticated reservation/Rendezvous and clip transfer, restart renewal, and forced transport paths. These require a registered Google test client, approved Active test account, reachable HTTPS relay test endpoint and two paired Electron profiles. Ticket remains claimed; no publication or deployment occurred.

- The operator confirmed on 2026-09-28 that this real OAuth/relay test setup is unavailable now. Keep the unmet runtime acceptance checks open and continue other eligible work; do not infer a live-flow pass from the local doubles.

- 2026-10-04 operator acceptance report: Android, Electron and the Chrome extension each connected and transferred a clip. The operator observed direct connections establishing quickly and did not isolate or verify transfer through the relay. Record real runtime connection/transfer as passed by operator report; forced relay transport transfer remains **not run**. This report does not resolve the ticket or its remaining transport/lifecycle acceptance gates.
