# PostgreSQL recovery foundations

Research date: 2026-09-06. Primary-source facts for [Define PostgreSQL backup, restore, and deletion recovery](../issues/15-define-postgresql-backup-restore-and-deletion-recovery.md).
This note does not select a backup product, implement recovery, or settle the proposed service objectives.

## Physical recovery versus logical exports

- Point-in-time recovery (PITR) combines a physical base backup with an uninterrupted WAL sequence beginning no later than the start of that backup and continuing through the recovery target. A gap can make later targets unrecoverable; a collection of recent WAL files without its required base backup is insufficient. Physical recovery restores the database cluster, not selected application rows. [PostgreSQL continuous archiving](https://www.postgresql.org/docs/current/continuous-archiving.html)
- A retention window must preserve a usable base backup and every required WAL segment for its oldest advertised recovery target. **Inference:** deleting every object older than exactly 30 days independently can destroy a nominal 30-day recovery window; backup-chain-aware expiration is required if that window is selected. [PostgreSQL continuous archiving](https://www.postgresql.org/docs/current/continuous-archiving.html)
- `pg_dump` produces a consistent logical export while readers and writers continue. It covers one database; roles and other cluster-global objects require separate treatment. Logical exports are useful for portability, transfer, or discrete restore points. [pg_dump](https://www.postgresql.org/docs/current/app-pgdump.html)
- SQL dumps cannot be used as base backups for WAL replay. Therefore scheduled dumps alone do not provide arbitrary point-in-time recovery between exports. [PostgreSQL continuous archiving](https://www.postgresql.org/docs/current/continuous-archiving.html)

## Recovery objectives need operational evidence

- WAL archiving ordinarily handles completed segments. `archive_timeout` forces periodic segment switches when there is database activity, addressing low-traffic delays. Forced switches still produce full-length segment files, creating a storage tradeoff. [WAL configuration](https://www.postgresql.org/docs/current/runtime-config-wal.html#RUNTIME-CONFIG-WAL-ARCHIVING)
- **Inference:** setting `archive_timeout=15min` does not prove a 15-minute recovery point objective (RPO): upload latency, archive backlog, outages, and durable destination confirmation also count. Measure the age of the latest off-cluster recoverable point and leave operational margin. [WAL configuration](https://www.postgresql.org/docs/current/runtime-config-wal.html#RUNTIME-CONFIG-WAL-ARCHIVING)
- Failed archiving retains local WAL; prolonged failure can fill the filesystem and stop PostgreSQL. Backups require failure reporting and storage monitoring, not merely a scheduled command. [PostgreSQL continuous archiving](https://www.postgresql.org/docs/current/continuous-archiving.html)
- `pg_verifybackup` detects many integrity problems but cannot perform every check a running server performs. PostgreSQL explicitly calls for test restores and validation of the restored data. **Inference:** a recovery time objective (RTO) must include locating keys/backups, provisioning, replay/import, application reconciliation, validation, and safe reopening—not just download time. [pg_verifybackup](https://www.postgresql.org/docs/current/app-pgverifybackup.html)

## Snapshots, encryption, and failure domains are separate properties

- PostgreSQL permits a correct atomic filesystem snapshot of the complete database, including WAL, while running; startup then performs crash recovery. Multiple data/WAL/tablespace volumes must be snapshotted simultaneously, or another supported backup procedure is needed. An ordinary live file copy is not equivalent. [Filesystem backups](https://www.postgresql.org/docs/current/backup-file.html)
- Kubernetes `VolumeSnapshot` describes a storage snapshot, with retention/deletion behavior delegated to its class and driver. It is not an application-level reconciliation mechanism. Longhorn exposes both snapshot-backed and backup-backed CSI workflows; distinguish local snapshots from copies in a backup target. [Kubernetes snapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/), [Longhorn CSI snapshot support](https://longhorn.io/docs/1.12.0/snapshots-and-backups/csi-snapshot-support/)
- **Inference:** a retained PVC, replicated Longhorn volume, or successful CSI snapshot alone establishes neither recovery after loss of the whole cluster nor Clipp's deletion/credential safety. Verify actual backup placement, complete database coverage, and restore behavior separately.
- Encryption is another independent property. For example, OCI Object Storage encrypts objects at rest by default, with Oracle-managed or supported customer-controlled key arrangements. This does not prove application consistency, retention correctness, or protection against an authorized account deleting objects. [OCI Object Storage encryption](https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/encryption.htm)
- **Inference:** an off-cluster encrypted repository needs recoverable keys and credentials outside the failed cluster, restrictive access, protected transport, and a documented deletion/retention policy. Provider-side encryption and client-side encryption have different trust and key-recovery costs; neither is selected here.

## Application consequences of moving the database backward

The following are **Clipp-specific inferences**, not PostgreSQL guarantees. They combine historical restoration with the durable account, credential, deletion, and quota records in the [persistence contract](../persistence-contract.md).

- Restoring an earlier database can restore an account deleted afterward, reverse a suspension or credential-generation increment, and restore consumed refresh tokens or revoked Portal Sessions. Ordinary expiry checks only reject records already expired at the real current time; they do not reconstruct lost revocations.
- Quota Committed and Retained Quota Usage can move backward. PostgreSQL recovery cannot infer consumption or deletions occurring after the selected recovery point. Account-linked audit records in the same restored database also move backward and are not an independent reconciliation authority.
- Maintenance plus invalidating all restored credentials prevents those old credentials being reused, but **does not by itself prevent deleted accounts reappearing or quotas being undercounted**. Fresh Google login could still reach a resurrected account unless account state is independently reconciled or conservatively fenced.
- Any deletion record stored only inside the restored database can itself be rolled back. Strong anti-resurrection semantics require a recovery authority outside that rollback boundary, or a deliberately conservative post-restore policy. The policy and privacy-minimal record design remain live decisions.
- Restoring backup data for inspection also needs isolation: keep public relay/portal entry points closed until schema compatibility, credential invalidation, current-time cleanup, deletion reconciliation, and quota policy have been applied and verified.

## Ready policy choices, not accepted facts

- Candidate objectives: RPO 15 minutes, RTO 4 hours, and a 30-day recoverable window. These are proposed targets, not measured capabilities or guarantees supplied by PostgreSQL.
- Candidate storage boundary: a dedicated encrypted off-cluster object-storage repository supplied by the operator, with equivalent recovery obligations for external and chart-managed PostgreSQL.
- Candidate restore boundary: every restore is explicit maintenance; invalidate restored credentials and reconcile or fence account/quota state before reopening.
- Decide those boundaries before selecting tooling/operator integration, backup cadence, key custody, retention mechanics, or an external deletion-reconciliation record.

No cluster access, backup creation, restore, bucket provisioning, or credential inspection was performed.
