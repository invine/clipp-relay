# 19: Install bundled PostgreSQL

**What to build:** Offer chart-managed PostgreSQL as an explicit alternative to external PostgreSQL, with safe first initialization and retained persistent data.

**Blocked by:** [18: Install with external PostgreSQL](18-install-with-external-postgresql.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I9, I11.

## Contract and scope

Bundle a separate single-instance PostgreSQL 18.x StatefulSet that remains running during relay maintenance. Build an independently pinned Debian image derived from the official PostgreSQL base with pgBackRest/libraries installed at build time, preserving the accepted upstream PostgreSQL-18 volume/data-directory layout. DB major and application schema are different; relay upgrades never silently upgrade the database. No HA/operator/subchart requirement.

Choose either a new configurable 10GiB claim or an existing claim, never both. Production storage uses operator-managed expandable Longhorn with Retain; protect created PVCs from Argo prune/application deletion and never adopt an existing claim. Operator protects direct PVC/namespace deletion. Never shrink/replace/delete, force-attach, root-repair or fall back to empty storage. Check expected data/major markers before upstream entrypoint. Initialization is explicit first-install permission only while relay is stopped, cleared before first serving; missing/empty/wrong data after use fails closed.

Explicit resumable bootstrap creates/verifies database and serving/migration/reload/backup roles, rejecting conflicting roles or passwords rather than resetting them. Bootstrap credentials never mount into serving/watcher/backup. Serving has no DDL. Remove inherited PUBLIC/default privileges that would undermine the role restrictions. Watcher and backup wait for provisioning without blocking initial database readiness.

All network clients use verify-full TLS/SCRAM, rejecting plaintext; certificate matches internal Service hostname/cluster domain. Only the permissioned same-Pod backup Unix socket is excepted. Probe the final server through one loopback TCP connection with correct hostname/trust, not an init socket: inner 2s, Kubernetes timeout 3s, period 5s, readiness 2 failures/1 success, startup 360 failures. No I/O-dependent DB liveness; fresh authenticated role/schema checks remain separate.

Connection ceiling 32 including three superuser-reserved slots; planned use runtime 8, repository/journal maintenance 1, watcher 1, backup ≤2, probe 1, bootstrap-or-migration 1, operator/emergency 3 =17, leaving 15 unassigned. Serialize single-client categories. PG settings: shared_buffers 128MB, work_mem 2MB, hash multiplier 2, maintenance 32MB, autovacuum 16MB each/≤2 workers, no parallel query/maintenance, runtime temp 64MB/backend; retain vacuum, durability and synchronous commit.

CPU request/limit, memory and ephemeral budgets respectively: PostgreSQL+archive 400m/750m, 384/768MiB, 128/256MiB; backup scheduler 90m/225m, 96/192MiB, 256/512MiB; watcher 10m/25m, 32/64MiB, 16/32MiB. Total DB Pod 500m/1 CPU, 512MiB/1GiB, 400/800MiB ephemeral; scratch backup 256MiB, archive 64MiB, locks 8MiB, watcher 8MiB. Sibling slack does not increase caps. Dependent tickets implement watcher/scheduler behavior within these budgets.

## Acceptance criteria

- [ ] Render both DB modes and reject conflicting storage/mode values, missing trust and unsafe first-init settings; external mode creates no bundled DB.
- [ ] From an authorized empty claim, bootstrap roles, migrate and serve with initialization disabled; prove bootstrap rerun is resumable and never overwrites conflicting credentials.
- [ ] Restart with valid data and separately test empty/missing/wrong-major expected data: fail closed rather than reinitialize; preserve upstream volume layout.
- [ ] Prove role/PUBLIC privilege restrictions and TLS/SCRAM rejection cases through actual SQL connections; probe succeeds before reload/backup roles are provisioned without bypassing final pre-serving validation.
- [ ] Verify retained PVC behavior in rendered prune/uninstall plans and isolated lifecycle tests, with existing claims never adopted and no automatic destructive storage operation.
- [ ] Assert the complete SQL slot ledger, PG settings, per-container resources and bounded scratch; demonstrate no unbounded helper or parallel worker outside that envelope.
- [ ] Keep DB available across relay maintenance and relay image changes; document explicit DB minor verification/major-upgrade boundaries and operator storage prerequisites.

## Demonstration

Install into an isolated namespace using a new retained claim, restart normally, enter relay maintenance and inspect database continuity. Then point a test instance at invalid expected data and demonstrate refusal rather than first-boot initialization.

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

