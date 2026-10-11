# Relay service logging operator guide

The [approved logging specification](../../.scratch/relay-service-visibility/spec.md),
[fixed event catalog](../../.scratch/relay-service-visibility/logging-catalog.json),
and [implementation evidence](../../.scratch/relay-service-visibility/run.md) live
with the relay service source.

The serving process writes one stream of bounded JSON diagnostic rows to stdout. Read an event as evidence of the stated server-side boundary at its timestamp. A missing row is inconclusive: rate limits, a full queue, a blocked or failed stdout writer, or process exit can discard it. Use the live readiness probe and durable business records when the decision requires current state or proof of a mutation.

## Captured local rows

These rows were captured on 2026-10-11 from `TestIntegratedHTTPRelayAndReadinessRowsMatchCatalog` against the shared logger, and `TestCleanupLogsFailedPassWithoutRoutineSuccessRows` against a disposable PostgreSQL 18 TLS/SCRAM fixture. They are actual local output, not a deployed sample. IDs are short-lived random diagnostics; the fixture's account, credential, Peer ID, URL and body canaries did not appear.

```json
{"event":"service.readiness","level":"INFO","msg":"service readiness","outcome":"success","reason":"none","time":"2026-10-11T14:06:12.743358Z"}
{"duration_ms":0,"event":"relay.discovery","level":"INFO","msg":"relay discovery","outcome":"success","reason":"none","request_id":"be8f731127013343abfa8402cb55566e","time":"2026-10-11T14:06:12.743417Z"}
{"duration_ms":0,"event":"portal.logout","level":"INFO","msg":"portal logout","outcome":"refused","reason":"origin_rejected","request_id":"312a5dd62468a498daf44abdd95bd26a","time":"2026-10-11T14:06:12.743695Z"}
{"duration_ms":0,"event":"relay.authentication","level":"INFO","msg":"relay authentication","outcome":"success","reason":"none","request_id":"35f1d850556d44c639997305d32161d4","session_id":"c0a4d57237eb3138a15038d0a0ca73f7","time":"2026-10-11T14:06:12.751583Z","transport":"tcp"}
{"event":"relay.reservation","level":"INFO","msg":"relay reservation","outcome":"success","reason":"none","time":"2026-10-11T14:06:12.752237Z","transport":"unknown"}
{"event":"log.suppressed","level":"WARN","msg":"diagnostic records suppressed","outcome":"observed","queue_full_count":0,"rate_limited_count":1,"reason":"diagnostic_loss","time":"2026-10-11T14:06:12.752985Z","window_seconds":10}
{"duration_ms":0,"event":"maintenance.cleanup","level":"WARN","msg":"maintenance cleanup","outcome":"failed","reason":"database_unavailable","time":"2026-10-11T14:05:49.689491Z"}
```

The fixture drove the HTTP and libp2p operations through their handlers. It observed a portal origin refusal after a readiness and discovery success, then a physical TCP connection's authentication and an aggregate stock reservation callback. The reservation row omits both IDs because that callback cannot identify its physical connection. One hundred concurrent portal refusals produced a best-effort loss summary; the summary's count describes rows omitted in that window, not a complete refusal count. `duration_ms:0` means the measured operation rounded down to less than one millisecond.

The following sections use additional illustrative rows to explain other fixed catalog outcomes. They are examples of interpretation, not a claim that each occurred in the captured fixture.

## Severity, correlation and loss

`INFO` includes meaningful successes and expected credential, quota and per-request Safety Limit refusals. `WARN` includes dependency degradation, stale observations, global overload, uncertain commits and unsuccessful drains. `ERROR` is reserved for fatal startup or listener failure and broken invariants. `reason` is a fixed safe category, never an upstream error message. An `uncertain` commit is not a success or rollback claim; check the durable workflow's separate proof path.

Each admitted HTTP or protocol request receives a fresh random `request_id` when generation succeeds. Headers, OAuth state and cookies cannot supply it. A `session_id` belongs only to one physical Relay Session and expires when that connection ends; it is not a Portal Session, Login Grant, account or Peer ID. Stock reservation and circuit callbacks have aggregate evidence and cannot inherit these IDs. A request ID on one OAuth callback cannot join it to the earlier redirect.

