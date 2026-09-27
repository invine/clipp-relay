# Bundled backup execution facts

Research date: 2026-09-07. Bounded planning evidence for [the recovery frontier](../issues/15-define-postgresql-backup-restore-and-deletion-recovery.md). No images built, packages installed, commands tested against PostgreSQL, or cluster changes made. Proposed topology is feasible in principle, not a verified deployment recipe.

## Recommended candidate topology

**Engineering recommendation:** keep scheduling in a same-Pod sidecar, run backup commands as a distinct non-root backup OS user, give it read-only access to the database volume and narrowly scoped SQL access, and leave PostgreSQL's existing non-root PID-1/reload arrangement unchanged. This avoids adding scheduling and backup-child lifecycle management to the database supervisor. It adds shared-volume/permission configuration, so an in-container scheduler is mechanically simpler only if broad sharing of the database OS identity is acceptable. This is a tradeoff, not an accepted policy change.

The pgBackRest backup process reads physical database files while issuing backup-control queries; it does not require write access to the live PGDATA directory. Crunchy's first-party worked example demonstrates a separate OS user with group-read access and a restricted SQL role. It explicitly separates backup from restore: restore runs as the database owner. The example targets PostgreSQL 14, so its old SQL function names must not be copied for PostgreSQL 18. [Secure permissions example](https://www.crunchydata.com/blog/secure-permissions-for-pgbackrest).

The same Pod can share explicitly mounted volumes and network resources. No shared PID namespace or SSH/TLS pgBackRest server is needed merely for a process to read a shared PGDATA mount and connect to a shared Unix socket. This local-mode arrangement is an architectural inference from those capabilities, not an upstream-certified Helm topology. [Kubernetes Pods](https://kubernetes.io/docs/concepts/workloads/pods/), [pgBackRest local configuration](https://pgbackrest.org/configuration.html).

## SQL role: documented recipe, not yet a proven minimum

EDB's supported pgBackRest documentation gives this PostgreSQL 15-and-later recipe for a non-superuser role:

- Membership in `pg_read_all_settings`.
- `EXECUTE` on `pg_catalog.pg_backup_start(text, boolean)` and `pg_catalog.pg_backup_stop(boolean)`.
- `EXECUTE` on `pg_catalog.pg_switch_wal()` and `pg_catalog.pg_create_restore_point(text)`.
- It additionally recommends `pg_checkpoint` for `start-fast=y`.

Source: [EDB non-superuser support](https://www.enterprisedb.com/docs/supported-open-source/pgbackrest/10-non-superuser/). These are database privileges, not a requirement to distribute the bootstrap/superuser secret. A dedicated login still needs an allowed database connection and an explicit authentication rule. This lookup did not verify whether every listed grant, particularly `pg_checkpoint`, is necessary for the pinned pgBackRest version and selected commands; do not claim this is a proven minimal grant set.

PostgreSQL 18 documents grantable execution of its backup-control functions. `pg_backup_start(..., true)` requests a fast checkpoint; `pg_backup_stop` returns backup-label/tablespace-map content for the backup area, which must not be written into live PGDATA. The SQL function privileges do not grant permission to overwrite arbitrary database tables. [PostgreSQL 18 backup-control functions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-BACKUP).

**Security boundary:** read access to physical database files still exposes the entire database contents, irrespective of missing SQL `SELECT` privileges. The sidecar is a highly trusted backup component, not a tenant-isolated reader.

## OS identity and authentication matter

- The reviewed official Debian image defines `postgres` as UID/GID 999. This is an image fact to pin/verify, not a universal PostgreSQL UID requirement. [Official PostgreSQL 18/Trixie Dockerfile](https://github.com/docker-library/postgres/blob/master/18/trixie/Dockerfile).
- A sidecar using the same `postgres` OS identity is easy to make file-compatible, but a local `peer` rule can also authenticate it as the SQL `postgres` user; a permissive `trust` rule is broader still. Setting `pg1-user=backupuser` changes the requested login, not the sidecar's potential authority. Do not claim “no superuser access” solely because its configuration names a restricted role. [Peer authentication](https://www.postgresql.org/docs/18/auth-peer.html), [initdb authentication warning](https://www.postgresql.org/docs/18/app-initdb.html).
- PostgreSQL supports `initdb --allow-group-access` specifically for backups by another non-privileged OS user. A distinct backup UID in the database file-reading group plus a read-only volume mount is a practical candidate. However, the official entrypoint attempts `chmod 00700 "$PGDATA"` during startup, which can defeat group traversal after a restart; initialization alone is insufficient. The derived startup path must preserve intended group permissions without root or recurring broad recursive permission rewrites. [PostgreSQL initdb](https://www.postgresql.org/docs/18/app-initdb.html), [official entrypoint source](https://github.com/docker-library/postgres/blob/master/docker-entrypoint.sh).
- A shared private Unix-socket directory is supported. Socket group/mode controls who can connect, while PostgreSQL authentication separately controls the SQL role. Use explicit local authentication rather than relying on image defaults; mounts, directory traversal, group mapping and first-start ordering require testing. [Socket settings](https://www.postgresql.org/docs/18/runtime-config-connection.html#GUC-UNIX-SOCKET-PERMISSIONS).

## Paths and runtime dependencies

- Backup and PostgreSQL-triggered archive commands need access to a common writable pgBackRest lock directory; the default is `/tmp/pgbackrest`. `spool-path` is writable transient state for asynchronous archive commands, not a substitute for durable WAL storage. A backup-only process does not inherently need to own that spool. File logging can be disabled; current upstream configuration calls the value `off`, removing the log-directory requirement. Check the pinned release's accepted option spelling. [Configuration reference](https://pgbackrest.org/configuration.html), [separate-user lock/spool example](https://www.crunchydata.com/blog/secure-permissions-for-pgbackrest).
- Mount PGDATA read-only in the backup sidecar at the configured path, including every referenced tablespace/WAL location if any; keep writable lock/state mounts separate. Mount configuration and required repository/encryption secrets read-only. Actual scheduler scratch needs depend on its implementation. These are proposed mount boundaries, not a tested exhaustive mount list.
- PostgreSQL's `archive_command` continues invoking pgBackRest in the database container as the database OS user. The database image therefore still needs the executable, runtime libraries, configuration, repository access and writable archiver state. A scheduler sidecar does not remove those requirements. Use the same pinned build in both containers; upstream requires exact matching versions for remote protocols. [pgBackRest installation/archiving guide](https://pgbackrest.org/user-guide.html).

## Verification gate before implementation is accepted

Prove first boot and restart, group-read file creation, denied PGDATA writes, denied superuser login, restricted-role `check`/full backup, mutual locking, repository expiry, successful PITR restore using a separate writable restore process, and clean shutdown/cancellation under the chosen scheduler. Also verify that scheduler or backup failure does not terminate PostgreSQL or silently disable WAL archiving. No superuser/bootstrap secret needs to be mounted in the live scheduler; granting its role remains a privileged provisioning operation.
