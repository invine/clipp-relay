# Service lifecycle rows

These are example stdout JSON rows. The time and schema revision shown are illustrative. Rows contain fixed event, outcome and reason values; raw configuration errors, addresses, credentials and identities are excluded.

```json
{"time":"2026-10-11T10:00:00Z","level":"INFO","msg":"service startup","event":"service.startup","outcome":"success","reason":"none","schema_revision":7}
{"time":"2026-10-11T10:00:01Z","level":"INFO","msg":"service serving","event":"service.serving","outcome":"success","reason":"none"}
{"time":"2026-10-11T10:00:03Z","level":"INFO","msg":"service readiness","event":"service.readiness","outcome":"recovered","reason":"none"}
```

`service.startup` means validation completed. `service.serving` means the HTTP listeners bound. Only a successful or recovered `service.readiness` row means the service observed usable publication at that moment. A later `failed` readiness row with `not_published`, `publication_stale` or `draining` withdraws that observation. The private `/readyz` probe remains the current readiness check. An initial `failed/not_published` row may appear before startup finishes. A row can be lost under the bounded output policy, so absence is not proof of health or failure.

```json
{"time":"2026-10-11T10:10:00Z","level":"INFO","msg":"service drain","event":"service.drain","outcome":"started","reason":"none"}
{"time":"2026-10-11T10:10:30Z","level":"WARN","msg":"service drain","event":"service.drain","outcome":"failed","reason":"deadline","duration_ms":30000}
{"time":"2026-10-11T10:10:30Z","level":"INFO","msg":"service stopped","event":"service.stopped","outcome":"success","reason":"none"}
```

`service.drain/success` means the relay drain returned successfully. `failed/deadline` means its grace expired; `failed/forced` means a second stop signal canceled it. `service.stopped` describes HTTP listener shutdown separately. The service does not claim that a remote client received a transfer. Diagnostics use a 256-row queue, per-tuple and shared rate budgets, and a 100 ms maximum flush wait. Loss summaries are best effort and no idle heartbeat is emitted.

Focused local checks for this slice: `go test -count=1 ./internal/diagnostic ./internal/service ./cmd/clipp-relay` (local loopback permission required for service listener tests). The full routine and race gates remain the verification commands in [relay verification](../verification.md). Local test results do not establish deployed behavior.

## Portal sign-in and OAuth credentials

Illustrative rows from separate HTTP requests:

```json
{"time":"2026-10-11T10:20:00Z","level":"INFO","msg":"portal login","event":"portal.login","outcome":"started","reason":"none","request_id":"0123456789abcdef0123456789abcdef"}
{"time":"2026-10-11T10:20:02Z","level":"INFO","msg":"portal login","event":"portal.login","outcome":"success","reason":"none","request_id":"fedcba9876543210fedcba9876543210"}
{"time":"2026-10-11T10:21:00Z","level":"INFO","msg":"oauth consent","event":"oauth.consent","outcome":"success","reason":"none","client_type":"android","request_id":"11111111111111111111111111111111"}
{"time":"2026-10-11T10:21:03Z","level":"INFO","msg":"oauth code_exchange","event":"oauth.code_exchange","outcome":"success","reason":"none","client_type":"android","request_id":"22222222222222222222222222222222"}
{"time":"2026-10-11T10:22:00Z","level":"INFO","msg":"oauth refresh","event":"oauth.refresh","outcome":"refused","reason":"replay_detected","client_type":"android","request_id":"33333333333333333333333333333333"}
```

`portal.login/started` means the server stored a sign-in transaction and issued a provider redirect. The callback uses a new request ID; `portal.login/success` means the Portal Session committed, not that a browser received its cookie. `refused/verification_failed` points to provider-token verification, while `failed/provider_unavailable` points to a provider transport or response failure. The fixed catalog has no stage field, so these rows cannot identify every internal callback substep. An unconfirmed Portal Session commit is reported as failure rather than success.

`oauth.authorize/success` means a validated authorization request reached the consent page or sign-in redirect. `oauth.consent/success` means an authorization code committed, and `oauth.code_exchange/success` or `oauth.refresh/success` means the credentials committed before the HTTP response was written. `refused/expired_credentials`, `refused/grant_limit`, and `refused/replay_detected` separate common policy cases; `uncertain/commit_unconfirmed` means the server could not prove the transaction result. A replay refusal is logged after its grant-termination transaction commits. None of these rows proves client receipt or Relay Authentication. Request IDs are independent random diagnostics, never OAuth state, a cookie, or a credential. Routine successful portal pages and assets emit no activity row.
