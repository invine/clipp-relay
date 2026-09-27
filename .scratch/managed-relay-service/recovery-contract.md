# PostgreSQL backup, restore, and deletion recovery contract

Status: accepted-policy consolidation; planning only. No image, backup,
restore, performance target or OCI integration is claimed to have been tested.

Owner: [Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md).
Q280–Q307 and their explicit revisions are authoritative. This asset brings
their implementation obligations together without adding new numeric policy.

## Recovery objectives and exceptions

- Target RPO: fifteen minutes of ordinary database-state loss, measured from
  the latest actually recoverable off-cluster state, including archive lag.
- Target RTO: four hours through safe service recovery, including infrastructure,
  reconciliation, validation and required account review. Separately report
  portal availability, accounts ready to relay and outstanding review holds;
  partial availability is not proof of full recovery within the target.
- The timed scenario covers loss of the database volume or Kubernetes cluster
  while the OCI region, protected repositories and operator access survive.
  Region loss or compromise/destruction of recovery authority is not covered
  by that guarantee. Missing mandatory evidence keeps recovery closed even if
  the time target is missed.
- Revised Q289 permits current-week consumption that cannot be recovered to
  reset to zero. Preserve demonstrably recoverable consumption and available
  historical weeks. If the affected set cannot be established, treat the
  restored scope's current-week consumption as unrecoverable. Apply the same
  classification to Retained Quota Usage; do not reimport an amount classified
  as unrecoverable or move an old week's usage into the new week.
- A reset knowingly grants additional allowance. It is an explicit, recorded,
  idempotent disaster-recovery adjustment, never normal zero-on-error behavior,
  a refund on restart, or a bypass for a missing live application key. Plans,
  restrictions, administrator review and deletion gates remain intact.

## Backup, retention and encryption

Bundled PostgreSQL uses pgBackRest for daily full base backups and continuous
WAL archiving through OCI's S3-compatible API. The provisional forced segment
switch interval is sixty seconds. Target seven days of usable PITR history
once established, while reporting the actual range rather than inferring it
from scheduler success. Keep the base and uninterrupted WAL dependencies for
every advertised target. External PostgreSQL may use equivalent provider-managed
PITR with verified access and recovery exercises; it still needs application
reconciliation and the Recovery Journal.

Thirty days is the maximum restore-eligibility age and deadline to request
cleanup, not a promise of physical erasure by the provider at that instant.
Apply the bound to required recovery material and copies, including historical
manifests, object versions, incomplete uploads, journal records and exported
bundles. There is no perpetual archive. Actual provider cleanup is monitored,
retried and reported when overdue. Expired material cannot regain eligibility
because deletion is delayed. Tool retention defaults must not silently preserve
older recoverable chains or manifests. Infrastructure diagnostic logs keep
their separate seven-day ceiling.

Use operator-provided off-cluster buckets, verified HTTPS and OCI server-side
encryption with Oracle-managed keys. V1 does not require another client-side
repository cipher, customer-managed KMS or locked bucket-wide retention on
mutable repository metadata. The trust boundary includes the storage provider
and authorized storage identities; it is not protection from a fully privileged
tenancy compromise.

The operator maintains an encrypted recovery bundle outside the failed cluster:
needed application pepper versions, current recovery access, trust/configuration
and image/schema references. No raw secrets enter Git, rendered public values,
reports or logs. Historic material remains only as required by eligible recovery
or the existing live-record lifetime rules. Do not discard keys required by
live Retained Quota Usage or restore old administrator/provider authority merely
because a historic secret exists in a bundle.

Snapshots/replicated PVCs and logical dumps may supplement this contract but do
not replace database-aware recovery, independent deletion evidence or exercises.

## Workloads and authority

