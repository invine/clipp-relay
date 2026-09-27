# 21: Produce verified recovery points

**What to build:** Produce off-cluster recovery points with provable WAL continuity and a bounded backup scheduler.

**Blocked by:** [19: Install bundled PostgreSQL](19-install-bundled-postgresql.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I10, I11.

## Contract and scope

Bundled pgBackRest supplies daily full backups plus continuous WAL and forced segment switch every 60s, targeting seven usable PITR days once established. Report only proven recoverable range from a valid base and uninterrupted archived WAL; scheduler success alone is insufficient. RPO target is 15m from proven off-cluster recoverability. External managed PITR may substitute only with equivalent verified recovery evidence and the separate application deletion journal.

The database main process invokes the local archive helper in its derived image. A distinct non-root backup UID in the same Pod uses the same pinned pgBackRest build, read-only PGDATA, bounded scratch/shared locks and an explicitly permissioned restricted backup-control Unix socket. This is the sole network-TLS exception. Preserve physical-data group readability across boots without root repair; prove no PGDATA writes, superuser login or bootstrap-secret access. No second-node live attachment, SSH/exec authority or shared PID.

Use operator-provided off-cluster OCI S3 repositories with verified HTTPS, dedicated Customer Secret Key archive/backup identity and Oracle-managed encryption. Writer cannot expire backups; expiry authority is separate. No mandatory extra cipher/KMS or locked retention on mutable metadata. Keep an encrypted operator recovery bundle outside the failed cluster with necessary pepper versions and current access/trust/config/image/schema references, never raw secrets in reports/Git. Do not provision buckets/IAM.

At most one active backup plus one coalesced pending request; manual backup/check shares that scheduler, no backlog of missed daily runs. pgBackRest process-max 1, buffer 1MiB, synchronous archive-push, db-timeout 30m, protocol-timeout 31m, io-timeout 60s, archive-timeout 120s. Whole backup 2h, retry 15m ±20%; local archive watchdog 2m, fail on uncertainty and terminate/reap own children before overlap. Stream directly to repository; no full-DB scratch stage. Stay within two backup SQL slots and the database Pod/container/scratch budgets.

Before first public serving require initialized verified journal plus a successful full backup and WAL-recovery check; before schema change require suitable pre-change recovery point/coverage and after restore a new verified baseline. Healthy restart needs no new full. Observe recoverability/storage every 60s: lag >5m warns, >15m or unprovable breaches RPO; full age >26h warns/>48h escalates; filesystem 80/90/95% warns/escalates/requires operator protective stop or earlier predicted exhaustion. Missing provider signal is unknown, not healthy.

## Acceptance criteria

- [ ] Create a full and continuous WAL chain in an isolated off-cluster repository, verify a chosen recovery point and report actual range/lag rather than schedule status.
- [ ] Prove same-Pod backup UID/socket permissions, read-only PGDATA, no superuser/bootstrap secret and permissions surviving restart without root repair.
- [ ] Run overlapping daily/manual/check requests and failures: one active/one coalesced pending, bounded retries/timeouts and no orphaned archive children or extra SQL clients.
- [ ] Verify backup/archive scratch and CPU/memory envelopes under operation; exhaustion fails visibly without WAL deletion, privileges, automatic growth, quota reset or auto-restore.
- [ ] Test HTTPS, repository identity and writer permission boundaries; writer cannot expire recovery material and serving cannot read bulk backups.
- [ ] Create and verify the encrypted off-cluster recovery-bundle procedure with required key versions/current authority references, without printing or committing secrets.
- [ ] Publish baseline/pre-change/post-restore checks and bounded freshness/disk signals; missing archive segment or external signal invalidates the advertised recoverable range.

## Demonstration

Take an isolated full, write a known marker, archive WAL and recover the marker to a separate target. Interrupt an archive attempt and show that claimed recoverability stops at the last proven continuous point.

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

