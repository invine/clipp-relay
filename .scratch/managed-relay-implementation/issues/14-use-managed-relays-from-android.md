# 14: Use managed relays from Android

**What to build:** Connect Android to managed relays using browser authorization and native Keystore-protected credentials.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md); [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** claimed

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

- Continuation claimed centrally on 2026-10-05 from approved consolidated client baseline `4dc53d7` in isolated worktree `/Users/invine/src/js/clipp/.local/managed-relay/worktrees/frontier-android-20261005`, branch `codex/managed-relay-14-frontier-20261005`. The operator connected the approved test phone; ADB reports an authorized OnePlus GM1917 device. Fresh-context agent will prepare and exercise native/runtime acceptance while preserving installed app data, Device Identity, credentials and other applications. Root owns status, review, integration and resolution; unavailable Google/paired-peer steps remain explicit. At most two implementation agents; shared Electron transport changes must be integrated before Android adopts them. No push, deployment, user-data clearing or destructive key tests against the operator profile.

- Approved breakdown published on 2026-09-27. Implementation not started.

- Claimed centrally on 2026-09-27 after ticket 12 resolved (`72a7dd4`) and ticket 10 remained resolved. Fresh-context implementation will use an isolated Clipp worktree based on integrated ticket 12 (`ab56937`); review, native/runtime evidence and integration checks are required before resolution.

- Local Android implementation `b35d104` and review-driven credential-race fix `2461fdc` were merged into the isolated Clipp integration branch as `3b6a474`; integration commit `153afb7` wired the shared signed-record route filter before transport start and updated runtime guidance. Independent spec and standards reviews found and fixed removal, persistence, refresh/login and shared UI action races. Integrated verification on Node 26.10.0/npm 11.19.1: `npm run check` (70 suites, 553 tests and all runtime typechecks), `npm run lint -- --quiet`, Android Vite build, Capacitor sync, and offline `:app:assembleDebug` using JDK 21 and the installed Android SDK all passed. In the isolated Android branch, API 31 AOSP arm64 (Android 12) managed-emulator instrumentation passed 4/4 new Keystore/backup/callback tests plus 8/8 background-continuity tests; those emulator tests were not repeated after the integration merge. The integrated debug APK build initially lacked `ANDROID_HOME`; setting the installed SDK path made it pass. Actual Custom Tab login → service reservation/Rendezvous → clip transfer, live process-death recovery and forced WSS/WebRTC Direct remain **not run** because no approved serving journal/relay or target browser/device for that flow is available. Status remains claimed pending these acceptance checks.

- 2026-10-04 operator acceptance report: Android, Electron and the Chrome extension each connected and transferred a clip. The operator observed direct connections establishing quickly and did not isolate or verify transfer through the relay. Record real runtime connection/transfer as passed by operator report; forced relay transport transfer remains **not run**. This report does not resolve the ticket or its remaining transport/lifecycle acceptance gates.

- 2026-10-05 central integration evidence: Integrated reviewed native acceptance isolation as client `1d19269`; 27 functional API31 OnePlus checks passed, plus separately executed credential reopen phases. Combined client check 74 suites/602 tests and canonical native acceptance build passed. Real Google/browser and forced live relay acceptance remain not run; ticket stays claimed.


- 2026-10-05: accepted single-session shared fallback integrated as Clipp `c514e9e`
  and relay `a29f581`. [Ticket 34 evidence](../34-single-session-fallback-evidence.md)
  covers core/adapter checks and real synthetic wire transfers. This does not
  replace this runtime's outstanding live/native acceptance; status remains claimed.
