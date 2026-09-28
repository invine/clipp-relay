# 17: Enforce retention and pepper rotation

**What to build:** Automatically remove expired online data within its retention deadline and rotate identity-digest peppers without reopening quota loopholes.

**Blocked by:** [16: Delete and re-register safely](16-delete-and-re-register-safely.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I5, I6, I11.

## Contract and scope

Use one indexed, fair cleanup worker at startup and every minute, at most 30s per pass and 250 rows per transaction inside the normal 8-connection/64-work runtime DB envelope and ordinary deadlines. Delete bounded children before hidden parents; no unbounded cascade or overlapping worker. Expiry is enforced at authorization/read time, never delayed until physical cleanup.

Purge ended credentials and redeemed/expired flow/session/grant records within 24h; preserve consumed refresh hashes while their grant is active. After completed self-deletion detach raw identity immediately and purge hidden account/dependencies within 24h. Retained Quota Usage lasts only the current UTC week; account quota history retains current plus 12 completed weeks. Audit retains 180d, is append-only for application writes and administrator-only for reads. Deleted random account IDs may remain in audit but never link identity or a new account.

Expire abandoned Pending accounts after 90d without successful Google portal login, with pending_expired audit but no retained identity/deletion journal record. Active, Suspended and Denied do not expire for inactivity. No automatic retention-clock restart after outages.

The identity HMAC keyring has explicit versions/current and retiring keys, not plaintext identity or trial of arbitrary keys. Keep necessary keys until dependent records expire and the 24h cleanup window passes. Reject premature removal; missing protection needed for retained current-week usage blocks new registration rather than treating usage as zero, until restored or no longer applicable. Rotation must preserve logical uniqueness and exact-once usage import.

## Acceptance criteria

- [ ] Use real PostgreSQL and controlled time to verify each retention boundary, bounded child-first deletion and restart catch-up without one account monopolizing cleanup.
- [ ] Prove expired credentials/data are unusable or invisible before physical deletion and purge lag grants no authorization grace.
- [ ] Check successful Google portal login, not generic traffic/failed login, refreshes Pending activity; expiration produces only allowed audit and never retained identity/journal records.
- [ ] Verify current-plus-12-weeks history, week-end retained-usage expiry and 180d audit cleanup without deleting live grants' consumed refresh hashes.
- [ ] Exercise pepper overlap/rotation, missing versions, premature retirement, duplicate logical records and concurrent re-registration; no zero-usage fallback or identity plaintext appears.
- [ ] Test deleted-account purge removes identity/presentation/dependencies and new-account mappings while permitted old random audit targets remain detached.
- [ ] Publish cleanup age/outcome and warn over 1h, escalate at 12h, explicitly record required 24h purge breaches; failures never reset retention clocks.

## Demonstration

Advance a controlled clock through Pending expiry, week rollover and completed-deletion purge, then rotate a pepper with retained usage still present. Show both safe overlap and rejection of premature key removal.

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

- Claimed centrally on 2026-09-28 after ticket 16 resolution (`1a4284d`). Assigned to a fresh isolated Go implementation agent for `/implement` and TDD. Resolution awaits integration, independent specification and standards reviews, and real-PostgreSQL acceptance evidence. The capacity ticket remains deferred.
