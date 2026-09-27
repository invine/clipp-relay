# Define Clipp runtime integration

Type: grilling
Status: resolved
Blocked by: 01, 03, 05, 07, 09

## Question

What shared-core and runtime-specific changes should Electron, Android, and the Chrome extension make for Google login callbacks, secure renewable-credential storage, Relay Authentication, automatic discovery of the sole current Relay Instance, restart recovery, explicit persistent-relay fallback, exact Rendezvous, user-visible states, and migration from the current hard-coded relay behavior?

## Comments

- Q187 settled: Relay Configuration is a strict union representing exactly one
  relay. Its authenticated form stores one exact canonical HTTPS Relay Discovery
  Endpoint; its unauthenticated form stores complete multiaddrs for one persistent
  Relay Peer ID. A stable local key and optional display name are local-only, and
  credentials are stored separately. The forms never mix.
- Q188 settled: production Clipp accepts user-entered HTTPS Relay Discovery
  Endpoints for self-hosted authenticated relays. The UI displays the endpoint
  host, credentials remain scoped to its exact origin, redirects are rejected,
  and credentials never move between configurations.
- Q189 settled: each runtime keeps one libp2p host and its existing Device
  Identity while connecting that host to every Relay Configuration. A Relay
  Configuration never creates a separate Device Identity or libp2p host.
- Q190 settled: shared core owns the per-configuration discovery, dial, remote
  Peer-ID verification, exact-connection Relay Authentication, reservation,
  Rendezvous, renewal, rediscovery, and retry state machine. Runtime adapters
  provide secure credential storage, interactive browser authorization,
  lifecycle, and HTTP capabilities.
- Q191 settled: login and credentials are scoped to one authenticated Relay
  Configuration rather than a global Clipp login. Account state, authorization,
  logout, and errors always identify the affected configuration.
- Q192 settled: Electron persists renewable credentials only with OS-backed
  `safeStorage` and otherwise keeps them memory-only; Android uses a native
  Android Keystore-backed encrypted store excluded from backup and transfer;
  the Chrome extension uses `chrome.storage.local` restricted to trusted
  extension contexts. Access tokens and unfinished PKCE transactions are
  memory/session-only. An unreadable credential is erased locally and requires
  authorization again; no runtime falls back to plaintext persistence.
- Q193 settled: Electron, Android, and the Chrome extension all expose persistent
  add, remove, and update operations for authenticated and unauthenticated Relay
  Configurations. The extension uses its options page, the other runtimes use
  shared settings UI, and unauthenticated relay entry is an explicit advanced
  choice.
- Q194 settled: Clipp starts its Device Network and direct-connectivity behavior
  even when all relays are logged out, unavailable, or invalid. Every Relay
  Configuration owns independent status and retry behavior; one failure cannot
  block direct connections or another relay.
- Q196 settled: Relay Configuration changes do not restart the runtime's single
  libp2p host. The host starts without automatic circuit-relay listeners, and a
  shared controller dynamically creates and removes configuration-owned relay
  connections, authentication, reservations, and Rendezvous state while
  preserving Device Identity, direct connections, and unrelated relays.
- Q197 settled: each Relay Configuration maintains at most one active connection
  to its relay even when several transports are advertised. Supported addresses
  are attempted with bounded staggering; the first Noise-authenticated connection
  matching the expected Relay Peer ID wins, losing dials close, and Relay
  Authentication occurs only on the winner. Electron prefers raw TCP, WSS, then
  WebRTC Direct; Android and the extension prefer WSS, then WebRTC Direct. All
  supported addresses are tried before the relay is declared unreachable.
- Q198 settled: display metadata may change in place, but a change of relay
  identity creates a replacement Relay Configuration. Changing an authenticated
  discovery URL or an unauthenticated Relay Peer ID never carries credentials or
  retry state forward. Unauthenticated multiaddrs may update in place only while
  retaining the same Relay Peer ID.
- Q199 settled: removing an authenticated Relay Configuration closes its local
  relay resources and erases its local credential without performing
  Account-wide Revocation. Because individual Login Grant revocation is absent,
  the inaccessible server-side grant continues to count until its inactivity
  expiry. `Sign out everywhere` remains a separate explicit action when immediate
  account-wide invalidation is wanted.
- Q200 settled: runtimes persist only Relay Configurations and renewable
  credentials. Access tokens, Relay Discovery Documents, current Relay Peer IDs,
  connections, reservations, and Rendezvous Leases are reconstructed after every
  process or host restart and are never restored as live state.
- Q201 settled: v1 retains Android's Activity/WebView-owned libp2p lifecycle. A
  process loss preserves Relay Configurations and Android Keystore credentials,
  but relay connectivity resumes only after the user reopens Clipp; the current
  foreground service does not reconstruct the network runtime.
