# Relay observability source inventory

Inspected on 2026-10-11 in the selected `clipp-relay` checkout at
`d59a373fe8689a80404b04e99946f9bdbfb47ec1`. These are source findings, not
measurements of the deployed image or cluster. Paths below are relative to that
selected repository root.

## Current signals

- The stdout JSON logger is created at `cmd/clipp-relay/main.go:27`. The
  `service starting` row at line 93 precedes relay construction, publication and
  HTTP serving; it does not establish readiness.
- `internal/service/service.go:102` serves private health and metrics. Existing
  metrics cover readiness, Rendezvous version counts, deletion and cleanup.
  Deletion and cleanup are fetched synchronously during a scrape at lines 138
  and 153; accepted snapshot semantics require bounded background sampling.
- `internal/relay/circuits.go:13` maintains active circuits and discards other
  stock tracer outcomes. Resource Manager metrics are disabled at
  `internal/relay/server.go:206`.

## Instrumentable serving-process journeys

| Journey | Observable boundary | Source anchors |
| --- | --- | --- |
| Lifecycle | Validation, listeners serving, publication usable/unusable, stop request, drain completion/deadline/force, process exit | `cmd/clipp-relay/main.go:93`, `:136`, `:190`, `:206`, `:212`; `internal/service/service.go:215` |
| Portal | Login initiation, provider exchange/verification, Portal Session issuance, sign-out, owner and administrator mutations, revision conflicts and uncertain commits | `internal/auth/auth.go:408`, `:471`, `:633`, `:660`, `:959`, `:993`; `internal/auth/admin.go:63`, `:432`, `:609`, `:692` |
| OAuth | Authorization validation and consent, code issuance/exchange, refresh, expiry and replay rejection | `internal/auth/oauth.go:88`, `:126`, `:203`, `:265`, `:388`, `:488` |
| Discovery/publication | Discovery served/refused; missing/stale/invalid publication; API failure and recovery after fresh valid evidence | `internal/relay/discovery.go:195`; `internal/publication/publication.go:194`, `:218`, `:257`, `:295` |
| Connections and sessions | Admission, preauthentication expiry, authentication and renewal, replacement, mismatch, ceilings, expiry/disconnect/policy invalidation/drain | `internal/relay/server.go:274`, `:293`, `:384`, `:396`, `:510`, `:660`; `internal/relay/reporter.go:90` |
| Reservations and circuits | HOP permission rejection, stock reservation and renewal outcomes, circuit request/open/close/duration, Rendezvous operations/version | `internal/relay/server.go:126`, `:245`; `internal/relay/circuits.go:13`; `internal/relay/rendezvous.go:63`, `:105`, `:404` |
| Quota/accounting | Funding accepted/exhausted/unavailable, ambiguous reconciliation, credit discarded/expired, charged/unattributed endpoint bytes, reporter failure | `internal/quota/quota.go:150`, `:178`, `:312`, `:447`; `internal/relay/reporter.go:33` |
| Dependencies/maintenance | DB admission/acquisition/operation outcomes, Google token/JWKS outcomes, publication API and deletion journal outcomes, cleanup/deletion backlog, quota clock | `internal/database/runtime.go:47`, `:89`, `:99`; `internal/auth/auth.go:633`, `:705`; `internal/auth/cleanup.go:17`, `:197`; `internal/auth/deletion.go:663`; `internal/quota/quota.go:93`, `:202`, `:220` |

Stock aggregate tracer callbacks must not be presented as session-correlated
evidence if the producer does not supply a safe association. Any handshake or
stream measurements need a bounded fixed-scope Resource Manager reporter before
claiming that coverage.

## Absent producers and visibility limits

- Verified backup/PITR freshness, WAL continuity, repository retention and
  encrypted-bundle coverage have no serving-process producer. The accepted
  recovery-point and recovery-expiry implementation tickets remain unstarted.
- Certificate convergence and current served-leaf expiry lack the accepted
  certificate-watcher producer. Successful startup TLS does not establish
  continuing convergence.
- Relay process resource observations and application DB pool statistics cannot
  establish DB host/PVC capacity, archive storage, WAL continuity, or external
  provider infrastructure health.
- Deletion-journal evidence is not proof of database recoverability.
- A later new session cannot be attributed to the same client as a previous
  failed connection without the stable identifiers forbidden by the policy.
- Direct Connection results, endpoint acceptance and Clip delivery cannot be
  established from relay circuit acceptance or forwarded opaque bytes.

The specification should distinguish observations supplied by current components,
observations that are temporarily stale, and observations whose producer is absent.
Unknown must not be rendered as healthy or as a measured zero.
