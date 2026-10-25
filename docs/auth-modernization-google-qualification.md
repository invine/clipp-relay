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

## Integrated local evidence (2026-10-25)

- Final reviewed relay revision: `009eb0c4e57d45d8a7312ba6ea3e15c09563366a`.
- PostgreSQL 18.6.1 TLS/SCRAM fixture under `/private/tmp/clipp-auth-pg18-20261025/`:
  `CLIPP_TEST_MIGRATION_CONFIG=/private/tmp/clipp-auth-pg18-20261025/migration.json CLIPP_TEST_SERVING_CONFIG=/private/tmp/clipp-auth-pg18-20261025/serving.json GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./internal/database` passed on schema revision 16; after restoring serving-role grants,
  `CLIPP_TEST_MIGRATION_CONFIG=/private/tmp/clipp-auth-pg18-20261025/migration.json CLIPP_TEST_SERVING_CONFIG=/private/tmp/clipp-auth-pg18-20261025/serving.json GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./internal/auth` passed, including Google callback signature/claim, provider outage, RAM-continuation, native-reset and OAuth lifecycle coverage.
- `GOCACHE=/private/tmp/clipp-go-cache bash scripts/verify.sh routine` and
  `GOCACHE=/private/tmp/clipp-go-cache CGO_ENABLED=1 bash scripts/verify.sh race`
  passed on the final reviewed revision with loopback networking enabled.
- This verifies configured local fixtures, not live Google account authorization or end-to-end runtime relay transfers. Those remain unqualified without provider/relay credentials, an Android device, and a registered extension ID.
