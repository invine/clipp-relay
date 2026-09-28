# 16: Delete and re-register safely

**What to build:** Let an account owner delete the account with independently committed deletion evidence, then re-register without escaping current-week consumption.

**Blocked by:** [09: Revoke access and change live quotas](09-revoke-access-and-change-live-quotas.md).

**Status:** resolved

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I5, I6, I10, I11.

## Contract and scope

Self-deletion is available in every account state after Google authentication within 10m; administrators cannot delete other accounts. Admit at most 1,024 durable pending deletions before irreversible work. Under identity/account guards, durably record a stable operation, revoke/fence access and competing mutations, and close sessions within 10s. Keep re-registration fenced until final detachment. Uncertain pending operations never expire or get evicted for capacity.

Use the operator-provided independent OCI journal with explicit repository identity and initial coverage floor. Never initialize an unexpectedly empty/wrong repository. The serving identity uses native OCI API signing with immutable event create/read and narrowly conditional head replacement only: no event overwrite/delete or backup access. Events contain old random account ID, stable event ID, sequence/hash/format and necessary time, never External Identity, email, identity digest, peer, credential or quota ledger. An uploaded event alone is not committed proof: verify conditional head membership and exact existing content on ambiguous retries.

One fair append/reconciliation worker uses 20s attempts, 5s object bounds, 1/2/4/8/16/30/60s half-to-full retry jitter, 4KiB encoded events, 16KiB heads/checkpoints and bounded streamed pages. Release DB locks before object I/O; compare journal maintenance generations. After proof, atomically detach raw identity/presentation, retain keyed current-week usage, invalidate credentials and append audit. Only then report completed deletion and begin the 24h hidden-record purge clock. Journal failure must not block unrelated accounts or account-wide revocation.

Re-registration creates a new random account in Pending, including after a Denied account deletes itself. Atomically import/delete matching current-week Retained Quota Usage exactly once; duplicate logical matches fail closed. Restore no old plan, overrides, state, credentials, email, older history or audit link. The new plan controls allowance. The dependent retention ticket completes physical purge; this slice must expose correct expiry/deletion state immediately.

## Acceptance criteria

- [x] Demonstrate owner-only recent-auth deletion from every state, including Denied; reject admin-on-behalf deletion and stale/replayed browser actions.
- [x] Verify stable durable admission, 1,024 cap before fencing, immediate invalidation and session closure within 10s; pending state honestly reports incomplete/retrying.
- [x] Inject event-upload/head-commit/DB-finalize crashes and ambiguous acknowledgements; retries reconcile exact content/sequence and cannot report success from upload alone.
- [x] Prove no DB lock is held across storage I/O; enforce worker/time/payload/page bounds and fair retries with no manual bypass.
- [x] Test wrong/empty journal, missing coverage, conflicting events and changed maintenance generations fail closed while unrelated traffic/revocation continues.
- [x] Prove finalization atomically detaches identity, emits permitted audit and begins the purge clock only once; current-week retained usage is keyed and not exposed.
- [x] Race deletion with login, refresh, account mutation, re-registration and UTC week rollover; fresh Pending registration imports only valid current-week usage once with no old-account link.
- [x] Expose bounded pending-count/age/outcome signals: warning at 15m, critical at 1h or 80% capacity; keep journal/account identifiers out of logs and metrics.

## Demonstration

Delete a consuming account while faulting the journal head acknowledgement, restart and reconcile. Re-register only after verified completion and show a fresh Pending account with the retained current-week consumption.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

- Claimed centrally on 2026-09-27 after ticket 09 resolution (`9861d10`); assigned to a fresh isolated Go implementation agent. External OCI journal qualification requires operator-provided repository identity and coverage floor; local implementation and test work can proceed while those inputs are unavailable. Resolution awaits integration review and acceptance evidence.

- Local implementation (`98eefaf`, journal-race follow-up `f6a03f3`) was merged centrally as `a1c8f53` after independent standards and specification reviews and final recheck. The orphan-upload race was reproduced red against real PostgreSQL, fixed, and rechecked: A can rebase after B commits the same sequence, while startup follows only the committed hash-linked chain. Go 1.27.1 on darwin/arm64: merged `go test -count=1 ./...`, `go test -race -count=1 ./internal/auth ./internal/relay ./internal/config ./internal/service`, `go vet ./...`, `go build ./...`, formatting and diff checks passed. Disposable Docker 29.8.0 smoke passed verified-TLS PostgreSQL 18.6 and 17.11 with signed local HTTPS journal fixture, migration 8, startup identity/coverage failures, auth/deletion/quota tests, and required PostgreSQL 16.15 rejection. The local fixture does not prove OCI Object Storage behavior or IAM scope. Actual provider qualification is **not run** until the operator supplies and approves an independent journal target, attested repository identity/coverage floor, and scoped OCI API-signing credentials. The ticket remains claimed; downstream ticket 17 is not unblocked. No push, publication, deployment, or external load occurred.

- On 2026-09-27 the operator confirmed that no OCI test journal is available now. Provider behavior and IAM qualification remain **not run**. Keep this ticket claimed and its dependents blocked while other eligible work proceeds.

- Resolved centrally on 2026-09-28 after the operator created and approved an independent, private, versioned OCI Object Storage **test** journal in `uk-london-1`. The attested new repository had floor 0 and the all-zero coverage hash; the serving key remained in a local protected file. An opt-in, temporary Go provider test using the real `OCIJournal` client passed native API signing, initial head read, empty/listed event pages, conditional event create and exact readback, duplicate-create rejection, stale and reused `If-Match` rejection, successful conditional head replacement and readback. OCI CLI 3.94.0, with a profile verified against the same serving user/tenancy/fingerprint, rejected unconditional event overwrite and event delete with `BucketNotFound`; it also rejected listing the existing backup bucket. Oracle documents that `BucketNotFound` may mean the caller is unauthorized; here the same identity successfully listed and read the test bucket and created the event, so the write/delete failures establish the intended scoped denials. The separate administrator removed each test probe. Final serving-identity checks showed only `journal/head.json`, matching repository identity/coverage, at sequence 0 with an empty event prefix. No identifier, key material or credential was added to the repository. The temporary provider test was removed after qualification. The previous reviewed local TDD/real-PostgreSQL and crash/race evidence above remains the full deletion/re-registration acceptance evidence. Go 1.27.1 darwin/arm64 on the integrated branch: `GOCACHE=/private/tmp/clipp-go-build-cache go test -count=1 ./...`, `GOCACHE=/private/tmp/clipp-go-build-cache go vet ./...` and `GOCACHE=/private/tmp/clipp-go-build-cache go build ./...` passed. The first test attempt was blocked by sandbox localhost bind restrictions; the rerun with localhost access passed. No push, publication, deployment or external load occurred. The test journal is not a production serving target.
