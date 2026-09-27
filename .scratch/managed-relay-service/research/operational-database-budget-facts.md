# Operational database budget facts

Research date: 2026-09-07. Bounded support for [operational Safety Limits](../issues/16-define-remaining-operational-limit-defaults.md). Inspected the current [persistence](../persistence-contract.md), [deployment](../deployment-contract.md) and [recovery](../recovery-contract.md) contracts. All numbers below are **proposed starting values**, not accepted decisions, benchmarks or evidence that the 5,000-session target fits. No installation or cluster work performed. pgx references were served as v5.10.0; implementation must pin and verify its selected v5 release.

## Contract boundaries

The existing contract requires short Read Committed transactions, ordered account locks, durable commit before local credit installation, and operation-specific ambiguity recovery. Provider/storage calls stay outside database transactions. Runtime, migration, bootstrap, backup and certificate-reload authorities remain distinct. PostgreSQL stays available during relay maintenance; the backup and watcher must wait for role provisioning without blocking database initialization. Neither new pools nor sidecars implicitly increase the accepted budget. [Persistence ordering](../persistence-contract.md#ordering-and-transaction-units), [deployment envelope](../deployment-contract.md#database-persistence-and-resource-envelope), [recovery workloads](../recovery-contract.md#workloads-and-authority).

## Small runtime pool and deadline candidate

| Setting | Proposed initial value / interpretation |
| --- | --- |
| Relay runtime pool | `MaxConns=8`, `MinConns=0`; one bounded pool, not one per account or operation. |
| Idle/lifetime management | Idle 5 minutes; lifetime 30 minutes plus up to 5 minutes jitter; health-check period 60 seconds. |
| Pool acquisition | 500 ms maximum wait, shortened by the parent deadline. This limits waiting, not how many callers can wait. |
| New database connection | 3-second connect budget; startup health/schema validation remains an explicit bounded operation. |
| Ordinary database unit | 3 seconds total including acquisition, statements and commit; always capped by the enclosing operation deadline. |
| Ordinary runtime SQL safeguards | `statement_timeout=2s`, `lock_timeout=250ms`, `idle_in_transaction_session_timeout=5s`; apply to the runtime role/session, not globally. |
| Background database work | At most one cleanup batch in flight; it shares the runtime pool unless a separately budgeted maintenance identity/pool is selected. |
| Maintenance/provisioning clients | One connection per executing Job, not the runtime pool; watcher at most one active database operation. Backup connection allowance must be verified with the pinned pgBackRest commands. |

The API supports explicit maximum/minimum connections, context-bearing acquisition, lifetime jitter and idle management. Defaults should not be inferred from CPU count. Pool creation does not establish connectivity; explicit acquire/ping is required. `Begin`/`BeginTx` cancellation does **not** automatically roll back a transaction: code must commit or roll back and release it. `Close` waits for borrowed connections, so shutdown needs bounded transaction cleanup. These facts support the profile's structure, not its numerical sufficiency. [pgxpool Config and lifecycle](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.10.0/pgxpool), [connection configuration](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.10.0/pgconn#Config).

PostgreSQL's statement timeout covers a statement, not acquisition or a whole multi-statement application operation. Lock timeout applies separately to each lock acquisition. Idle-in-transaction timeout terminates stalled transactions; it does not replace a client operation deadline. Keep lock timeout shorter than statement timeout. PostgreSQL discourages global timeout settings that inadvertently affect every session. [PostgreSQL 18 timeout semantics](https://www.postgresql.org/docs/18/runtime-config-client.html).

**Conflicts to avoid:** a 2-second global statement timeout would also affect pgBackRest's checkpoint/backup-control calls and migrations. Give those roles independent bounded profiles under their Job/backup deadlines. Do not apply a runtime `transaction_timeout` globally; on PostgreSQL 18 it terminates sessions, and a shorter value supersedes longer statement/idle timeouts. Do not let retries reset the three-second operation budget, blindly replay an ambiguous refresh/commit, or release a connection with an unfinished transaction. A deadline failure is not proof of rollback. [Timeout ordering](https://www.postgresql.org/docs/18/runtime-config-client.html), [accepted ambiguity rules](../persistence-contract.md#ordering-and-transaction-units).

## Initial bundled PostgreSQL configuration candidate

Start with `max_connections=32`, retaining three superuser-reserved slots and zero additional reserved slots. Account for runtime eight, watcher one, a provisional backup allowance of two, ordinary probes/cleanup and maintenance connections before calling the remaining slots headroom. Bootstrap/migration are offline phases, but backup/watcher may coexist with them. Do not grant reserved-connection privileges merely to make an undersized pool pass. PostgreSQL allocates some resources according to `max_connections`, and reserved slots are included within that maximum. External PostgreSQL needs an equivalent connection allowance without taking over the operator's global settings. [PostgreSQL connection capacity](https://www.postgresql.org/docs/18/runtime-config-connection.html#GUC-MAX-CONNECTIONS).

Proposed complete client allowance: eight runtime connections **including** ordinary in-process cleanup, one separate scheduled cleanup-Job connection, one watcher connection, two budgeted backup connections, one active database probe, one active bootstrap-or-migration client, and three operator/emergency connections: **17 maximum planned client slots**, leaving 15 of 32 unassigned. Enforce serialization for each one-client category; this is not a claim that a role name itself limits concurrency. No foreground-priority pool is introduced. If the pinned backup needs more than its provisional two-slot allowance, revise the ledger explicitly. Background workers and archive helper processes are not equivalent to ordinary SQL client slots.

Candidate small-memory settings: `shared_buffers=128MB`, `work_mem=2MB`, `hash_mem_multiplier=2`, `maintenance_work_mem=32MB`, `autovacuum_work_mem=16MB`, `max_parallel_workers_per_gather=0`, `max_parallel_maintenance_workers=0`, and runtime-role `temp_file_limit=64MB`. These are bounded starting knobs, not a complete memory/disk bound. A query can use multiple work-memory allocations; hash operations multiply that amount and concurrent sessions multiply it again. Temporary-file limits are per backend, not whole-cluster caps. Lower settings trade memory pressure for I/O and longer queries. [PostgreSQL resource settings](https://www.postgresql.org/docs/18/runtime-config-resource.html).

Keep autovacuum enabled, initially with `autovacuum_max_workers=2`; this limits parallel background work but can increase cleanup lag and bloat. Tune from vacuum progress/dead tuples and workload rather than turning it off to fit memory. PostgreSQL 18 also has autovacuum worker-slot settings; reconcile those with the selected image instead of assuming the worker count represents every background process. [PostgreSQL vacuum settings](https://www.postgresql.org/docs/18/runtime-config-vacuum.html).

Do not relax durability, TLS, logging privacy, or synchronous commit to compensate for these small budgets. Database-side migration memory and CPU are charged to PostgreSQL even when the migration client has a separately bounded Kubernetes Job. [Accepted deployment constraints](../deployment-contract.md#database-persistence-and-resource-envelope).

## Exact per-container split within the accepted aggregate

| Database Pod container | CPU request | CPU limit | Memory request | Memory limit |
| --- | --- | --- | --- | --- |
| PostgreSQL including local archive helper | 400m | 750m | 384 MiB | 768 MiB |
| Backup scheduler and its pgBackRest children | 90m | 225m | 96 MiB | 192 MiB |
| Certificate reload watcher | 10m | 25m | 32 MiB | 64 MiB |
| **Sum** | **500m** | **1,000m** | **512 MiB** | **1,024 MiB** |

This is a testable allocation proposal, not a new allowance. CPU limits can throttle a container; an idle sidecar does not let PostgreSQL exceed its own 750m CPU or 768 MiB memory limit. Memory limits can cause OOM termination. Keep volume-backed scratch limits and usage visible too; memory-backed volumes consume memory. The aggregate arithmetic alone does not guarantee scheduling success or absence of resource pressure. [Kubernetes resource accounting/enforcement](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/).

Start pgBackRest with one concurrent scheduled backup, `process-max=1` and `buffer-size=1MiB`, including the archiver's own one-worker profile. The worker count governs compression/transfer parallelism per command, not all processes across the Pod: a scheduled backup and the PostgreSQL-triggered archiver can run concurrently. Buffer size does not bound total memory, which also includes metadata, compression/encryption and storage-client allocations. Automatic expiry must remain disabled in the backup identity because cleanup authority is separate. [pgBackRest configuration](https://pgbackrest.org/configuration.html), [accepted recovery authority](../recovery-contract.md#workloads-and-authority).

**Hard caution:** pgBackRest's `archive-push-queue-max` is not a harmless disk cap: reaching it can acknowledge and discard WAL, breaking the PITR chain. Do not select a finite value as a generic “bounded queue” fix for the 10 GiB PVC. Storage-pressure handling, backup/archive deadlines and failure alerts need their own accepted operational policy. [pgBackRest archive queue behavior](https://pgbackrest.org/configuration.html#section-archive/option-archive-push-queue-max).

## Follow-on validation / tunables

Measure pool wait/cancellation, transaction/lock latency, connection churn, backup/archive duration and lag, PG and sidecar peak RSS, CPU throttling, temp-file growth and vacuum backlog. Test simultaneous backup plus quota/auth load, database outage recovery, cancellation/ambiguous commits, migration and certificate reload under load. The 25m/64 MiB watcher and 225m/192 MiB backup limits are deliberately small and may prove inadequate; a failed test requires explicit retuning/scope tradeoff, not silent expansion or a capacity claim.