| Component | Execution and authority |
| --- | --- |
| PostgreSQL and WAL archiver | Independently pinned image derived from official PostgreSQL 18 Debian with compatible pgBackRest/libraries installed at build time. PostgreSQL remains its main process and invokes its local archiving helper. |
| Scheduled full backup | Same-Pod scheduler sidecar, same pinned pgBackRest build, distinct non-root backup UID, read-only PGDATA, separate bounded scratch and shared pgBackRest lock paths. No separate attachment of live PGDATA on another node. |
| Backup SQL access | Dedicated non-superuser role with verified backup-control grants, provisioned by bootstrap. An explicitly permissioned private shared Unix socket with exact local authentication is the sole accepted exception to blanket database TLS. Network connections retain verified TLS. |
| Database backup storage access | Dedicated operator-provided OCI Customer Secret Key in referenced Secrets. Only necessary backup/archive repository access; expiry authority is separate and automatic expiry must respect that separation. |
| Live Recovery Journal writer | Dedicated native OCI API-signing identity. Create/read immutable events and narrowly scoped conditional replacement of the journal head; no event overwrite/delete or database-backup access. |
| Cleanup | Scheduled maintenance with separate cleanup authority for chain-safe expiry and journal pruning. Only relevant database/API/DNS/storage-HTTPS paths and required credentials, not all application Secrets. |
| Restore/reconciliation | Explicit run-specific offline command/Job with operator-authorized source/target and recovery access. No automatic restoration at application startup. |

The backup sidecar is trusted to read the entire physical database despite its
restricted SQL grants. Prove that its OS user cannot authenticate as a database
superuser, write live PGDATA or access bootstrap credentials. Preserve group-read
permissions across first boot and restarts in the derived image; the unmodified
official entrypoint's permissions are not sufficient evidence. No root repair,
shared PID namespace, SSH server or Kubernetes exec authority is added.

Database role bootstrap precedes a successful backup, but the scheduler must
wait without blocking PostgreSQL's initial readiness. First-serving validation
checks bootstrap, watcher, backup and journal separately. Backup and archiver
work count within the accepted database Pod envelope alongside PostgreSQL and
the reload watcher; exact allocations and bounded work settings belong to
[Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md).
An increase is not implied by adding the helper.

No enhanced-OKE/workload-identity or node-wide instance-principal prerequisite
is introduced. Credential rotation is explicit and verified, with no fallback
to a broader identity. Buckets, IAM and the encrypted operator bundle are not
provisioned by the chart.

## Recovery Journal and account deletion

Use a dedicated initialized journal bucket, independent of the database's
rollback timeline. Its events contain only the old random Relay Account ID,
stable event ID, format/sequence/hash information and times needed for replay
and retention. No Google identity, email, identity digest, Peer ID, raw credential
or quota-allocation ledger. A conditional-update head/checkpoint identifies the
committed sequence and retained coverage. Repository identity and initial floor
are explicit setup; an unexpectedly empty or wrong repository is not initialized
automatically during ordinary startup or restore.

The deletion ordering is:

1. Under the existing identity/account serialization rules, durably record the
   in-progress operation, invalidate/fence permissions and stop new admission
   and competing mutations. Close live sessions within the accepted ten seconds.
   Re-registration waits until identity detachment completes.
2. Release short database locks before object-storage I/O. Append using a stable
   event ID, validate exact existing content on an ambiguous retry, and verify
   the conditional head commit. An event upload alone is not accepted intent
   until it belongs to the committed sequence. Serialize ordinary appends and
   compare head generations against authorized maintenance writers.
3. After committed intent is proven, finalize identity detachment, current-week
   Retained Quota Usage, credential invalidation and the deletion audit atomically
   through the persistence contract. Resume the same operation after a crash;
   finalization of durable intent is irreversible.
4. Only then report successful deletion. During uncertainty show incomplete/
   retrying, never false success. Keep pending work bounded and observable;
   journal failure need not stop unrelated accounts. The existing post-deletion
   purge clock begins at completed deletion and is never restarted by retries.

Replay targets the old random account ID, never a new account created by the
same External Identity. Reconciliation of a record already applied is a no-op,
not a reason to append a duplicate event. Pending local operations and remotely
committed intents must be reconciled together, including crash boundaries and
weekly reset. A record cannot be pruned while it is still required to make any
eligible target safe; a retained-floor claim alone is not proof of coverage.

During restore, fence all old serving, append and cleanup writers. Verify the
current repository identity, head, retained floor and every required event/hash
through the committed sequence. Complete every list page where enumeration is
used. A successful empty listing or checksum on the remaining objects does not
prove no acknowledged deletion is missing. Missing/inconsistent required evidence
blocks public reopening. The design does not promise detection of coherent
rollback by a privileged operator or compromised journal-writing authority.

## Cleanup and failure handling

Journal cleanup conditionally enters a maintenance generation on the head;
new deletion commits wait while unrelated portal and relay activity continues.
It validates and monotonically advances the recovery cutoff/checkpoint, deletes
only records proven ineligible, then releases maintenance. In-flight append/head
operations compare generations. Interrupted cleanup resumes or is explicitly
reconciled. A stale worker cannot make an expired target eligible again or act
on a broader deletion set; frozen per-run targets and monotonic eligibility
must make delayed requests harmless. Do not treat an expired timer alone as
proof the previous worker stopped. Failed maintenance may leave deletions pending
until reconciled, which must be visible to the operator.

