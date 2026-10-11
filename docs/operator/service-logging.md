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
