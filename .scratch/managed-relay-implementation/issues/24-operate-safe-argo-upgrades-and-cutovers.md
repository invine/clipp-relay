# 24: Operate safe Argo upgrades and cutovers

**What to build:** Give operators a safe manual Argo sequence for installation, schema upgrades, restore cutovers and database-mode changes.

**Blocked by:** [20: Rotate database credentials and certificates](20-rotate-database-credentials-and-certificates.md); [22: Restore accounts without resurrection](22-restore-accounts-without-resurrection.md); [23: Expire recovery material safely](23-expire-recovery-material-safely.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I9, I10, I11.

## Contract and scope

Implement explicit stopped → migrating → serving phases without hidden scaling. Stopped means zero relay Pods, preserved routing/database, verified old-process termination or real node fencing; only here may first-install initialization be enabled. Migrating keeps zero serving replicas and a read-only verifier confirms actual old Pod absence; API denial/unavailability fails closed. Bootstrap then migrate the intended target. Serving requires the selected image/database/schema/run and all applicable configuration/roles/recovery/monitoring prerequisites, with first-init disabled.

Each build requires an exact schema revision. Use ordered checksummed transaction-compatible migrations, atomically record revision with change and serialize runners on a dedicated connection. No startup migration, concurrent serving schema change or down migration. Verify suitable pre-change recovery point and journal coverage; post-restore baseline before cutover. Roll back only to a schema-compatible image, otherwise forward fix or deliberate recovery.

Use ordinary run-specific Jobs, never generic PreSync hooks. Names include explicit run ID and bounded target/config identity; changed inputs require a new run. Current success must match intended image/database/schema rather than stale Healthy/success. restartPolicy Never, no automatic failed retries, no TTL removal of failed evidence. Jobs cannot secretly scale serving. Bootstrap 10m, migration 30m, restore/reconciliation 3h; reviewed new run may change deadline but not silently increase resources. Bootstrap/migration SQL statement 5m, lock 5s, idle-in-transaction 60s, maintenance 32MB, no parallel maintenance, temp 256MB/backend, parent bound wins. Each worker CPU 100m/500m, memory 128/256MiB, ephemeral 256/512MiB, scratch 256MiB.

Argo example uses existing project/namespace, pinned Git revision/chart/values, manual full sync and PruneLast=true; no automatic self-heal, Force/Replace or selective maintenance sync. Diagnose/reconcile failed run before a new one, review old Jobs before pruning last. Preserve routing/bundled DB during phases. Uninstall/release renaming/database-mode changes first verify old shutdown and explicit target/data handoff; mode switch is transfer/restore, never an empty-DB toggle or merge. PVCs and operator-owned resources remain protected. Preflight is non-mutating and never prints Secrets. Tested operational sequence does not authorize actual production sync.

## Acceptance criteria

- [ ] Render/lint/schema-test both DB modes in all phases and verify singleton/zero-replica semantics, stable routing resources and initialization gates.
- [ ] Exercise fresh bootstrap/migration/serve, same-schema upgrade and schema-changing upgrade against real PostgreSQL, including runner contention/checksum mismatch/transaction rollback.
- [ ] Reject stale successful Jobs, mismatched run/image/schema/target, API-denied absence checks and uncertain node fencing; no forced-delete shortcut.
- [ ] Inject failed Job/cancellation/partial run, retain evidence and prove only diagnosed explicit new runs proceed within declared SQL/resource/deadline budgets.
- [ ] Require first/pre-change/post-restore recovery checks and current monitoring/configuration prerequisites at the relevant serve gate; ordinary healthy restart does not require a fresh full backup.
- [ ] Test schema-compatible rollback and reject incompatible rollback/down-migration; mode/release-name transition preserves source and requires validated data handoff.
- [ ] Supply tested manual full-sync/uninstall/cutover runbooks naming safe action, forbidden shortcuts and reopening verification, with retained PVC/Secret/operator-resource guarantees.

## Demonstration

Perform a complete isolated stopped/migrate/serve upgrade, deliberately fail a migration and demonstrate no serving restart from cached Healthy state. Recover with a reviewed new run, then inspect a safe uninstall plan preserving data.

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

