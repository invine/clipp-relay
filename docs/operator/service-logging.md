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
{"time":"2026-10-11T10:22:05Z","level":"WARN","msg":"portal login","event":"portal.login","outcome":"uncertain","reason":"commit_unconfirmed","request_id":"44444444444444444444444444444444"}
{"time":"2026-10-11T10:22:06Z","level":"INFO","msg":"http request_refused","event":"http.request_refused","outcome":"refused","reason":"invalid_route","request_id":"55555555555555555555555555555555"}
```

`portal.login/started` means the server stored a sign-in transaction and issued a provider redirect. The callback uses a new request ID; `portal.login/success` means the Portal Session committed, not that a browser received its cookie. `refused/verification_failed` points to provider-token verification, while `failed/provider_unavailable` points to a provider transport or response failure. The fixed catalog has no stage field, so these rows cannot identify every internal callback substep. `uncertain/commit_unconfirmed` means the server could not prove the Portal Session commit outcome; it does not claim a browser cookie was issued.

`oauth.authorize/success` means a validated authorization request reached the consent page or sign-in redirect. `oauth.consent/success` means an authorization code committed, and `oauth.code_exchange/success` or `oauth.refresh/success` means the credentials committed before the HTTP response was written. `refused/expired_credentials`, `refused/grant_limit`, and `refused/replay_detected` separate common policy cases; `uncertain/commit_unconfirmed` means the server could not prove the transaction result. A replay refusal is logged after its grant-termination transaction commits. None of these rows proves client receipt or Relay Authentication. Request IDs are independent random diagnostics, never OAuth state, a cookie, or a credential. Unknown routes and wrong methods emit `http.request_refused` with `invalid_route` or `invalid_method`; raw URL, query, header and body values are excluded. Routine successful portal pages and assets emit no activity row.

## Relay connection and authentication rows

These examples describe one physical TCP connection. The IDs are generated by the relay solely for diagnostics. They are unrelated to a Peer ID, Relay Account, token, or client identity.

```json
{"time":"2026-10-11T10:20:00Z","level":"INFO","msg":"relay connection","event":"relay.connection","outcome":"success","reason":"none","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp"}
{"time":"2026-10-11T10:20:01Z","level":"INFO","msg":"relay authentication","event":"relay.authentication","outcome":"success","reason":"none","request_id":"19df8c2080e2a813aeb2ed99e321b3c1","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp","duration_ms":4}
{"time":"2026-10-11T10:20:01Z","level":"INFO","msg":"relay session","event":"relay.session","outcome":"started","reason":"none","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp"}
{"time":"2026-10-11T10:22:00Z","level":"INFO","msg":"relay session","event":"relay.session","outcome":"success","reason":"renewed","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp"}
{"time":"2026-10-11T10:22:45Z","level":"INFO","msg":"relay session","event":"relay.session","outcome":"ended","reason":"policy_changed","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp"}
```

A connection row means a Noise-authenticated physical connection was admitted; it does not mean Relay Authentication succeeded. Each authentication stream gets its own `request_id`. `relay.session/started` means the server installed a Relay Session after credential and quota checks. Renewal keeps the same `session_id` because it occurs on the same physical connection. A new connection, including one replacing the same Peer ID, gets a new ID. IDs leave process memory at disconnect. TCP and WebSocket/WebRTC transport values come from the actual libp2p connection address; an unknown address is labeled `unknown`.

`relay.connection/ended/preauth_expired` identifies a connection that did not authenticate within ten seconds. `relay.authentication/refused/invalid_request` covers malformed frames, while `invalid_credentials`, `account_mismatch`, `rate_limited`, `session_limit`, and `draining` identify distinct supported refusals. A known credit exhaustion still returns `quota_exhausted` on the wire, but the fixed authentication catalog has no truthful quota exhaustion pair; it emits no authentication outcome row for that case. Quota funding diagnostics own that category. Generic quota failures are `failed/quota_unavailable` and do not prove whether the database or clock was the cause.

Relay Session ends distinguish `connection_closed`, `session_expired`, `replaced`, `policy_changed`, `reporting_failed`, and `draining`. The current accounting error contract does not distinguish clock failure from other temporary reporting failures, so this slice uses `reporting_failed` rather than asserting `clock_unavailable`. Handshake failures rejected before a libp2p connection callback have no relay-side producer here. A server-side authentication success proves neither a reservation nor a circuit nor client receipt. The fixed shared rate limits can suppress individual rows; `log.suppressed` is a best-effort aggregate with no identity list. Quiet periods emit no heartbeat.

Focused local checks for this slice: `go test -count=1 ./internal/relay -run 'Test(RelayAuthDiagnosticJourney|PolicyCloseEmitsSessionEndWithoutIdentity|RelaySessionExpiryIsDistinctFromDisconnect|RelayAuthRefusalIsBoundedAndPrivate|PreauthenticationExpiresWithoutRelaySession|MalformedAuthFrameDoesNotLeakPayload|DrainEndsRelaySessionWithDrainReason|QuotaReporterClosesSessionsWithReportingReason|QuotaExhaustionKeepsAuthenticationReasonHonest|PhysicalTransportDiagnosticIDsNeverCarryAcrossConnections|ClientDisconnectDuringDrainKeepsConnectionReason)$'`. The complete relay suite, routine verification and race verification provide broader regression evidence. These local checks do not establish deployed behavior.

## Reservation, circuit and Rendezvous rows

```json
{"time":"2026-10-11T10:23:00Z","level":"INFO","msg":"relay authentication","event":"relay.authentication","outcome":"success","reason":"none","request_id":"19df8c2080e2a813aeb2ed99e321b3c1","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp","duration_ms":4}
{"time":"2026-10-11T10:23:01Z","level":"INFO","msg":"relay reservation","event":"relay.reservation","outcome":"refused","reason":"draining","request_id":"98e1147caaf8ba28e9564986b12c7d2a","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"unknown"}
{"time":"2026-10-11T10:24:00Z","level":"INFO","msg":"relay reservation","event":"relay.reservation","outcome":"success","reason":"none","transport":"unknown"}
{"time":"2026-10-11T10:25:00Z","level":"INFO","msg":"relay reservation","event":"relay.reservation","outcome":"success","reason":"renewed","transport":"unknown"}
```

Authentication success does not imply a reservation. The refused row above came from a protected HOP stream on the same physical connection, so it has connection and request IDs. The stock reservation callbacks for new and renewed reservations have no physical connection or request association; their `transport` is `unknown` and they omit both IDs. A stock success callback means the relay allowed the reservation; it does not prove the client received its response. A malformed or unreadable denied HOP request has no truthful RESERVE/CONNECT label and produces no operation row.

```json
{"time":"2026-10-11T10:26:00Z","level":"INFO","msg":"relay circuit","event":"relay.circuit","outcome":"started","reason":"none","transport":"unknown"}
{"time":"2026-10-11T10:26:00Z","level":"INFO","msg":"relay circuit","event":"relay.circuit","outcome":"success","reason":"none","transport":"unknown"}
{"time":"2026-10-11T10:26:07Z","level":"INFO","msg":"relay circuit","event":"relay.circuit","outcome":"ended","reason":"none","transport":"unknown","duration_ms":7000}
```

Stock `started` means circuit capacity was allocated; `success` means the stock CONNECT handler returned OK. `ended` uses the stock callback's measured circuit lifetime. These aggregate callbacks cannot be joined to a reservation, Relay Session, transport, Peer ID or specific HOP request. They do not show bytes, payload, Direct Connection success or Clip delivery. Resource limit and protocol refusals are separate fixed reasons; a failed STOP setup is `failed/connection_failed`. A close after setup failure can still have a measured lifetime.

```json
{"time":"2026-10-11T10:27:00Z","level":"INFO","msg":"relay rendezvous v2 register","event":"relay.rendezvous.v2.register","outcome":"refused","reason":"lease_unavailable","request_id":"43658814e868ae379956c3d3e53e5400","session_id":"8b754e3d75693c1f218dce2fc355671a","duration_ms":1}
{"time":"2026-10-11T10:27:02Z","level":"INFO","msg":"relay rendezvous v2 register","event":"relay.rendezvous.v2.register","outcome":"success","reason":"none","request_id":"096b2b41eb6c39897ea093f6b9f6df22","session_id":"8b754e3d75693c1f218dce2fc355671a","duration_ms":1}
{"time":"2026-10-11T10:27:03Z","level":"INFO","msg":"relay rendezvous v1 find","event":"relay.rendezvous.v1.find","outcome":"success","reason":"none","request_id":"f52bbaed7182434a917eb4f72d2ebd25","session_id":"730217c0b963dc4c42b3c8392d575455","duration_ms":1}
{"time":"2026-10-11T10:27:04Z","level":"INFO","msg":"relay rendezvous v2 find","event":"relay.rendezvous.v2.find","outcome":"refused","reason":"lease_unavailable","request_id":"9581afec6d0bf5fe6bd45f25bb0bd83e","session_id":"730217c0b963dc4c42b3c8392d575455","duration_ms":1}
```

The version is the actual protocol on that stream. A `find` miss is logged as `refused/lease_unavailable` even though the compatibility wire response is `ok:true` with no record. `register/lease_unavailable` means the current connection lacked a live stock reservation. No row includes the looked-up Peer ID, Signed Peer Record, address, topic or response body. The outcome is observed after server processing and does not assert client receipt. A request that fails before a valid action can be identified has no truthful version-specific operation row. Shared output limits can suppress repeated rows.

Focused local checks: `go test -count=1 ./internal/relay -run 'Test(ReservationDiagnosticsDistinguishAuthenticationAndStockRenewal|CircuitDiagnosticsUseOnlyStockAggregateEvidence|RendezvousDiagnosticsFollowVersionAndLeaseOutcome|RendezvousRepeatedMissesRemainBounded)$'`. The routine and race profiles are the required broader gates. This local evidence does not establish deployed behavior.

## Quota and accounting rows

```json
{"time":"2026-10-11T10:28:00Z","level":"INFO","msg":"quota funding","event":"quota.funding","outcome":"success","reason":"none","duration_ms":8}
{"time":"2026-10-11T10:28:01Z","level":"WARN","msg":"quota funding","event":"quota.funding","outcome":"uncertain","reason":"allocation_unconfirmed","duration_ms":3000}
{"time":"2026-10-11T10:28:03Z","level":"INFO","msg":"quota funding","event":"quota.funding","outcome":"success","reason":"none","duration_ms":9}
{"time":"2026-10-11T10:28:04Z","level":"INFO","msg":"quota credit_retired","event":"quota.credit_retired","outcome":"success","reason":"discarded"}
{"time":"2026-10-11T10:28:05Z","level":"WARN","msg":"quota reporting","event":"quota.reporting","outcome":"failed","reason":"reporting_failed","duration_ms":3}
{"time":"2026-10-11T10:28:05Z","level":"INFO","msg":"relay session","event":"relay.session","outcome":"ended","reason":"reporting_failed","session_id":"8b754e3d75693c1f218dce2fc355671a","transport":"tcp"}
```

`quota.funding/success` means a credit receipt was committed and proved usable for the current account and week. It is Quota Committed, including still-unused credit, not measured payload transfer. A lost commit acknowledgement produces `uncertain/allocation_unconfirmed`; a later success can be proof of that same receipt, not another allocation. The rows give no amount, account identity or estimate of crash-stranded credit. `refused/quota_exhausted` is an expected allowance limit and `refused/account_blocked` is a policy refusal. `failed/database_unavailable` and `failed/clock_unavailable` mean safe credit could not be established. A known rollback can use `failed/database_unavailable` while a lost commit acknowledgement remains uncertain.

`quota.credit_retired/success/discarded` means process-local usable credit was invalidated after a policy or generation change; `expired` means its local week deadline passed. Ordinary retirement does not refund Quota Committed. The stock HOP/STOP byte reporter logs no successful callback rows. On a reporting failure, `quota.reporting/failed/reporting_failed` is aggregate evidence without a request or Relay Session ID; all live sessions for the sampled account are closed, and their individual `relay.session/ended/reporting_failed` rows may carry real physical-connection IDs. Reporter callbacks may charge a later association for an old connection's tail bytes, so these rows cannot establish exact transferred bytes or client receipt. Repeated outcomes share the fixed activity and per-tuple rate limits; individual rows and the best-effort loss summary may be absent.

Focused checks: `go test -count=1 ./internal/quota -run 'Test(QuotaDiagnostics|LostCommitReplyReconcilesSameReceipt|KnownRollbackRetriesWithoutDoubleDebit|DatabaseOutageOnlyAllowsConfirmedLocalCredit|AllocationProcessCrashAtCommitBoundary)$'` with a disposable real PostgreSQL 18 TLS/SCRAM fixture, plus `go test -count=1 ./internal/relay -run 'Test(QuotaReporterClosesSessionsWithReportingReason|ConfirmedPartialCreditClosesAllAccountSessionsOnExhaustion|ReporterChargesCurrentPeerAssociationAndRecordsUnattributedTail)$'`. The real SQL fixture must be recorded separately from routine checks, which can skip it. Local checks do not establish deployed behavior.