Activity is limited to 20 rows per second with burst 40, and each fixed tuple to one row per second with burst five. Lifecycle, observation, `ERROR` and loss-summary rows have a separate four-per-second, burst-eight budget. One writer uses a non-blocking 256-row queue, each row at most 2 KiB; shutdown waits at most 100 ms for best-effort flush. `log.suppressed` is eligible only after loss, no more than once per 10 seconds, and itself may be lost. Quiet periods emit no heartbeat or recurring gauge. The logger retains only bounded process-local counters for this summary, no durable identity-linked traffic history.

## Relay Discovery and publication

`relay.discovery/success` means the handler authorized the request and wrote the current complete Discovery Document. It does not prove the client received or used it. `refused/invalid_credentials` includes a missing or rejected bearer credential; the current authorization result does not distinguish a blocked account from other invalid access, so the logger does not assert `account_blocked`. `refused/not_published` means the current address snapshot is absent or stale, `refused/draining` means the Relay Instance stopped admission, and `refused/safety_limit` means the global session ceiling was reached. Malformed routes or request shapes have no truthful `relay.discovery` catalog pair and remain quiet. Untyped backend errors return the existing unavailable HTTP response without inventing a database-specific log reason. `service.readiness` and `dependency.publication_api` describe publication availability separately from any one Discovery request.

These Discovery rows carry a request ID and measured handler duration, but no token, account, Peer ID, address, URL, header or response body. Their output shares the same bounded logger and may be suppressed under repeated discovery traffic.

## Service lifecycle rows

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

## Owner and administrator actions

Illustrative rows from separate requests and one background deletion worker:

```json
{"time":"2026-10-11T10:30:00Z","level":"INFO","msg":"portal logout","event":"portal.logout","outcome":"success","reason":"none","request_id":"0123456789abcdef0123456789abcdef","duration_ms":8}
{"time":"2026-10-11T10:31:00Z","level":"INFO","msg":"portal revoke","event":"portal.revoke","outcome":"refused","reason":"csrf_rejected","request_id":"11111111111111111111111111111111","duration_ms":3}
{"time":"2026-10-11T10:32:00Z","level":"WARN","msg":"portal revoke","event":"portal.revoke","outcome":"uncertain","reason":"commit_unconfirmed","request_id":"22222222222222222222222222222222","duration_ms":12}
{"time":"2026-10-11T10:32:30Z","level":"INFO","msg":"admin approve","event":"admin.approve","outcome":"refused","reason":"origin_rejected","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","duration_ms":2}
{"time":"2026-10-11T10:33:00Z","level":"INFO","msg":"admin approve","event":"admin.approve","outcome":"refused","reason":"revision_conflict","request_id":"33333333333333333333333333333333","duration_ms":6}
{"time":"2026-10-11T10:34:00Z","level":"INFO","msg":"admin plan_create","event":"admin.plan_create","outcome":"success","reason":"none","request_id":"44444444444444444444444444444444","duration_ms":16}
{"time":"2026-10-11T10:35:00Z","level":"INFO","msg":"portal delete","event":"portal.delete","outcome":"success","reason":"none","request_id":"55555555555555555555555555555555","duration_ms":20}
{"time":"2026-10-11T10:35:05Z","level":"INFO","msg":"maintenance deletion","event":"maintenance.deletion","outcome":"started","reason":"none"}
{"time":"2026-10-11T10:35:06Z","level":"WARN","msg":"maintenance deletion","event":"maintenance.deletion","outcome":"failed","reason":"journal_unavailable","duration_ms":1000}
{"time":"2026-10-11T10:35:07Z","level":"INFO","msg":"maintenance deletion","event":"maintenance.deletion","outcome":"success","reason":"none","duration_ms":34}
```