- Q202 settled: the Chrome extension's MV3 background worker owns Relay
  Configuration persistence, PKCE, interactive browser authorization, and
  renewable credentials. The offscreen document owns the shared relay controller
  and libp2p host, obtains only short-lived access tokens through a narrow
  background message port, and performs fresh discovery and Relay Authentication
  whenever it is recreated.
- Q195 research follow-up (2026-09-05): the user requested comparison with
  established self-hosted applications before selecting Android callback
  behavior. [Self-hosted mobile authorization callbacks](../research/self-hosted-mobile-authorization-callbacks.md)
  documents custom app callbacks in Immich and browser-approval polling in
  Nextcloud. The revised recommendation is an Android private-use callback with
  authorization code, S256 PKCE, and transaction-state validation, avoiding a
  hosted callback-domain dependency. Google still returns to the relay's own
  HTTPS callback. Polling would require a separate Clipp authorization flow;
  Nextcloud's protocol is not OAuth Device Authorization. Q195 remains open
  pending the user's choice; this research does not change the settled contract.
- Q195 settled (2026-09-05): Android uses an Immich-style private application
  callback with the existing authorization-code and S256 PKCE flow. Google
  returns to the configured relay's registered HTTPS callback; after validating
  Google authentication and account eligibility, the relay issues a one-use
  Clipp authorization code and redirects to the fixed, registered Android app
  URI. The app validates callback URI and transaction state, then redeems the
  Clipp code with its PKCE verifier only at the relay that initiated the flow.
  Google credentials never enter the Android callback. This replaces the
  earlier Android verified-App-Link recommendation and requires no shared
  callback website, per-relay APK build, or `assetlinks.json` hosting. Electron
  retains its temporary IPv4 loopback callback, and Chrome retains
  `chrome.identity.launchWebAuthFlow` with its registered extension callback.
- Q203 settled (2026-09-05): do not migrate legacy relay settings into the new
  Relay Configuration model. Upgrades perform no automatic import or conversion
  of existing relay-address lists. This decision concerns relay settings only;
  it does not authorize changes to Device Identity or other application data.
  The initial Relay Configuration list for new installs and upgrades remains a
  separate decision.
- Q204 settled (2026-09-05): new installations and upgrades adopting the new
  Relay Configuration model start with an empty relay list. No built-in relay
  is inserted, and users explicitly add the relays they want to use. Direct
  Device Network connectivity remains available without a configured relay.
  This initialization rule does not reset configurations already saved in the
  new model on subsequent application upgrades.
- Q205 settled (2026-09-05): browser sign-in starts only after an explicit user
  action for the affected Relay Configuration. Startup, reconnect attempts, and
  a requirement for renewed login never open the browser automatically. Valid
  renewable credentials may still refresh silently; when interactive login is
  required, the configuration presents a user-invoked sign-in action.
- Q206 settled (2026-09-05): reject duplicate Relay Configurations within one
  installation. Authenticated entries are duplicates when their canonical Relay
  Discovery Endpoint URLs are identical; unauthenticated entries are duplicates
  when their Relay Peer IDs are identical, regardless of their address sets or
  display names. Apply this validation when adding or updating an entry.
- Q207 settled (2026-09-05): when different Relay Discovery Endpoint URLs
  advertise the same Relay Peer ID, preserve the already-connected Relay
  Configuration and flag the other as a configuration conflict. The conflicting
  entry must not share credentials, take over the existing connection, or
  automatically switch its account association.
- Q208 settled (2026-09-05): each authenticated Relay Configuration permits only
  one credential refresh operation in flight. Concurrent consumers, including
  Relay Discovery and Relay Session renewal, await the same operation and use
  its result rather than submitting the same single-use refresh token again.
  Refresh coordination remains independent between Relay Configurations.
- Q209 settled (2026-09-05): if secure persistence of a successfully renewed
  credential fails, Clipp may continue using the new credential in memory for
  the current runtime session. It displays a warning that restarting will
  require sign-in. It never reuses the old single-use refresh token or falls
  back to plaintext credential storage.
- Q210 settled (2026-09-05): an authenticated Relay Configuration shows `Ready`
  only while its connection-scoped Relay Authentication, relay reservation, and
  Rendezvous registration are all valid. A transport connection alone does not
  imply readiness; incomplete setup is displayed separately.
- Q211 settled (2026-09-05): loss of Rendezvous registration alone does not tear
  down a relay connection whose Relay Authentication and reservation remain
  valid. Preserve working relayed connections, display degraded status, and
  retry Rendezvous independently under the existing retry and error rules.
