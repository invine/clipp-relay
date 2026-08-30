# Cross-runtime Google authorization

## Question

What authorization-code-with-PKCE and renewable-grant architecture can serve Clipp's Electron, Android, and Chrome-extension runtimes while keeping Google credentials out of Relay Instances, authorizing administrators from a verified-email allowlist, avoiding durable Device Identity records, and failing safely during Coordinator or PostgreSQL outages?

## Recommendation

Use two deliberately separate authorization hops:

1. The Relay Coordinator is a confidential Google OpenID Connect relying party. It alone holds the Google web client ID and client secret, receives Google's authorization code, validates the Google ID token, and resolves the External Identity to a Relay Account.
2. The Relay Coordinator is also the authorization server for three pre-registered public Clipp clients: Electron, Android, and the Chrome extension. Each Clipp runtime starts an authorization-code flow with PKCE and receives Clipp Relay credentials, never Google credentials.

The resulting trust boundary is:

```text
Google OIDC
    |  Google code and tokens
    v
Relay Coordinator / portal
    |  one-use Clipp code + rotating Clipp Relay credentials
    v
Clipp runtime
    |  short-lived opaque Relay access token
    v
Relay Instance -- protected introspection --> Relay Coordinator
```

Google's web-server flow sends the authorization code to an exact registered HTTPS callback and has the server exchange that code using its client secret; this is the appropriate place to keep the Google secret and tokens ([Google web-server OAuth guide](https://developers.google.com/identity/protocols/oauth2/web-server)). Native and browser-based public clients cannot safely keep a shared client secret and must use PKCE ([RFC 8252, sections 6 and 8.5](https://www.rfc-editor.org/rfc/rfc8252.html), [RFC 10017, section 6.3.2](https://www.rfc-editor.org/rfc/rfc10017.html)).

This architecture preserves the settled domain boundary: neither the Google transaction, the Clipp authorization grant, nor its tokens contain or store a libp2p Peer ID. A Relay Instance associates the account returned by token introspection with the Noise-authenticated Peer ID only in the live Relay Session.

## Google OIDC hop

### One confidential web client

Configure one Google OAuth client of type **Web application**. Its ID and secret come from an existing Kubernetes Secret and are mounted only into Coordinator/web pods, never Relay Instance pods. Register one exact HTTPS Google callback such as:

```text
https://relay.example.com/oidc/google/callback
```

