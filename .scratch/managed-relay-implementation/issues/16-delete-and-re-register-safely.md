# 16: Delete and re-register safely

**What to build:** Let an account owner delete the account with independently committed deletion evidence, then re-register without escaping current-week consumption.

**Blocked by:** [09: Revoke access and change live quotas](09-revoke-access-and-change-live-quotas.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I5, I6, I10, I11.

## Contract and scope

Self-deletion is available in every account state after Google authentication within 10m; administrators cannot delete other accounts. Admit at most 1,024 durable pending deletions before irreversible work. Under identity/account guards, durably record a stable operation, revoke/fence access and competing mutations, and close sessions within 10s. Keep re-registration fenced until final detachment. Uncertain pending operations never expire or get evicted for capacity.

Use the operator-provided independent OCI journal with explicit repository identity and initial coverage floor. Never initialize an unexpectedly empty/wrong repository. The serving identity uses native OCI API signing with immutable event create/read and narrowly conditional head replacement only: no event overwrite/delete or backup access. Events contain old random account ID, stable event ID, sequence/hash/format and necessary time, never External Identity, email, identity digest, peer, credential or quota ledger. An uploaded event alone is not committed proof: verify conditional head membership and exact existing content on ambiguous retries.

One fair append/reconciliation worker uses 20s attempts, 5s object bounds, 1/2/4/8/16/30/60s half-to-full retry jitter, 4KiB encoded events, 16KiB heads/checkpoints and bounded streamed pages. Release DB locks before object I/O; compare journal maintenance generations. After proof, atomically detach raw identity/presentation, retain keyed current-week usage, invalidate credentials and append audit. Only then report completed deletion and begin the 24h hidden-record purge clock. Journal failure must not block unrelated accounts or account-wide revocation.

Re-registration creates a new random account in Pending, including after a Denied account deletes itself. Atomically import/delete matching current-week Retained Quota Usage exactly once; duplicate logical matches fail closed. Restore no old plan, overrides, state, credentials, email, older history or audit link. The new plan controls allowance. The dependent retention ticket completes physical purge; this slice must expose correct expiry/deletion state immediately.

## Acceptance criteria

- [ ] Demonstrate owner-only recent-auth deletion from every state, including Denied; reject admin-on-behalf deletion and stale/replayed browser actions.
- [ ] Verify stable durable admission, 1,024 cap before fencing, immediate invalidation and session closure within 10s; pending state honestly reports incomplete/retrying.
- [ ] Inject event-upload/head-commit/DB-finalize crashes and ambiguous acknowledgements; retries reconcile exact content/sequence and cannot report success from upload alone.
- [ ] Prove no DB lock is held across storage I/O; enforce worker/time/payload/page bounds and fair retries with no manual bypass.
- [ ] Test wrong/empty journal, missing coverage, conflicting events and changed maintenance generations fail closed while unrelated traffic/revocation continues.
- [ ] Prove finalization atomically detaches identity, emits permitted audit and begins the purge clock only once; current-week retained usage is keyed and not exposed.
- [ ] Race deletion with login, refresh, account mutation, re-registration and UTC week rollover; fresh Pending registration imports only valid current-week usage once with no old-account link.
- [ ] Expose bounded pending-count/age/outcome signals: warning at 15m, critical at 1h or 80% capacity; keep journal/account identifiers out of logs and metrics.

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