`portal.logout/success` means only that the Portal Session delete statement returned success; it does not revoke Login Grants or Relay Sessions. `portal.revoke/success` follows the Account-wide Revocation transaction and its local session effects. A failed transaction acknowledgement is `uncertain/commit_unconfirmed`, even if the database may have committed; conservative account fences and session closure still apply. The logger never turns a lost acknowledgement into success without a separate proof. Origin, CSRF, recent-auth, administrator allowlist and account revision checks remain the authority and have fixed refusal categories. `admin.plan_create/success` also covers an idempotent retry or a lost acknowledgement for which the stored operation and audit were independently proven. A changed replay is `refused/revision_conflict`.

`portal.delete/success` means the owner deletion request committed its admission and returned HTTP 202. It does **not** mean erasure has completed. The HTTP status endpoint reports pending as 202 and completed as 200; it produces no routine page activity row. `maintenance.deletion/started` identifies one due worker attempt. `failed/journal_unavailable` means that attempt could not prove a journal step, while `failed/database_unavailable` can identify a rejected SQL step before finalization. Both remain pending for durable retry. `uncertain/commit_unconfirmed` means an autocommit preparation acknowledgement or finalization could not be proven; it does not claim rollback. A later attempt may succeed. `maintenance.deletion/success` appears only after journal proof and final database commit or independent finalization proof. Worker rows have no request ID because they are not the owner's HTTP request. These rows are bounded diagnostics, not a durable audit history or proof of browser receipt. Administrator review has no pair in the fixed catalog and therefore emits no invented event. No row carries actor or target identity, email, plan name, form input, operation proof, deletion credential or journal key.

