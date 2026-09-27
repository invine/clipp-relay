# Self-hosted mobile authorization callbacks

Date: 2026-09-05. Research supporting **Define Clipp runtime integration**, Q195. The subsequent user decision is recorded in [Define Clipp runtime integration](../issues/10-define-clipp-runtime-integration.md); recommendations below describe the research at the time of investigation. Documentation and default-branch source were inspected, not deployed applications. Source links may change with later releases.

## Findings

Self-hosting does not require every installation's hostname to appear in the Android APK. Immich uses a fixed private application scheme with a user-selected server. Nextcloud offers a different approach: browser authorization followed by client polling, with no authorization callback into the app. These are concrete precedents for avoiding a shared HTTPS callback service.

### Immich: private application scheme, with an optional server redirect bridge

Immich documents `app.immich:///oauth-callback` for both mobile platforms. When an identity provider will not accept that scheme, administrators can use their own Immich server's `/api/oauth/mobile-redirect` as an HTTPS redirect URI and enable the mobile redirect override. The official Google configuration example uses this override. There is no shared Immich callback hostname in this flow. [Immich OAuth documentation](https://docs.immich.app/administration/oauth/)

The mobile implementation resolves the API endpoint from the user's `serverUrl`, asks that server to start authorization, and supplies a `state` and PKCE `codeChallenge`. It opens browser authorization using `FlutterWebAuth2.authenticate` with the `app.immich` callback scheme. After callback, it submits the result, expected `state`, and `codeVerifier` to the selected server's callback API. [Immich mobile OAuth service](https://github.com/immich-app/immich/blob/main/mobile/lib/services/oauth.service.dart)

The Android manifest registers a callback activity for `app.immich`. It also contains verified `my.immich.app` links for other features, but the OAuth callback uses the private scheme. [Immich Android manifest](https://github.com/immich-app/immich/blob/main/mobile/android/app/src/main/AndroidManifest.xml)

The server bridge returns a temporary HTTP redirect. Its service constructs the fixed mobile URI with the incoming query string. The same service validates that expected state and a verifier were supplied, exchanges the provider authorization code through its OAuth repository, and issues an Immich session token. The override maps the mobile redirect URI to the deployment's configured HTTPS bridge URI for provider exchange. [Immich OAuth controller](https://github.com/immich-app/immich/blob/main/server/src/controllers/oauth.controller.ts), [Immich authentication service](https://github.com/immich-app/immich/blob/main/server/src/services/auth.service.ts)

**Applicability to Clipp (inference):** The fixed private callback scheme is directly relevant. Clipp already separates Google login from application authorization: Google returns to the relay's HTTPS callback; the relay then issues its own short-lived Clipp authorization code to the app. Consequently Clipp would not copy Immich's forwarding of the provider callback; its private URI would contain only Clipp's code and state, with redemption at the originating relay and PKCE verification there.

### Nextcloud: browser approval plus one-time polling

Nextcloud Login Flow v2 begins with an anonymous POST to the selected server's `/index.php/login/v2`. It returns a browser login URL and separate polling endpoint/token. The app opens the login URL in the default browser while polling. The token lasts 20 minutes; polling returns 404 while authorization is incomplete, then a one-time response containing server URL, login name, and an app password. The browser does not need to deliver those credentials through an app callback. [Nextcloud Login Flow v2 documentation](https://docs.nextcloud.com/server/latest/developer_manual/client_apis/LoginFlow/index.html#login-flow-v2)

The server source generates separate random login and polling tokens, looks up the polling token by its hash, deletes the completed record when retrieving credentials, and decrypts the generated app password for that response. This is an implemented protocol, not merely a proposed fallback. [Nextcloud LoginFlowV2Service](https://github.com/nextcloud/server/blob/master/core/Service/LoginFlowV2Service.php)

**Applicability to Clipp (inference):** An analogous relay-owned transaction could avoid Android callback registration entirely. Its costs are new transaction/polling endpoints, expiry and abuse controls, lifecycle handling while the browser is active, and potentially asking the user to return to Clipp manually. Nextcloud's protocol issues an app password; it is neither the OAuth authorization-code-with-PKCE flow nor evidence that Google device authorization should be used for smartphones.

## Standards boundary and recommendation

Private schemes are a standard native OAuth return mechanism. RFC 8252 requires their names to use reversed domain notation based on a domain under the application's publisher's control. That naming requirement does not mean operating an HTTPS callback service or associating every self-hosted relay domain with the APK. The RFC prefers claimed HTTPS redirects where practical and requires PKCE for public native clients. PKCE prevents redemption of an intercepted code without the initiating verifier, but does not establish the app's identity or prevent a different app from initiating its own authorization request. [RFC 8252 §§7.1–7.2, 8.1, 8.6](https://www.rfc-editor.org/rfc/rfc8252.html)

The OAuth device grant is a separate standard intended for constrained devices; its introduction explicitly excludes using it as a replacement for browser-based OAuth on capable smartphones. Nextcloud's application-specific polling protocol should not be conflated with that grant. [RFC 8628 §1](https://www.rfc-editor.org/rfc/rfc8628.html#section-1)

**Recommendation for discussion:** Prefer a fixed Clipp private callback scheme with the existing relay-issued code and PKCE flow if the requirement is arbitrary self-hosted relay URLs without maintaining a common callback website. Keep external-browser login, bind each pending transaction to its originating Relay Configuration, validate state, and redeem only with that relay. A Nextcloud-like polling flow is a legitimate alternative if eliminating application callback handling outweighs the extra protocol and lifecycle work. Neither recommendation is a recorded user decision.

Home Assistant and Bitwarden were considered but are not relied on here: their available documentation alone did not establish a sufficiently comparable current mobile browser flow within this bounded investigation.