Backup expiry is chain-aware and covers metadata and all relevant object copies.
Never discard unarchived WAL merely to make archiving look successful. Archive
failure or an RPO breach produces an incident, not automatic quota reset,
restoration or blanket termination of otherwise healthy relay activity. Use
bounded warning/protective-maintenance thresholds before storage exhaustion;
their numeric values and cleanup cadence belong to the limits ticket.

## Explicit restoration and safe reopening

1. Declare the incident/run and validate operator-current access, trusted
   repository identity and an eligible source. Preserve the source for diagnosis
   within retention. Verify old serving and maintenance writers are stopped;
   uncertain node ownership requires fencing, not force-assumed absence.
2. Restore into a separate isolated database/volume. Match physical PostgreSQL
   major and backup/schema metadata. Use supported forward schema migration to
   a supported application image; never down-migrate or publicly expose an
   obsolete image solely because it matches the backup.
3. Invalidate every restored Portal Session, Login Grant, refresh generation,
   access token and unfinished authorization flow. Do not throw away unrelated
   peppers needed for Retained Quota Usage. Reconcile deleted accounts and
   pending deletion operations before exposing any restored account data.
4. Preserve recoverable current-week consumption or apply revised Q289's zero
   adjustment where unrecoverable. Preserve available historical records and
   mark known incompleteness rather than invent totals. Install Recovery Review
   Holds independently of normal account status; keep known restrictions.
5. Apply real-current-time expiry and cleanup. Validate current administrator
   allowlist, database/provider credentials and trust separately from backup
   contents. Record step completion against the immutable run/source/target/
   schema/journal identity. Retries cannot apply a second quota reset after
   reopening, and a changed target requires a new explicit run.
6. Produce a privacy-safe validation report and establish the accepted new
   verified backup baseline before deliberate cutover. Inspect current evidence,
   not only a previous Job's successful report. An incomplete run cannot serve.
7. Reopen the portal after structural gates pass, with review holds enforced.
   Freshly authenticated administrators explicitly review each restored
   account's status and assignments in the existing administration page. Neither
   a page visit nor a quota edit silently clears the hold. One reviewed account
   need not wait for every other account to be reviewed. Fresh registrations
   retain the normal Pending/approval path.

The profile shows zero after a reset with a current-week "Usage reset during
recovery" notice, not a claim that no traffic occurred. Recovery and review audit
use the existing restricted audit/privacy policy; public logs do not gain stable
account IDs, private identity claims or credentials. Ordinary healthy process
restarts preserve the existing durable-credential behavior and do not invoke
this restore procedure.

## Baselines, verification and handoffs

Before first public serving, require initialized verified journal identity,
successful off-cluster full backup and WAL-recovery check. Before a schema change,
verify a suitable pre-change recovery point and journal coverage under the
accepted stop/migrate sequence. After disaster reconciliation, establish a new
baseline before cutover. Ordinary healthy restarts do not need a fresh full
backup. Storage failure can delay these gates; neither targets nor security
checks may be silently waived.

Run an isolated end-to-end restore before production, quarterly, and after
material backup/storage/key/schema-path changes. Exercise both external and
bundled modes as applicable, actual OCI access/cleanup, ARM64/AMD64 packaging,
first boot/restart permissions, denied sidecar writes/superuser login, backup
locking, archive lag and shutdown. Test missing WAL/keys/journal records,
ambiguous event/head/database commits, interrupted pruning, deletion followed
by re-registration, weekly reset, revoked credentials, current authority,
review holds and repeated recovery runs. Measure data loss and time through
safe reopening; the daily-backup setting is not evidence of meeting targets.

The [deployment contract](deployment-contract.md) owns rendering/lifecycle
integration. The [persistence contract](persistence-contract.md) owns normal
atomic operations and gains the accepted pending-deletion/recovery markers.
The limits ticket owns the already-delegated resource splits, work caps,
timeouts, warning/stop thresholds and cleanup cadences. The
[acceptance ticket](issues/13-define-acceptance-release-and-operating-criteria.md)
owns executable release evidence and runbooks. No new unticketed policy or
production execution is authorized by this consolidation.
