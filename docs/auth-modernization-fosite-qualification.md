# Fosite dependency qualification (ticket 01)

Status: candidate pinned; integrated PostgreSQL and routine/race qualification passed on relay revision `e9d2e0e` (2026-10-25). The broader module vulnerability scan caveat below remains.

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
confirms an assertion on a registered public client is rejected. The HTTP
endpoint also rejects client assertions at its strict form-field boundary
before Fosite; this is covered by
`TestNativeTokenHTTPRejectsClientAssertionsBeforeFosite`. The real-PostgreSQL
HTTP code/PKCE/refresh lifecycle and native access-auth tests passed on the
integrated revision below. This establishes the configured public-client HTTP
path, but does not replace the separate full-repository vulnerability scan
caveat below. If future configuration enables assertion client authentication,
requalify the JOSE reachability boundary.
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
- Integrated relay revision: `e9d2e0e29e6bc3a3a58232e838a6894730553c21`.
- Disposable PostgreSQL 18.6.1 TLS/SCRAM:
  `CLIPP_TEST_MIGRATION_CONFIG=/private/tmp/clipp-auth-pg18-20261025/migration.json CLIPP_TEST_SERVING_CONFIG=/private/tmp/clipp-auth-pg18-20261025/serving.json GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./internal/database` passed on schema revision 16. The package resets schema/grants; serving-role grants were restored before auth tests.
- `CLIPP_TEST_MIGRATION_CONFIG=/private/tmp/clipp-auth-pg18-20261025/migration.json CLIPP_TEST_SERVING_CONFIG=/private/tmp/clipp-auth-pg18-20261025/serving.json GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./internal/auth` passed against the same fixture, including native HTTP lifecycle, reset ambiguity and post-reset Portal Session reauthorization tests.
- `GOCACHE=/private/tmp/clipp-go-cache bash scripts/verify.sh routine` and
  `GOCACHE=/private/tmp/clipp-go-cache CGO_ENABLED=1 bash scripts/verify.sh race` passed on the integrated revision with loopback networking enabled.
- Live provider login, Android-device and registered-extension runtime acceptance remain separate release gates.

[GO-2026-4945]: https://pkg.go.dev/vuln/GO-2026-4945
[GHSA-mgcc-cww5-fp2q]: https://github.com/go-jose/go-jose/security/advisories/GHSA-mgcc-cww5-fp2q
[#882]: https://github.com/ory/fosite/issues/882
[#890]: https://github.com/ory/fosite/pull/890
