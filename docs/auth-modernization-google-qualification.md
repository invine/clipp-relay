# Google authentication dependency qualification

Qualification snapshot: 2026-10-25. This records the dependency selection and
security review for the Google exchange and ID-token verifier implementation.
It does not replace the required real-PostgreSQL callback tests or final
integration qualification.

## Selected revisions

| Module | Revision | Selection |
| --- | --- | --- |
| `golang.org/x/oauth2` | `v0.37.0` | Latest tagged revision on the official Go repository at the qualification date. |
| `github.com/coreos/go-oidc/v3` | `v3.21.0` | Latest tagged v3 revision on the maintainer repository at the qualification date. |
| `github.com/go-jose/go-jose/v4` | `v4.1.6` | Explicitly pinned above go-oidc's `v4.1.4` requirement to include current JWK parser fixes. |

The application uses `oauth2.AuthStyleInParams`; it does not use provider
discovery, token refresh helpers or provider tokens after extracting the ID
token. A single stock `oidc.RemoteKeySet` handles Google keys.

## Advisory review

- The oauth2 and go-oidc maintainer advisory pages listed no published
  advisories for their selected modules at review time.
- go-jose `v4.1.6` fixes high severity
  [GHSA-mgcc-cww5-fp2q](https://github.com/go-jose/go-jose/security/advisories/GHSA-mgcc-cww5-fp2q),
  which affects v4 through `v4.1.5`. The same revision includes the fixes for
  prior CBC-HMAC and JSON parsing advisories patched in `v4.1.5`.
- Source checks: [oauth2 advisories](https://github.com/golang/oauth2/security/advisories),
  [go-oidc advisories](https://github.com/coreos/go-oidc/security/advisories),
  [go-jose v4.1.6 release](https://github.com/go-jose/go-jose/releases/tag/v4.1.6).

`govulncheck v1.9.0` ran against `./internal/auth` with Go `1.27.3`. It found
zero reachable symbol or package vulnerabilities across the 24 scanned modules.
It reported one module-only result, `GO-2026-5932`, for the unmaintained
`golang.org/x/crypto/openpgp` package in `golang.org/x/crypto v0.57.0`; the
application does not import that package. This scan covered the auth package's
reachable graph, not every package in the repository.

## Remaining qualification

The auth HTTP tests use a disposable PostgreSQL fixture selected by
`CLIPP_TEST_SERVING_CONFIG`. That variable was unavailable for this handoff, so
the callback tests skipped. Their public-operation behavior, database-backed
transaction guarantees, and full repository verification remain unqualified.
