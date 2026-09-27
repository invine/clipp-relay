# Operational maintenance bounds: source facts and candidates

Research date: 2026-09-07. Scope: ticket 16 after Q324–Q331, read alongside the
deployment and recovery contracts. Every numeric candidate below is **unapproved,
not benchmarked**, and stays inside the accepted PostgreSQL Pod CPU/memory split.
This note changes no backup retention, recovery, TLS, or WAL-loss policy.

## Backup execution and connection accounting

- In pgBackRest 2.59.1, `dbGet()` constructs and opens one SQL client per configured
  PostgreSQL cluster. With exactly one local primary configured, the backup owns
  one primary SQL control connection. [Database helper source](https://github.com/pgbackrest/pgbackrest/blob/release/2.59.1/src/db/helper.c)
- Backup file work uses a separate parallel-protocol executor; `process-max=1`
  does not mean two SQL connections, nor does it bound the entire process tree to
  one OS process. [Backup command source](https://github.com/pgbackrest/pgbackrest/blob/release/2.59.1/src/command/backup/backup.c)
- The normal local `archive-push` path reads files and repository metadata, without
  opening a SQL control client. This is a source-inspection inference for this
  topology, not every possible wrapper. [Archive-push source](https://github.com/pgbackrest/pgbackrest/blob/release/2.59.1/src/command/archive/push/push.c)
- Therefore the proposed two-slot backup allowance is conservative headroom for
  command overlap, not a mandatory two-client requirement. Serialize scheduler
  commands, bound health/check commands too, and verify actual sessions in the
  eventual pinned-image integration test. Local archiver work still consumes
  PostgreSQL-container CPU/memory and overlaps the backup sidecar.

## Deadline semantics and proposed work profile

pgBackRest's database timeout covers individual queries, including backup start
and stop; protocol timeout must exceed it. I/O timeout measures absence of
progress, not total transfer duration. Archive timeout bounds backup/check waits
for required WAL, not PostgreSQL's forced segment-switch interval. Buffers and
compression add memory beyond a nominal buffer size. Async spool data is
transient; the archive queue-drop option can acknowledge and discard WAL.
[Configuration reference](https://pgbackrest.org/configuration.html)

Candidate: one active full backup, one coalesced pending request, no concurrent
manual/check backup-control command; `process-max=1`, `buffer-size=1MiB`, synchronous
archive-push, `db-timeout=30m`, `protocol-timeout=31m`, `io-timeout=60s`, and
`archive-timeout=120s`. A scheduler-owned two-hour whole-backup deadline is
additional to those tool timeouts. After failure, retry after 15 minutes with
bounded jitter; never accumulate missed daily runs. These are tuning candidates,
not a claim that the 225m-CPU/192-MiB sidecar can back up 10 GiB in two hours.
Keep automatic expiry disabled under separate cleanup authority. Never enable
queue-driven WAL dropping. Do not inherit runtime's two-second SQL timeout into
backup roles. Cancellation must terminate/reap only that command's descendants,
preserve repository resumability/locks, and remain visibly failed until validated.

Archive failure must report failure: PostgreSQL retries unsuccessful archiving;
reporting success permits WAL recycling. An outage can consequently fill the
data volume; scratch limits do not bound this backlog. A two-minute local archive
attempt watchdog is a possible separate engineering bound, but needs tested exit
and child-cleanup behavior before selection. [PostgreSQL archiving](https://www.postgresql.org/docs/18/continuous-archiving.html)

## Database probes

`pg_isready` is server-acceptance status, not authenticated SQL/schema validation;
incorrect connection parameters can cause failed-login logs. Its own timeout is
configurable. [PostgreSQL pg_isready](https://www.postgresql.org/docs/18/app-pg-isready.html)
The official entrypoint temporarily starts a socket-only setup server, so an
unguarded socket probe can report ready before final startup. [Entrypoint source](https://github.com/docker-library/postgres/blob/master/docker-entrypoint.sh)

Candidate: readiness every five seconds, internal check timeout two seconds,
Kubernetes timeout three seconds, two failures/one success; initial startup allows
30 minutes at the same cadence. Omit a database I/O-dependent liveness probe:
PostgreSQL process exit already triggers container restart, while repeatedly
restarting slow recovery is harmful. Offline restore needs its own sufficiently
long startup budget. Kubernetes readiness removes Service endpoints; startup
failure eventually restarts the container. [Probe semantics](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)

Unresolved mechanics: target the final server without routing through a Service
that requires readiness first; preserve verified TLS for network checks. A new
local-socket health check must not silently expand the sole accepted backup-socket
TLS exception. Do not require watcher/bootstrap/backup success for initial database
readiness. Authentication and first-serving checks remain separate. Validate that
the selected probe cannot spam authentication errors or exceed its one-slot budget.

## Certificate watcher

PostgreSQL keeps its previous SSL configuration when a reload encounters invalid
files; a successful reload request is not proof of the served leaf. [PostgreSQL SSL](https://www.postgresql.org/docs/18/ssl-tcp.html)
Projected Secret updates are eventual; `subPath` mounts do not refresh. Polling
frequency is not an end-to-end delivery SLA. [Kubernetes Secrets](https://kubernetes.io/docs/concepts/configuration/secret/)

Candidate: poll complete published generations every 30 seconds, one operation
at a time, ten-second overall reload-and-fresh-handshake deadline. Retry after
5/15/30/60 seconds, then at most once per minute with jitter. Check the served
leaf afresh every five minutes even without a detected change. Warn after five
minutes of failed convergence and for seven days remaining validity; escalate
at 24 hours remaining or immediately upon expiry. Short-lived certificate
deployments must retune warning thresholds. Wait quietly for initial role
provisioning; serving validation still requires watcher success. Never downgrade
TLS on retry or make renewal failure a PostgreSQL restart loop.

## Scratch and remaining verification

Candidate disk-backed `emptyDir` size limits: backup scratch 256 MiB, local archive
helper scratch 64 MiB, shared lock directory 8 MiB, watcher scratch 8 MiB. Do not
stage a complete backup there; these sizes need file-count/manifest and interrupted
transfer tests. Keep logs out of these paths under the accepted log policy. An
`emptyDir` is ephemeral, shares node storage, and can run out before its size limit;
memory-backed volumes consume memory budgets. Explicit per-container ephemeral
storage requests/limits must also account for logs and writable layers. These
scratch candidates are not a node reservation or synchronous disk-write quota.
[Kubernetes volumes](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir)

Outstanding verification: pinned image permissions and cross-UID lock behavior;
whole-process-tree cancellation; backup peak RSS under the accepted split;
probe final-server/TLS/logging behavior; interrupted repository operations;
restore-specific duration/storage allowance; WAL pressure observability. No
benchmark, installation, cluster operation, or production implementation occurred.