- Q212 settled (2026-09-05): Clipp exposes `Manage relay account` for an
  authenticated Relay Configuration. The action opens that relay's web portal
  for account statistics and `Sign out everywhere`; the portal displays the
  account being managed and confirms committed revocation. The browser account
  may differ from the account used by Clipp, so opening the portal neither
  proves matching identity nor successful revocation. Clipp does not report
  revocation success or erase credentials merely because it opened the portal;
  it handles server-side credential invalidation through its normal runtime
  flow. Relay Access Tokens gain no account-management permissions. This
  supersedes Q156's promise of an in-app revocation operation and completion
  confirmation in [Define account credential, lifecycle, and retention
  policy](09-define-account-credential-lifecycle-and-retention.md).
- Q213 settled (2026-09-05): recognized quota or session-limit rejection retains
  valid login credentials and shows the specific limit only when the protocol
  identifies it. Retry automatically at a slow pace while respecting server
  retry hints, and offer manual retry without bypassing those hints. Capacity
  failures do not require a new sign-in. Retry duration defaults remain in the
  initial-defaults ticket; ambiguous stock Circuit Relay statuses must not be
  presented as a more specific account-quota diagnosis.
- Q214 settled (2026-09-05): a remote Signed Peer Record does not implicitly
  configure or authorize use of another relay. Skip circuit routes through
  relays absent from the installation's Relay Configurations, while continuing
  to try direct addresses and eligible configured-relay routes. Receiving the
  record does not add a configuration, trigger browser sign-in, or bypass Relay
  Authentication. Preserve the original signed record unchanged; route
  eligibility is a local dialing decision.

## Answer

Confirmed by the user. This resolves the runtime-integration design; it does not
implement or deploy the production changes.

### Configuration and connection ownership

Electron, Android, and the Chrome extension expose persistent add, update, and
remove operations for independent Relay Configurations. The extension uses its
options page; Electron and Android use shared settings UI. Each configuration
represents exactly one relay: either an exact canonical HTTPS Relay Discovery
Endpoint, or a non-empty set of complete multiaddrs for one persistent Relay
Peer ID. Unauthenticated entry is an explicit advanced choice, not automatic
fallback. A stable local key and optional display name identify the entry;
credentials are stored separately and never transferred between entries.

Arbitrary self-hosted HTTPS discovery endpoints are accepted and their host is
visible in the UI. Discovery rejects redirects, and credentials remain scoped
to the configured endpoint and origin. Adding or updating an entry rejects
duplicate canonical discovery URLs and duplicate unauthenticated Relay Peer
IDs. If different discovery URLs advertise the same Relay Peer ID, preserve the
already-connected configuration and flag the other as conflicting without
sharing credentials, taking over the connection, or changing its account.

Display metadata changes in place. Changing an authenticated endpoint or an
unauthenticated Relay Peer ID replaces the configuration without carrying its
credentials or retry state forward. Explicit multiaddrs may change in place
while retaining the same persistent Relay Peer ID. Removing an entry closes its
local relay resources and erases its local credentials, but does not revoke the
account's server-side Login Grants; their expiry and account-wide revocation
remain governed by the account contract.

### Shared core and runtime responsibilities

Each runtime retains one libp2p host and its existing Device Identity. Shared
core owns each configuration's discovery, dial, Peer-ID verification,
connection-scoped Relay Authentication, reservation, Rendezvous, renewal, and
recovery state machine. Runtime adapters supply credential storage, interactive
authorization, HTTP capabilities, and lifecycle integration. Clipp attempts all
configured relays independently; relay failures never block direct Device
Network connectivity or unrelated relays.

The host starts without automatic circuit-relay listeners. The shared
controller adds and removes configuration-owned relay resources dynamically,
without restarting the host or disturbing unrelated connections. Each
configuration maintains at most one active relay connection. Supported
addresses are attempted with bounded staggering; the first connection whose
Noise-authenticated Peer ID matches the expected Relay Peer ID wins, losing
dials close, and Relay Authentication runs only on the winner. Electron prefers
raw TCP, WSS, then WebRTC Direct; Android and the extension prefer WSS, then
WebRTC Direct. All supported addresses are tried before declaring the relay
unreachable. Every authenticated relay connection independently crosses the
Relay Authentication Barrier before protected protocols are used.

Android retains its Activity/WebView-owned network lifecycle. Its foreground
service does not reconstruct the libp2p runtime after process loss; networking
resumes when the user reopens Clipp. In the extension, the MV3 background worker
owns configuration persistence, interactive authorization, PKCE, and renewable
credentials. The offscreen document owns shared-core networking and receives
only short-lived access tokens through a narrow background message port. A
recreated offscreen document performs fresh discovery and Relay Authentication.

