# Research cross-runtime Google authorization

Type: research
Status: resolved
Blocked by:

## Question

Against official Google OAuth/OIDC, Chrome extension, Android, Electron/browser, and OAuth security guidance, what authorization-code-with-PKCE and renewable-grant architecture can securely serve all three Clipp runtimes, keep Google tokens out of relay data-plane processes, support a verified-email administrator allowlist, rotate and revoke account credentials without storing Device Identities, and fail safely across Coordinator or database outages?

## Comments

## Answer

[Cross-runtime Google authorization](../research/cross-runtime-google-authorization.md) recommends a two-hop design: the Coordinator alone completes Google OIDC, while Electron, Android, and the Chrome extension are fixed public Clipp clients using authorization code plus mandatory S256 PKCE. Opaque Relay access tokens are checked through protected introspection; renewable login grants rotate with reuse detection and store no Peer ID or other Device Identity data.
