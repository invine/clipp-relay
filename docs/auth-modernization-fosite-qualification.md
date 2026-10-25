# Fosite dependency qualification (ticket 01)

Status: candidate pinned; full auth qualification is incomplete.

## Selected revisions

- `github.com/ory/fosite v0.49.0`, signed release tag at commit `653c812`. The
  upstream release page still marks v0.49.0 as latest as of 2026-10-25.
- `github.com/go-jose/go-jose/v3 v3.0.5`, an explicit module-graph override of
  Fosite's v3.0.3 requirement. This is **not a generally patched version**:
  upstream's [GHSA-mgcc-cww5-fp2q] says all v3 versions through 3.0.5 are
  affected and no v3 fix is available. The earlier [GO-2026-4945] entry says
  v3.0.5 fixes a separate JWE-decryption panic; it does not clear this JWK
  denial-of-service advisory. Fosite v0.49.0 compiles against v3.0.5, but that
  compile result is not an advisory fix.
- Fosite's graph still includes `github.com/dgraph-io/ristretto v1.0.0`, which
  the module author retracts for incompatibility with the later v0.2.0 API. The
  v1 generic API is required by Fosite's JWKS client-auth package; Clipp only
  registers public OAuth clients and does not enable that client-auth method.
  No known security advisory was identified for this retracted revision. Do not
  upgrade or replace it without proving the Fosite package compiles and the
  public-client paths remain unchanged.

## Known Fosite parser issue

Fosite issue [#882] reports unbounded allocation in
`token/hmac.HMACStrategy.Signature` because it splits the whole attacker
controlled token before checking segment count. The proposed fix remains in
open PR [#890], rather than a released Fosite version. The project currently
bounds OAuth POST bodies at 16 KiB. The native adapter now requires the exact
94-character native token shape and validates its fixed-size base64url message
and signature segments before calling Fosite signature or validation methods.
The same check protects tokens from bearer use through the private issuer API.

The go-jose/v3 advisory is specifically reachable when untrusted JWK JSON with
an RSA private key is unmarshaled: `JSONWebKey.UnmarshalJSON` calls
`rawJSONWebKey.rsaPrivateKey`, which calls `rsa.PrivateKey.Validate`. Fosite
v0.49.0's `DefaultClientAuthenticationStrategy` only calls
`findClientPublicJWK` for `private_key_jwt` client assertions; the registered
client authentication method is checked first and clients with method `none`
are rejected. Fosite's authorization-code and refresh token formats use the
HMAC strategy and do not need JWK parsing. The isolated composition registers
exactly the three public clients with `token_endpoint_auth_method=none` and
supplies a Fosite client-authentication strategy that rejects Authorization
credentials, client secrets, and client assertions before Fosite can parse a
JWT assertion or call `findClientPublicJWK`. Authorization query validation
admits only the six Clipp fields, so signed request objects are not passed
through. A non-DB test exercises Fosite's `NewAccessRequest` boundary and
confirms an assertion on a registered public client is rejected. The
real-PostgreSQL HTTP lifecycle test is implemented but has not run because its
disposable database fixture is unavailable. Runtime non-reachability at the
complete HTTP boundary therefore remains unproven until that test runs. If the
boundary cannot be established in the real HTTP path, v0.49.0 with go-jose/v3
is incompatible with the advisory requirement.
Fosite's published releases currently top out at v0.49.0, which still imports
go-jose/v3; go-jose/v4 is not a drop-in import-path update for this Fosite
revision. No released Fosite revision eliminating v3 was found.

The retracted Ristretto module, the open Fosite parser issue, and the
conditional go-jose reachability mean this is a candidate dependency set, not a
general advisory-clearance claim. A complete reachability scan also remains
outstanding: the local `govulncheck v1.1.4`
module-only scan reported legacy `golang.org/x/crypto/openpgp` GO-2026-5932 in
the wider existing module graph, but `go mod why golang.org/x/crypto/openpgp`
shows the package is not imported by the main module. Its source scan panicked
inside x/tools v0.29.0 on Go 1.27.3 (`typesinternal.ForEachElement`, `panic:
Int`), so it did not establish reachable-symbol results.

## Local evidence

- `go list -m`: Fosite v0.49.0, go-jose/v3 v3.0.5, Ristretto v1.0.0, x/crypto
  v0.57.0.
- `go mod verify`: passed.
- `go test ./internal/auth/...`: passed. Auth tests needing durable PostgreSQL
  skipped because `CLIPP_TEST_SERVING_CONFIG` was not configured.
- A real PostgreSQL lifecycle proof was unavailable: Docker CLI could not reach
  a daemon, and no local PostgreSQL server was present. This does not satisfy
  ticket 01's database acceptance requirement.

[GO-2026-4945]: https://pkg.go.dev/vuln/GO-2026-4945
[GHSA-mgcc-cww5-fp2q]: https://github.com/go-jose/go-jose/security/advisories/GHSA-mgcc-cww5-fp2q
[#882]: https://github.com/ory/fosite/issues/882
[#890]: https://github.com/ory/fosite/pull/890