The Coordinator requests only `openid email`. `profile` is unnecessary for the agreed account and quota pages. Omit `access_type=offline`: Google only returns a Google refresh token when offline access is requested, and Clipp does not need continuing access to a Google API ([Google OIDC flow](https://developers.google.com/identity/openid-connect/openid-connect)). After validating the ID token, discard Google's ID and access tokens and persist only the normalized External Identity and permitted presentation fields.

The Google transaction must use a one-time, high-entropy `state` value bound to the browser session and a one-time OIDC `nonce`. The callback must validate `state`, exchange the code over HTTPS, and validate the ID token's signature, `iss`, `aud`, `exp`, and `nonce`; Google documents those claims and publishes its endpoints and keys through its discovery document ([Google OIDC guide](https://developers.google.com/identity/openid-connect/openid-connect), [Google OIDC API reference](https://developers.google.com/identity/openid-connect/reference)). Use a maintained OIDC/JWT verifier rather than treating decoded JWT fields as trusted.

Google accepts both `accounts.google.com` and `https://accounts.google.com` as issuer values. Validate either documented value, then canonicalize the persisted issuer to `https://accounts.google.com`; otherwise the same `sub` could accidentally create two Relay Accounts. Persist `(canonical issuer, sub)` as the External Identity. Google says `sub` is unique, stable across email changes, and never reused, while email can change and must not be the account key ([Google OIDC claims](https://developers.google.com/identity/openid-connect/reference)).

### Nested transaction, not forwarded Google credentials

There are two distinct pieces of correlation state:

- The Clipp client supplies its own opaque `state` and PKCE challenge to the Coordinator's `/oauth/authorize` endpoint.
- The Coordinator creates separate server-side Google `state` and `nonce` values and binds them to that outer authorization transaction.

Store the short-lived transaction in PostgreSQL so any Coordinator replica can finish it. Never put the Google code, Google token, Clipp code, PKCE verifier, or a selectable return URL in a cookie or log. The outer redirect is selected only from the pre-registered Clipp client record. OAuth security guidance requires exact redirect matching, prohibits open redirectors, and requires PKCE for public clients ([RFC 9700, section 2.1](https://www.rfc-editor.org/rfc/rfc9700.html)).

After Google authentication:

- First login creates a Pending Relay Account and an authenticated portal session, but it does not create a Clipp authorization code. A client-initiated flow returns a standard `access_denied` authorization error with a presentation-safe pending-approval message; the client can link to the account-status page.
- Active accounts receive a one-use Clipp authorization code.
- Suspended or Denied accounts receive no code or Relay credential.
- An allowlisted administrator may enter the admin portal independently of whether that identity's own Relay Account is approved, so the first administrator can approve accounts.

### Administrator email allowlist

Evaluate admin access from the validated Google ID token, not from an unverified request parameter or stored browser value. Require all of the following:

1. a valid Google ID token for the configured web client;
2. `email_verified=true`;
3. an exact match after only trimming and ASCII case-folding against the Secret-backed allowlist; do not strip dots, `+suffixes`, or otherwise alias addresses; and
4. evidence that Google is authoritative for the address: either the email ends in `@gmail.com`, or `email_verified=true` and `hd` is present.

The fourth check matters. Google explicitly warns that a non-Gmail Google Account can retain `email_verified=true` after ownership of its third-party mailbox changes; Google calls Gmail addresses and Workspace addresses with `hd` authoritative, but not third-party addresses without `hd` ([Google server-side ID-token verification](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token)). Regular Relay Accounts can still use any valid Google `sub`, because their identity is not keyed by email. If an administrator must use a non-Google-hosted email, the simple email allowlist is insufficient; a future issuer-plus-sub administrator allowlist is the safe alternative.

Admin authorization must be re-evaluated on every admin request against the current Secret-backed allowlist and the account's most recently validated email metadata. Portal sessions should be short-lived, server-side, revocable records referenced by host-only `Secure`, `HttpOnly` cookies; they are separate from Clipp login grants.

## Clipp authorization hop

### Public client registry and callbacks

Register three fixed public clients. None has a client secret.

| Runtime | Client callback | Required behavior |
| --- | --- | --- |
| Electron | `http://127.0.0.1:{ephemeral-port}/oauth/callback` | Bind a temporary listener to the IPv4 loopback interface only, open the authorization URL in the system browser, accept one callback, then close the listener. The Coordinator may vary only the port for this registered loopback pattern. |
| Android | `https://relay.example.com/oauth/callback/android` | Declare a verified Android App Link for the exact host/path and publish `/.well-known/assetlinks.json` with the release signing certificate. Launch login in a browser Custom Tab, not the Capacitor WebView. |
| Chrome extension | `https://<extension-id>.chromiumapp.org/clipp-relay` | Obtain the exact URI from `chrome.identity.getRedirectURL("clipp-relay")` and run `chrome.identity.launchWebAuthFlow({interactive: true, ...})` only after an explicit user action. |

RFC 8252 requires native apps to use an external user-agent, requires PKCE, prefers claimed HTTPS callbacks where available, and defines temporary loopback callbacks for desktop apps ([RFC 8252](https://www.rfc-editor.org/rfc/rfc8252.html)). Android App Links verify the app/domain association and prevent another app from intercepting the claimed URL ([Android App Links](https://developer.android.com/training/app-links/about)); Android recommends Custom Tabs for third-party sign-in rather than a WebView ([Android web authentication guidance](https://developer.android.com/develop/ui/views/layout/webapps)). Electron's main-process `shell.openExternal` opens HTTPS URLs in the default browser ([Electron `shell`](https://www.electronjs.org/docs/latest/api/shell)). Chrome's identity API creates the `chromiumapp.org` callback, closes the auth window on that callback, and returns the final URL to the extension ([Chrome `identity`](https://developer.chrome.com/docs/extensions/reference/api/identity)).

The Coordinator must compare registered redirects exactly, with the sole RFC-defined exception of an Electron loopback port. It must reject `localhost`, non-loopback addresses, alternate paths, userinfo, fragments, and arbitrary `return_to` parameters. Each client must verify the exact callback URI and its own one-time `state` before exchanging the code.

### PKCE and code semantics

For every attempt, the runtime generates:

- a 32-byte cryptographically random PKCE verifier;
- `code_challenge = BASE64URL(SHA256(verifier))` with method `S256`; and
- a separate high-entropy `state` value.

The Coordinator requires PKCE for every registered Clipp client, supports only `S256`, and rejects downgrade or missing-challenge attempts. RFC 7636 recommends at least 256 bits of verifier entropy and `S256`; RFC 9700 requires public clients to use PKCE and authorization servers to enforce it ([RFC 7636](https://www.rfc-editor.org/rfc/rfc7636.html), [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)).

The Clipp code is opaque, random, short-lived, single-use, and bound to the client ID, exact redirect URI, account ID, scope, and PKCE challenge. Store only its keyed hash. Redeem it with an HTTPS form POST to `/oauth/token`; return tokens in the response body with `Cache-Control: no-store`, never in a URI. OAuth requires codes to expire quickly, be single-use, and remain bound to client and redirect URI ([RFC 6749, section 4.1](https://www.rfc-editor.org/rfc/rfc6749.html)).

### Relay access token and renewable login grant

Prefer **opaque** Clipp Relay tokens over self-contained JWT access tokens for the first release:

- The access token is a high-entropy, short-lived bearer token with an audience restricted to Relay Discovery and Relay Authentication and only the required scopes.
- PostgreSQL stores a keyed hash plus account ID, login-grant ID, client ID, scopes, issue/expiry times, and revocation state.
- Relay Discovery validates the token in the Coordinator directly.
- A Relay Instance sends the presented token to a protected, in-cluster RFC 7662-style introspection endpoint. An active response supplies the Relay Account ID and expiry; an inactive token returns only `{"active":false}`. The endpoint authenticates the Relay Instance and is protected by TLS. RFC 7662 defines this protected introspection behavior and warns that caching increases the stale-revocation window ([RFC 7662](https://www.rfc-editor.org/rfc/rfc7662.html)).
- Relay Instances do not persist the introspection result. They hold the Relay Account association only for that live Relay Session.

Opaque introspection is a better fit than offline JWT validation here because the settled behavior already requires new Relay Authentication to fail closed when current account or quota state cannot be checked. It also makes suspension and access-token revocation immediate for new sessions without distributing signing-key rotation logic to the data plane.

The renewable credential is a separate, high-entropy opaque refresh token representing a **login grant**, not a device. Store only a keyed hash and these fields: grant/family ID, account ID, public client ID, current-token generation, status, created/last-used/absolute-expiry times, and reuse-detection history. Do not record a Peer ID, device name, hardware identifier, app-install identifier, IP address, or user-agent fingerprint.

Every successful refresh atomically:

1. validates the current token, account status, client binding, inactivity limit, and absolute lifetime;
2. marks that refresh token consumed;
3. creates a new access token and refresh-token generation; and
4. commits before returning the response.

Reusing a consumed refresh token revokes the whole grant family and every access token derived from it, then requires a new browser authorization. RFC 9700 requires public-client refresh tokens to be sender-constrained or rotated with reuse detection and recommends inactivity expiry ([RFC 9700, section 4.14](https://www.rfc-editor.org/rfc/rfc9700.html)). The current no-durable-client-binding decision rules out DPoP or another persistent client-key thumbprint, so rotation is the compliant choice. RFC 10017 additionally requires a maximum or inactivity lifetime for browser-client refresh tokens and says rotation must not extend an already fixed absolute lifetime ([RFC 10017, section 6.3.2.3](https://www.rfc-editor.org/rfc/rfc10017.html)).

Expose RFC 7009-style revocation for the current grant and portal actions to revoke any listed login grant. Revoking a refresh token should cascade to its family and derived access tokens; suspending or denying a Relay Account invalidates every grant and access token for that account. RFC 7009 requires refresh-token revocation and permits cascading invalidation of the underlying grant ([RFC 7009](https://www.rfc-editor.org/rfc/rfc7009.html)).

This bearer design has an explicit residual risk: possession of an unexpired Relay access token or current refresh token is sufficient to use it from another client or Peer ID. Short lifetimes, protected platform storage, rotation, introspection, and revocation limit that risk but do not eliminate it. Eliminating it would require a durable per-grant proof key; that would be a new tracking/binding decision even if it were not the Clipp Device Identity.

## Runtime fit in the current Clipp codebase

### Electron

The Electron main process already owns libp2p startup, SQLite, and the preload bridge (`apps/electron/src/main.ts`, `storage.ts`, and `preload.ts`). It should own the browser launch, temporary loopback listener, code exchange, refresh, and credential storage. Renderer APIs should expose only actions and presentation state; they must never return token strings.

Encrypt the refresh token using Electron `safeStorage` in the main process, preferably its asynchronous API, and store only ciphertext in the existing SQLite key/value table. Electron documents that `safeStorage` uses Keychain on macOS, DPAPI on Windows, and desktop-dependent secret stores on Linux; Linux can fall back to insecure `basic_text` when no secret store exists ([Electron `safeStorage`](https://www.electronjs.org/docs/latest/api/safe-storage)). If a usable OS-backed encryption provider is unavailable, keep the login grant memory-only and require login after restart rather than persisting plaintext. Access tokens and the PKCE verifier stay in memory.

Only pass a freshly obtained access token from the main-process credential service into the shared network layer for Relay Discovery and connection-scoped Relay Authentication. Never expose it to `window.clipp` or renderer state.

### Android / Capacitor

The Android runtime currently stores general state through Capacitor Preferences with `localStorage` and memory fallbacks (`apps/android/src/storage.ts`). That is not an acceptable refresh-token store. Add a narrow native secure-credential bridge that generates a non-exportable AES key in Android Keystore and stores only authenticated ciphertext in app-private preferences. Android Keystore keeps key material non-exportable and supports limiting its cryptographic use ([Android Keystore](https://developer.android.com/privacy-and-security/keystore)).

The current manifest enables backup and its backup rules exclude only background-continuity diagnostics. Exclude refresh-token ciphertext and pending PKCE transaction material from both cloud backup and device transfer; a restored ciphertext without its Keystore key must be treated as credential loss and trigger fresh login.

`MainActivity` is already `singleTask`, and the app's minimum SDK is 23, the first Android App Links release. Add an exact `VIEW`/`BROWSABLE`/`DEFAULT` intent filter with `android:autoVerify="true"`, handle the callback in the native activity/Capacitor bridge, and verify `state` before passing a one-use code to the token exchanger. Publish the Digital Asset Links file with every accepted release signing fingerprint. Use a Custom Tab; never navigate the existing Capacitor WebView to Google's authorization endpoint.

### Chrome extension

The MV3 background service worker should own authorization and refresh. Add the `identity` permission, start `launchWebAuthFlow` only from the explicit login action, generate PKCE/state in the service worker, validate the returned URL, and exchange the code there. The offscreen document continues to own libp2p; the background sends it only a short-lived access token when it needs Relay Authentication. Popup, options, renderer-like state, and content-script messages never contain the refresh token.

For renewable login, persist the refresh token in `chrome.storage.local` and immediately restrict that area's access to `TRUSTED_CONTEXTS` before reading it. Chrome documents that `storage.local` is exposed to content scripts by default and that `setAccessLevel()` can restrict it to extension contexts ([Chrome `storage`](https://developer.chrome.com/docs/extensions/reference/api/storage/)). Keep access tokens and PKCE transaction state in `chrome.storage.session` or service-worker memory.

This is the strongest persistent store Chrome exposes to an extension, but it is not equivalent to an OS keychain. Browser-storage encryption at rest is not guaranteed, and same-execution-environment compromise can exfiltrate tokens ([RFC 10017, section 8](https://www.rfc-editor.org/rfc/rfc10017.html)). The product and threat model must state that limitation. Native Messaging solely to reach a keychain would add an installation dependency and attack surface and is not recommended for the initial release.

## Failure and revocation behavior

| Failure or event | Required result |
| --- | --- |
| Google unavailable | New browser login/registration fails with a retry page and no Clipp code. Existing Clipp login grants continue until their own expiry or revocation because they do not depend on a stored Google token. |
| PostgreSQL unavailable before/during Google callback | Do not create or update an account, portal session, authorization code, or token. Show retry; never fall back to an unsigned cookie or in-memory replica state. |
| PostgreSQL unavailable during code exchange or refresh | Roll back and return a temporary server error with no token material. Clients back off. A response lost after a committed one-time refresh can safely force fresh browser authorization; do not weaken replay detection to guess which party retried. |
| Coordinator unavailable to Relay Instance introspection | Reject/close new Relay Authentication and therefore deny new reservations and circuits. Do not treat a network error as an active token. |
| Coordinator/PostgreSQL lost after Relay Session authentication | Follow the settled degraded-mode rule: existing circuits consume only already allocated quota blocks and remain bounded by current token/session validity; no new authenticated activity is admitted. |
| Account suspended/denied | Mark every token inactive, revoke every login-grant family, publish the existing cluster invalidation event, and terminate live Relay Sessions within the agreed ten-second target. |
| Refresh-token reuse | Revoke the entire grant family and derived access tokens; require a fresh external-browser login. |
| Callback replay, wrong state, wrong verifier, expired or reused code | Return a non-specific OAuth error and no tokens; redact the supplied values from logs. |
| Local secure storage becomes unreadable | Delete the unusable local record, clear access-token state, and require a new login. Never fall back to plaintext persistence. |

Google logout or account disablement does not automatically revoke an independently issued Clipp login grant. If that coupling is later required, Google Cross-Account Protection can deliver signed security events such as session revocation and account disablement, but it adds a service account, receiver, terms, and currently does not cover Google Workspace users ([Google Cross-Account Protection](https://developers.google.com/identity/protocols/risc)). Keep it as an explicit future extension; bound initial login grants with absolute and inactivity expiry and rely on user/admin revocation in the first release.

## Minimal durable records

The authorization subsystem needs only:

- External Identity: canonical Google issuer, Google `sub`, last verified email and `email_verified`/`hd` presentation metadata.
- Short-lived authorization transaction: registered client, exact redirect, outer client state, PKCE challenge, Google state/nonce hashes, expiry, and one-time status.
- Short-lived authorization code: keyed hash, account, client, redirect, scopes, PKCE challenge, expiry, and redeemed flag.
- Login grant family: account, client type, token hashes/generations, status, timestamps, reuse marker, and absolute/inactivity expiry.
- Short-lived access token: keyed hash, account, grant, audience/scopes, issue/expiry, and revoked flag.
- Portal session: keyed session hash, account, authentication time, expiry, and administrator decision evaluated from current allowlist policy.

None of these records needs or permits Peer ID, Device Identity, Device Network, device name, hardware ID, or device fingerprint. Log filters must redact `Authorization`, cookies, Google/Clipp codes, ID/access/refresh tokens, PKCE verifiers, and `state`/`nonce` values.

## Acceptance checks to carry forward

- Wrong/missing PKCE verifier, `plain`, redirect mismatch, code expiry, and code replay all fail without issuing tokens.
- Wrong Google signature, `aud`, `iss`, `exp`, `nonce`, or browser `state` fails before account lookup.
- The two documented Google issuer spellings resolve to one canonical External Identity.
- Pending, Suspended, and Denied accounts cannot obtain Clipp codes or tokens.
- A non-authoritative third-party Google email cannot satisfy the administrator email allowlist even if `email_verified=true`; Gmail and Workspace-with-`hd` cases can.
- Refresh rotation is transactional; reuse revokes the family; expiry never slides past the initial absolute lifetime.
- Introspection is authenticated, uses TLS, returns no reason for inactive tokens, and fails closed on dependency errors.
- Database schema and structured logs contain no Peer ID in authorization, grant, access-token, or portal-session records.
- Electron binds only `127.0.0.1` for one attempt; Android App Link verification passes for the release certificate; Chrome uses the exact store extension ID callback and denies content-script access to `storage.local`.
- Google tokens and the Google client secret are absent from Clipp runtime storage, Relay Instance configuration, Relay Authentication frames, and Relay Instance logs.

## Sources

- [Google OpenID Connect guide](https://developers.google.com/identity/openid-connect/openid-connect)
- [Google OpenID Connect API reference](https://developers.google.com/identity/openid-connect/reference)
- [Google OAuth 2.0 for web-server applications](https://developers.google.com/identity/protocols/oauth2/web-server)
- [Google server-side ID-token verification](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token)
- [Google Cross-Account Protection](https://developers.google.com/identity/protocols/risc)
- [RFC 6749: OAuth 2.0 Authorization Framework](https://www.rfc-editor.org/rfc/rfc6749.html)
- [RFC 7636: Proof Key for Code Exchange](https://www.rfc-editor.org/rfc/rfc7636.html)
- [RFC 7662: OAuth 2.0 Token Introspection](https://www.rfc-editor.org/rfc/rfc7662.html)
- [RFC 7009: OAuth 2.0 Token Revocation](https://www.rfc-editor.org/rfc/rfc7009.html)
- [RFC 8252: OAuth 2.0 for Native Apps](https://www.rfc-editor.org/rfc/rfc8252.html)
- [RFC 9700: OAuth 2.0 Security Best Current Practice](https://www.rfc-editor.org/rfc/rfc9700.html)
- [RFC 10017: OAuth 2.0 for Browser-Based Applications](https://www.rfc-editor.org/rfc/rfc10017.html)
- [Chrome Extensions `identity` API](https://developer.chrome.com/docs/extensions/reference/api/identity)
- [Chrome Extensions `storage` API](https://developer.chrome.com/docs/extensions/reference/api/storage/)
- [Android App Links](https://developer.android.com/training/app-links/about)
- [Android web authentication guidance](https://developer.android.com/develop/ui/views/layout/webapps)
- [Android Keystore](https://developer.android.com/privacy-and-security/keystore)
- [Electron `shell`](https://www.electronjs.org/docs/latest/api/shell)
- [Electron `safeStorage`](https://www.electronjs.org/docs/latest/api/safe-storage)