For local proof, provision the disposable PostgreSQL 18 TLS/SCRAM fixture described in [real database checks](../../README.md#local-real-database-check) and run `CLIPP_TEST_SERVING_CONFIG=<fixture-serving-config> go test -count=1 ./internal/auth -run 'Test(OwnerAndAdminMutationDiagnosticsFollowHTTPCommitProof|OwnerDeletionLogsAcceptanceAndProvenCompletion|AdministratorAccountDiagnosticsRespectRevisionAndCommit|AdministratorPlanDiagnosticsDistinguishProofFromUncertainty)$'`. `go test` without the serving config skips durable tests and is not equivalent evidence. Routine and race checks remain those in [relay verification](../verification.md). Local evidence does not establish deployed behavior.

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

## Dependency and maintenance observations

These rows are illustrative. A dependency failure describes a provider request or database operation; a later `recovered` row means a fresh operation succeeded. Neither proves a particular client received a response. Repeated failures and repeated successes in the same state stay quiet. Durations measure the individual operation. Raw SQL, upstream URLs and response bodies are excluded.

```json
{"time":"2026-10-11T10:30:00Z","level":"WARN","msg":"dependency database","event":"dependency.database","outcome":"failed","reason":"unavailable","duration_ms":503}
{"time":"2026-10-11T10:31:00Z","level":"INFO","msg":"dependency database","event":"dependency.database","outcome":"recovered","reason":"none","duration_ms":12}
{"time":"2026-10-11T10:32:00Z","level":"WARN","msg":"dependency deletion_journal","event":"dependency.deletion_journal","outcome":"failed","reason":"deadline","duration_ms":5000}
```

The same transition rule covers `dependency.google_token`, `dependency.google_jwks` and `dependency.publication_api`. A valid JWKS response that lacks the token's requested key is a credential verification refusal, not a JWKS dependency failure. A publication API failure may leave the previous verified address snapshot usable until its existing five-minute expiry. A journal failure does not relax the deletion proof or startup gate. A quota-clock diagnostic is separate from its existing stricter safety probe and recovery rule.

```json
{"time":"2026-10-11T10:33:00Z","level":"INFO","msg":"observation state","event":"observation.state","outcome":"observed","reason":"none","signal":"cleanup","state":"available"}
{"time":"2026-10-11T10:33:00Z","level":"INFO","msg":"observation threshold","event":"observation.threshold","outcome":"observed","reason":"warning","signal":"cleanup"}
{"time":"2026-10-11T10:34:00Z","level":"WARN","msg":"observation state","event":"observation.state","outcome":"failed","reason":"observation_failed","signal":"cleanup","state":"failed"}
{"time":"2026-10-11T10:37:00Z","level":"WARN","msg":"observation state","event":"observation.state","outcome":"failed","reason":"observation_stale","signal":"cleanup","state":"stale"}
{"time":"2026-10-11T10:38:00Z","level":"INFO","msg":"observation state","event":"observation.state","outcome":"recovered","reason":"none","signal":"cleanup","state":"available"}
{"time":"2026-10-11T10:38:00Z","level":"INFO","msg":"observation threshold","event":"observation.threshold","outcome":"recovered","reason":"none","signal":"cleanup"}
{"time":"2026-10-11T10:38:00Z","level":"INFO","msg":"observation state","event":"observation.state","outcome":"observed","reason":"producer_unavailable","signal":"full_backup","state":"unavailable"}
```

The diagnostic pass runs every 60 seconds with a five-second deadline and one pass in flight. A failed sample immediately invalidates its current value; an unrefreshed successful sample becomes stale after 180 seconds. No value or periodic gauge appears in these rows. Cleanup warning is older than one hour, critical at 12 hours and breach at 24 hours. Pending deletion warning is at 15 minutes and critical at one hour or 820 pending operations (80% of 1,024, rounded up). A threshold row tracks the highest crossed level; a fresh lower sample changes that level, and `recovered/none` clears it. Process CPU and memory have no connected diagnostic producer in this slice; each has an eligible initial `unavailable` state row without a numeric measurement. Recovery, full backup, repository retention, certificate and database infrastructure producers also remain unavailable and cannot satisfy a recoverability gate. The existing private `/metrics` endpoint and maintenance worker cadence retain their own behavior.

`maintenance.cleanup/failed/database_unavailable` identifies a failed cleanup pass or its accompanying breach observation when database work could not complete. `failed/deadline` identifies an expired pass or observation deadline. A `success/none` row appears only after a prior failed pass and a later complete pass in the same worker lifetime; ordinary successful passes stay quiet. This is per-worker evidence, not a durable proof that every expired row was deleted, nor a substitute for the separate `observation.threshold` cleanup backlog signal. Shutdown cancellation does not create a false database failure row. The worker emits no SQL, account or credential values.

Focused checks: `go test -count=1 ./internal/diagnostic ./internal/database ./internal/publication ./internal/auth -run 'Test(DependencyTransitionsEmitFailureAndProvenRecoveryOnly|MaintenanceObservationTransitionsAndFreshness|MaintenanceObservationHasOneInFlightPass|RuntimeWorkFailureAndRecoveryAreAggregateTransitions|ServiceChangesAndOutagePublishCompleteSnapshot|QualifiedJournalFailureAndRecoveryAreSafeTransitions|CleanupSignalsReportRetentionBreach|OwnerDeletionJournalReconciliationAndFreshPendingAccount)$'`. The last two require a disposable real PostgreSQL fixture; a skip is not a pass. Run the routine and race verification profiles separately.

## Audit, metrics and qualification limits

Structured stdout rows are best-effort operational diagnostics. They contain neither a complete history nor durable proof of an account, grant, quota or deletion mutation. Restricted transactional audit and the existing authoritative SQL or journal records retain those roles. The private `/metrics` endpoint remains a separate existing interface; these logging changes add no metric family, histogram, scrape snapshot, collector or alert route. The loss-summary counts are only logger bookkeeping and cannot be used as exact traffic totals.

Local HTTP, libp2p and disposable PostgreSQL fixtures can prove the documented server boundaries and safety properties at a recorded source revision. They cannot establish browser or device receipt, Direct Connection result, Clip delivery, identity-linked client recovery, deployed network behavior or production capacity. Backup and restore, certificate convergence, repository retention and database infrastructure observations without connected producers remain `unavailable`; a test fixture does not turn them into production measurements. Image publication, deployment, native and live qualification, and the accepted full observability release gates remain separate work.
