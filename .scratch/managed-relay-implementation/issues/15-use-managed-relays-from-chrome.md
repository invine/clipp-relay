# 15: Use managed relays from Chrome

**What to build:** Connect the Chrome extension to managed relays while keeping renewable credentials in the background context.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md); [10: Relay over WSS and WebRTC Direct](10-relay-over-wss-and-webrtc-direct.md).

**Status:** claimed

Repository scope: Clipp.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I7, I11.

## Contract and scope

Use chrome.identity.launchWebAuthFlow with the exact registered chrome.identity.getRedirectURL("clipp-relay") callback for the configured extension ID; no arbitrary chromiumapp origins. Fresh state and 32-byte S256 PKCE bind the initiating relay. Interactive flow only follows user action.

Background owns configuration, browser flow, PKCE and renewable credentials. Set chrome.storage.local access level to TRUSTED_CONTEXTS before credential access. This storage is not an OS-keychain encryption guarantee. Offscreen owns libp2p/shared controller and receives only short Access Tokens through a narrow validated port. Popup, options and content scripts receive neither renewable tokens nor PKCE secrets; expose only necessary actions and safe state. Access Tokens/PKCE remain transient.

Offscreen recreation performs fresh discovery/authentication, not replay of stale session/reservation state. Coordinate single-flight refresh across consumers and service-worker lifecycle. Persist rotated credentials atomically where possible; save failure keeps only the new credential in RAM with restart warning and never old-token replay/plaintext fallback elsewhere. First adoption starts with an empty relay list; later new-model configuration persists. Prefer WSS then WebRTC Direct and retain the Device Identity. Opening the portal proves no account action.

## Acceptance criteria

- [ ] Complete explicit authorization through launchWebAuthFlow and the registered extension callback, then reservation/Rendezvous and relayed transfer in Chrome.
- [ ] Reject wrong state/relay/callback and unregistered extension IDs; cancellation and blocked accounts never create interactive retry loops.
- [ ] Prove TRUSTED_CONTEXTS is applied before credential reads/writes and content scripts cannot retrieve tokens; inspect popup/options messages and narrow offscreen port for credential separation.
- [ ] Show that offscreen receives only short Access Tokens, no refresh tokens, and both Access Tokens and PKCE disappear with the owning transient lifecycle.
- [ ] Exercise service-worker suspension/restart, offscreen recreation and concurrent consumers without stale credential replay or duplicate refresh; rotation/save failure gives a clear restart warning.
- [ ] Persist/edit/remove every configuration independently through shared UI/options, preserving the host/Device Identity and unrelated routes; force WSS and WebRTC Direct separately.
- [ ] Run npm run check and extension build, then record actual Chrome/extension-ID/runtime evidence rather than relying only on mocks.

## Demonstration

Authorize from extension UI, relay a clip, destroy/recreate offscreen and suspend/resume the service worker. Verify fresh discovery and independent relay state while observing that renewable credentials never cross into popup or offscreen.

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

- Continuation claimed centrally on 2026-10-05 in isolated client worktree `/Users/invine/src/js/clipp/.local/managed-relay/worktrees/frontier-chrome-20261005`, branch `codex/managed-relay-15-frontier-20261005`, from reviewed/integrated client main `ec87a19`. Fresh-context implementation follows the shared relay-isolation seam after Electron WSS receipt passed. Prepare safe development acceptance and lifecycle checks while preserving the operator's installed extension, profile, Device Identity and credentials. Browser policy blocks automation of installed extension-internal pages; manual registered-ID browser flow remains separate from synthetic fixture tests. Root owns canonical claims, integration and resolution; status stays claimed until full runtime acceptance. No push, publication, deployment or cluster mutation; capacity remains deferred.

- Approved breakdown published on 2026-09-27. Implementation not started.

- Claimed centrally on 2026-09-27 after ticket 12 resolved (`72a7dd4`) and ticket 10 remained resolved. Fresh-context implementation starts from integrated Clipp ticket 12 (`ab56937`) in an isolated worktree; the shared host seam from ticket 13 will be integrated only after its review fixes. Chrome runtime evidence, review and integration checks are required before resolution.

- Local Chrome implementation commits `5af113e` and `2b2c47f` were merged after an independent spec review and review-driven TDD fixes for authenticated offscreen control, crash-safe credential removal, endpoint-scoped token access, resilient shutdown, denied-login guidance and bounded token HTTP. On the combined Clipp integration branch, `npm run check` passed 68 suites/541 tests with all runtime typechecks, `npm run lint -- --quiet` passed, `npm --workspace apps/extension run test:managed-relays` passed in Chrome 143.0.7499.4 (unpacked ID `gfalngoclhgbppbjkcnmfdaedgoeopmj`), and `npm --workspace apps/extension run test:offscreen-storage` passed. The reviewed ticket 13 host seam is present in this integration build. Registered-ID OAuth, live WSS/WebRTC reservation/transfer, and worker suspension with live credentials remain **not run**: no approved serving journal/relay, registered extension ID or second device is available. Status remains claimed pending those acceptance checks.

- 2026-10-04 operator acceptance report: Android, Electron and the Chrome extension each connected and transferred a clip. The operator observed direct connections establishing quickly and did not isolate or verify transfer through the relay. Record real runtime connection/transfer as passed by operator report; forced relay transport transfer remains **not run**. This report does not resolve the ticket or its remaining transport/lifecycle acceptance gates.

- 2026-10-05 central integration evidence: Integrated reviewed forced-transport acceptance as client `33fee35`; owned disposable Chrome143 fixture passed with synthetic provider, identity preservation and direct-dial denial. Combined client check passed 74 suites/602 tests. Registered-ID Google auth and actual forced Chrome relay transfer remain not run; ticket stays claimed.
