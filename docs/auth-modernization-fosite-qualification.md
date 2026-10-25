# Fosite dependency qualification (ticket 01)

Status: candidate pinned; full auth qualification is incomplete.

## Selected revisions

- `github.com/ory/fosite v0.49.0`, signed release tag at commit `653c812`. The
  upstream release page still marks v0.49.0 as latest as of 2026-10-25.
- `github.com/go-jose/go-jose/v3 v3.0.5`, an explicit module-graph override of
  Fosite's v3.0.3 requirement. v3.0.5 fixes [GO-2026-4945], a panic in JWE
  decryption reported for go-jose/v3 versions before v3.0.5. Fosite compiles
  against the same v3 package path at v3.0.5.
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
bounds OAuth POST bodies at 16 KiB; the native adapter must also reject token
strings above its small protocol bound before calling any Fosite signature or
validation method, including for tokens taken from bearer headers. The accepted
native format is 94 characters, so a small fixed ceiling covers it with room
for bounded parsing. The final adapter has not yet added this gate.

The retracted Ristretto module and the open Fosite parser issue mean this is a
candidate dependency set, not a general advisory-clearance claim. A complete
reachability scan also remains outstanding: the local `govulncheck v1.1.4`
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
[#882]: https://github.com/ory/fosite/issues/882
[#890]: https://github.com/ory/fosite/pull/890