### Browser authorization and credential recovery

Login is scoped to one authenticated Relay Configuration, never a global Clipp
login. Browser authorization begins only after an explicit user action; startup,
reconnection, and expired renewable credentials never open a browser on their
own. Valid renewable credentials may refresh silently.

All runtimes use the settled authorization-code and S256 PKCE flow. Google
returns to the configured relay's registered HTTPS callback, and the relay
issues its own one-use Clipp authorization code after validating authentication
and account eligibility. Electron receives that code through its temporary IPv4
loopback callback. Android uses the fixed, registered private application URI
selected through the [self-hosted callback comparison](../research/self-hosted-mobile-authorization-callbacks.md),
without a shared callback website, per-relay APK build, or `assetlinks.json`
hosting. Chrome uses `chrome.identity.launchWebAuthFlow` and its registered
extension callback. Clients validate the callback and transaction state and
redeem the code with the PKCE verifier only at the initiating relay. Google
credentials never enter the app callback.

Electron persists renewable credentials only through OS-backed `safeStorage`,
otherwise retaining them in memory. Android uses a native Android
Keystore-backed encrypted store excluded from backup and transfer. The
extension uses `chrome.storage.local` restricted to trusted extension contexts;
this is the agreed browser-storage boundary, not an OS-keychain guarantee.
Access tokens and unfinished PKCE transactions remain memory/session-only.
Unreadable stored credentials are erased locally and require sign-in again;
storage failures do not introduce a plaintext fallback.

Each configuration permits one credential refresh operation in flight.
Concurrent discovery and Relay Session renewal consumers await the same result,
avoiding reuse of a single-use refresh token. If securely saving a successfully
renewed credential fails, Clipp may continue with it in memory and warns that
restarting will require sign-in. It never retries with the consumed old token.
The strict rotation, expiry, and revocation rules remain in
[Define account credential, lifecycle, and retention policy](09-define-account-credential-lifecycle-and-retention.md).

### Readiness, Rendezvous, and failure isolation

An authenticated configuration shows `Ready` only while its Relay
Authentication, reservation, and Rendezvous registration are all valid.
Transport connection alone is not readiness. Incomplete setup is shown
separately, and loss of Rendezvous registration alone shows degraded status
while preserving valid relay connections and working circuits. Rendezvous
repairs independently under the settled error and retry rules.

Recognized quota and session-limit rejections retain valid login credentials,
show a specific cause only when the protocol identifies it, and use slow
automatic retries respecting server hints. Manual retry cannot bypass those
hints. Capacity rejection does not request another sign-in, and ambiguous stock
Circuit Relay statuses do not become unsupported account-quota diagnoses.
Numeric deadlines and retry defaults belong to
[Choose initial Quota Plan and Safety Limit defaults](12-choose-initial-quota-and-safety-limit-defaults.md).

The exact Rendezvous contracts and initial-release v2-first/v1-compatible
negotiation remain as settled in
[Define single-process relay control protocols](05-define-relay-control-protocols.md).
The no-settings-migration decision does not remove protocol compatibility.
Receiving a remote Signed Peer Record does not authorize or configure another
relay: skip unconfigured circuit routes, continue trying direct and eligible
configured-relay routes, and do not initiate browser sign-in. Route filtering is
local to dialing and never changes the original signed record.

### Account management and initialization

Clipp's `Manage relay account` action opens the configured relay's web portal
for account statistics and `Sign out everywhere`. The portal displays the
account being managed and confirms committed revocation. A browser's current
account may differ from Clipp's account; opening the portal proves neither
matching identity nor successful revocation. Clipp does not claim success or
erase credentials merely because it opened the portal, and instead handles
server-side invalidation through its normal runtime flow. This supersedes the
earlier Q156 in-app revocation/completion promise without expanding Relay Access
Token permissions or weakening the server's commit-before-success requirement.

There is no migration of legacy relay settings. New installations and upgrades
first adopting the new model start with an empty list, without built-in relay
defaults; users explicitly add their relays. Later upgrades preserve
configurations already saved in the new model. Device Identity and other
application data remain untouched.

Only Relay Configurations and renewable credentials are durable relay-controller
state. The persistent Peer ID in an unauthenticated configuration remains part
of that configuration; discovered managed Relay Peer IDs, access tokens,
discovery documents, connections, reservations, and Rendezvous Leases are never
restored as live state. Process or host restart rebuilds them through the
settled discovery and authentication flow, following
[Define single Relay Instance lifecycle and discovery](07-define-relay-pool-lifecycle-and-assignment.md).
