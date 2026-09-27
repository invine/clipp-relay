# Define PostgreSQL backup, restore, and deletion recovery

Type: grilling
Status: resolved
Blocked by: 09, 11, 14

## Question

What provider-independent backup frequency, retention, encryption, RPO and RTO, restore validation, schema-migration compatibility, Retained Quota Usage handling, and deletion-reconciliation contract must external and chart-managed PostgreSQL deployments satisfy so a restore cannot silently resurrect deleted Relay Accounts or expired credentials?

## Comments

- The user revised Q242 in [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md)
  to support external PostgreSQL and PostgreSQL managed as part of the chart.
  Cover both modes without assuming a PVC or storage-level replication is a
  sufficient backup/restore or anti-resurrection mechanism. Database lifecycle
  and storage ownership are decided in that deployment ticket; recovery policy
  stays here.

- Claimed after the deployment contract resolved, following the user's explicit
  request to document accepted deployment decisions and continue to the next
  frontier. Use grilling and domain-modeling; no backup setup, restore, bucket
  provisioning or cluster mutation is authorized by this planning work.
  The [deployment contract](../deployment-contract.md) supplies both database
  modes, retained storage, version support, TLS/roles, maintenance phases and
  missing-data startup guards. Next question numbering starts at Q280.

### First round — Q280–Q287 (accepted)

The user accepted the whole batch on 2026-09-07 and requested the next frontier.
This settles the recovery boundaries below, not the tool, journal protocol or
conservative reconciliation choices explicitly deferred by those questions.
Continue this claimed ticket's ready decision frontier before resolving it or
claiming another ticket.

The [PostgreSQL recovery foundations](../research/postgresql-recovery-foundations.md)
capture primary-source facts. The values below are accepted policy targets,
not measured recovery performance or already-provisioned backup facilities.

- **Q280 — Recovery objectives:** Target at most 15 minutes of ordinary
  database-state loss (RPO) and restoration of validated service within four
  hours (RTO) for the declared disaster scenario. Measure RPO from the latest
  actually recoverable off-system state, not merely a configured archive
  interval, and include validation/reconciliation in RTO. These targets do not
  permit restoring revoked credentials, deleted accounts or unjustified quota;
  the security/correctness gates below remain mandatory even if recovery takes
  longer. Backlog/failure must be observable, not silently described as within
  target. Prove the objectives through recovery exercises before claiming them.
  The user's revised Q289 below permits unrecoverable current-week consumption
  to reset to zero; that specific quota exception supersedes the no-quota-gift
  wording here, without weakening credential or deletion gates.
- **Q281 — Bounded backup retention:** Retain encrypted database backups and
  their required recovery material for at most 30 days, with an accurately
  reported available recovery range. No indefinite full-backup archive or
  hidden longer-lived exported copies. This is the backup exception to online
  deletion, not permission to query deleted data from the running relay.
  Exact base-backup/WAL scheduling must respect both usable chain dependencies
  and this maximum; do not promise that every instant of a full 30-day window
  is recoverable before that schedule is defined. The accepted seven-day
  infrastructure-log limit is separate and is not extended to 30 days.
  Accepted Q293 below qualifies this as the restore-eligibility and
  deletion-request deadline, with monitored asynchronous provider cleanup.
- **Q282 — Separate protected backup storage:** Use encrypted off-cluster
  storage supplied by the operator, with backup encryption/trust material
  recoverable independently of the lost relay/database namespace. OCI Object
  Storage is the initial environment to support; equivalent external-provider
  facilities may satisfy the same contract. Separate backup-writing,
  retention-management and restore permissions where the backend allows it;
  the relay serving process receives no bulk backup-read/delete credentials.
  The chart does not create OCI buckets/IAM/KMS or a new credential system.
  Exact immutable-retention/versioning controls and key ownership follow this
  storage decision; encryption at rest alone is not a deletion/recovery policy.
- **Q283 — Database-aware point-in-time recovery:** For bundled PostgreSQL,
  require a PostgreSQL-aware base-backup plus continuous WAL recovery mechanism,
  not Longhorn/Velero snapshots or periodic SQL dumps alone. External PostgreSQL
  may use equivalent provider-managed PITR with verified export/recovery access
  and restore tests. Volume snapshots remain optional supplements, not proof
  of application consistency or compliance with the recovery target. Choose
  the concrete backup tool and schedule only after these outcomes are accepted.
- **Q284 — Independent security-recovery record:** Permit a small protected
  recovery journal outside PostgreSQL's own rollback timeline for the minimum
  facts needed to reconcile acknowledged deletions and restrictive account
  changes. It is recovery metadata, not a live device registry, a second
  general-purpose account database or an implementation of horizontal scaling.
  Never put Peer IDs, raw credentials or unnecessary identity claims into it.
  Exact fields, retention, ordering/acknowledgement and failure behavior must
  be grilled next; this proposal does not silently impose a new per-buffer or
  per-credit-block remote write on the data plane. A restored database and
  its old audit table alone cannot prove which later deletions occurred.
- **Q285 — Fresh authentication after every restore:** Before any restored
  environment can serve, invalidate all restored Portal Sessions, Login Grants,
  refresh generations, access tokens and incomplete authorization flows/codes.
  All users sign in again; no old relay connections or process identity return.
  This is restore-only behavior, not a change to ordinary restart durability.
  Do not achieve it by indiscriminately dropping peppers still required for
  Retained Quota Usage. Account states and history remain subject to separate
  reconciliation, not discarded merely to invalidate credentials.
- **Q286 — No silent quota or restriction rollback:** Reconcile current-week
  usage/Retained Quota Usage and restrictive account changes before resuming
  affected accounts. If the safe current state cannot be established, withhold
  relay access/credit until a conservative recovery action is explicitly
  applied; never treat missing consumption as zero or an older Active row as
  evidence that a later suspension/deletion did not happen. The next round
  chooses the minimum journal data versus conservative fencing/review needed
  to achieve this. Ordinary RPO tolerance does not authorize restored quota
  gifts. No unproven exact traffic ledger is introduced by this requirement.
  Superseded in part by the user's revised Q289: zero unrecoverable current-week
  consumption instead of withholding credit. Restriction/deletion reconciliation
  remains mandatory; Q290 supplies the conservative permission-review policy.
- **Q287 — Restore in isolation, then cut over:** Restore into a separate
  database/volume with no public relay admission; preserve the source while
  investigating and validating, subject to approved retention. Check recovery
  integrity, supported PostgreSQL/schema versions, deletion/retention cleanup,
  credential invalidation and quota/state reconciliation before deliberately
  switching the single relay to the recovered target. Missing reconciliation
  evidence fails closed. Do not automatically overwrite production, skip
  schema checks, run down migrations, or reopen service merely because
  PostgreSQL accepts connections. Recovery exercises use the same gates.

After this batch, the ready branches are concrete backup mechanism/schedule,
storage/key/retention ownership, independent recovery-journal privacy and
acknowledgement rules, conservative quota reconstruction, and restore/cutover
verification. Ask ready independent decisions together. This ticket remains
claimed; no backup or restore has been executed.

### Second round — Q288–Q296 (accepted; Q289 revised by user)

The user accepted Q288 and Q290–Q296 and replaced Q289 with: "set week's
consumption to zero if it can't be restored." The rejected proposal to pause
relay credit until Monday is not part of the contract. Q293's qualification of
Q281 is now accepted. Continue the remaining decision frontier of this claimed
ticket; tool integration and journal mechanics have not yet been accepted.

- **Q288 — Disaster scope:** Apply the initial recovery objectives to loss of
  the database volume or entire Kubernetes cluster while the OCI region,
  protected off-cluster repository and operator access remain available. The
  clock includes replacement infrastructure and application validation. Region
  loss or simultaneous destruction/compromise of the repository and its access
  authority is outside the initial timed objective, not silently guaranteed.
- **Q289 — Reset unrecoverable weekly consumption (user revision):** During
  explicit disaster restoration, set current-week Quota Committed to zero
  wherever that consumption cannot be recovered. Preserve demonstrably
  recoverable consumption; if the affected set cannot be established, treat
  all current-week consumption in the restored scope as unrecoverable. Apply
  the same recovery rule to current-window Retained Quota Usage so stale
  deletion/re-registration records cannot reintroduce amounts classified as
  unrecoverable. Preserve available historical weeks; do not erase all history
  or call incomplete historical records complete.
  This knowingly permits additional quota after disaster recovery and
  supersedes Q280/Q286's contrary quota language only for this case. No
  week-long credit block or additional per-allocation recovery ledger is
  required. The original four-hour service-recovery objective remains, including
  mandatory deletion, credential and administrator-review gates; missing that
  target must still be reported honestly. Normal restarts, database reconnects,
  missing application keys and ordinary allocation errors do not authorize a
  quota reset. Plans and account restrictions do not reset with consumption.
  Record the recovery adjustment without claiming it measures actual traffic;
  exact operator-run idempotency and portal presentation follow below. Restores
  must never run old and recovered serving writers concurrently.
- **Q290 — Minimal journal and conservative permission review:** Use deletion
  tombstones keyed only by old random Relay Account ID, with a unique event ID,
  format version and time needed for retention/replay. No External Identity,
  email, HMAC identity digest, Peer ID, raw credential or quota allocation log.
  Reconcile deletion records before exposing any restored account. For lost
  restrictions, hold restored accounts from relay access until administrator
  review of status and plan/overrides; preserve known Pending/Suspended/Denied
  states rather than automatically activating them. Fresh accounts remain
  Pending under normal approval. The hold, rather than a second administrative
  event ledger, implements the restrictive-change part of Q284/Q286. It does
  not prevent sign-in to an otherwise safely reconciled portal or override
  Q289. This journal is still an explicit, restricted retention exception.
- **Q291 — Deletion acknowledgement:** Never report successful deletion before
  its independent recovery record is durable and the online deletion transaction
  has completed. An asynchronous database-only outbox is insufficient proof.
  If journal persistence is unavailable, report deletion as incomplete, not
  successful; normal unrelated relay traffic need not stop solely for that
  outage. Once durable deletion intent is accepted, retry/recovery completes
  the deletion even if the browser loses its response or the process crashes;
  do not undo that intent. Exact short-lock/fencing/idempotency protocol and
  handling of ambiguous writes must be specified and tested after this policy
  is accepted. No long remote call inside an account database transaction is
  implicitly approved.
- **Q292 — Journal coverage and retention:** Keep deletion records for the full
  period in which any eligible restore could contain that old account, bounded
  by the accepted backup age cutoff. Purge an event only when all pre-event
  restore targets are ineligible; do not restore an expired backup without its
  reconciliation evidence. Verify journal completeness and authentic provenance
  independently of the restored database; unavailable, missing or inconsistent
  required records block reopening. This does not authorize a perpetual deleted
  identity denylist. Under the accepted Q293 qualification, apply the same
  restore-eligibility/physical-cleanup distinction to journal copies. The exact
  checkpoint/manifest integrity mechanism remains to be selected.
- **Q293 — Qualify the thirty-day deletion promise:** Make Q281's
  thirty days a hard maximum for restore eligibility and the deadline to submit
  deletion of expired backup material, rather than a guarantee of physical
  erasure by the object-storage provider at that instant. Cleanup must cover
  versions, copies and journal material, be retried and monitored, and report
  overdue physical cleanup as a retention incident; no indefinite archive or
  reuse of expired material. Provider completion remains asynchronous. This is
  an accepted qualification of Q281, not a physical-erasure guarantee.
  Fact basis: [OCI lifecycle management](https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/usinglifecyclepolicies.htm)
  describes best-effort processing, day rounding and potentially delayed
  deletion; a lifecycle rule alone cannot prove an exact erasure deadline.
- **Q294 — Schema and application compatibility:** Record PostgreSQL major,
  schema revision and relevant application image identity with each backup.
  Restore physical data using its compatible PostgreSQL major, keep the relay
  stopped, and use supported forward migrations before running a supported
  application release against the resulting schema. No down migrations or
  public exposure of an obsolete image merely because it matches an old backup.
  Unsupported recovery paths remain offline until a tested path exists.
- **Q295 — Recovery exercises:** Require an isolated end-to-end restore before
  first production use, every three months thereafter, and after material
  backup/storage/key/schema-path changes. Include deletion after the recovery
  point, previously revoked credentials, spent quota, missing journal/WAL and
  missing keys; verify safe refusal as well as successful recovery. Measure
  actual data loss and time through safe reopening, distinguish partial portal
  availability from full relay recovery, and keep privacy-safe evidence. No
  automatic destructive production restore.
- **Q296 — Backup cadence and useful history:** Start with a daily full base
  backup, continuous WAL archiving with a provisional sixty-second forced
  segment-switch interval, and a target of seven days of usable point-in-time
  history once established. Preserve the older base/WAL dependencies needed
  for that window within the overall maximum age policy. Report the actual
  recoverable range and measured archive lag; neither a sixty-second setting
  nor a scheduled Job proves the fifteen-minute RPO. Bound backup concurrency
  and measure CPU/memory/storage impact against the deployment budget before
  claiming the daily-full schedule is operationally validated.

The [backup-tool and object-retention research](../research/postgresql-backup-tool-options.md)
records primary-source facts and remaining compatibility checks, not a selected
tool. Tool integration, storage protection/key ownership, journal
write/replay completeness mechanics and final restore gates remain branches to
resolve after their prerequisites, not implementation authority. No second
ticket has been claimed.

### Third round — Q297–Q304 (accepted)

The user accepted the entire batch. This selects the backup tool/image,
encryption and credential boundaries, journal structure, pending-deletion and
review behavior, explicit recovery execution and backup-degradation policy.
Execution placement and remaining operational choices are not silently supplied
by this acceptance. The ticket remains claimed pending its last decision round
and consolidated consistency review.

The [backup-tool research](../research/postgresql-backup-tool-options.md) and
[Recovery Journal storage facts](../research/recovery-journal-storage-facts.md)
support these choices. Tool feasibility is not tested integration. This batch
does not ask the user to re-approve the zero-on-unrecoverable-consumption rule.

- **Q297 — Backup tool and database image:** Select pgBackRest for bundled
  PostgreSQL backups through OCI's S3-compatible API, subject to an end-to-end
  integration release gate. Build an independently pinned database image from
  the official PostgreSQL 18 Debian base with a pinned compatible pgBackRest
  executable and libraries installed at build time. This explicitly qualifies
  the deployment contract's unmodified official-image assumption; no startup
  package installation, privileged init installer or floating versions.
  External databases retain equivalent provider-managed recovery. Exact helper
  placement, scheduling, role permissions and budget split depend on this
  selection and must be specified without assuming a remote Job eliminates
  the required local archiving helper.
- **Q298 — Encryption and recovery material:** Use verified HTTPS plus OCI
  server-side encryption with Oracle-managed keys initially for backup and
  journal buckets. Do not add a second client-side repository cipher or a
  customer-managed KMS key in v1. This trusts the storage provider's encryption
  and access boundary, not protection against a fully privileged tenancy
  compromise. The operator must separately retain an encrypted recovery bundle
  of required application pepper versions, database/provider access material,
  trust/configuration and image/schema references outside the failed cluster.
  No raw secrets in Git, manifests, reports or logs. Preserve old recovery
  material only while eligible backups require it, within approved retention;
  never delete a key still required by live Retained Quota Usage. Provider
  access revocation/rotation must not silently destroy recovery access.
- **Q299 — Storage credentials:** Use operator-provisioned, narrowly scoped
  identities referenced through existing Kubernetes Secrets: an OCI Customer
  Secret Key for the pgBackRest S3 path, and a separate dedicated native OCI
  API-signing identity for the journal. Separate restore/cleanup authority from
  live journal-writing authority; the relay never receives bulk database backup
  read/delete credentials. Do not assume this OCI-hosted Kubernetes cluster is
  enhanced OKE or grant node-wide instance-principal authority to avoid a Secret.
  Workload identity can be a later verified integration rather than a v1
  deployment prerequisite. Credential rotation is an explicit verified
  maintenance operation with fail-closed dependent writes, not silent fallback
  to a more privileged principal.
- **Q300 — Journal repository and completeness:** Put the Recovery Journal in a
  dedicated operator-provided bucket using native OCI conditional writes. Use
  immutable event records plus a small conditional-update head/checkpoint that
  identifies the committed sequence and hashes needed for recovery. Allow the
  live identity create/read on its event namespace and tightly scoped head
  replacement, but no event overwrite or deletion; cleanup is separate. A
  stable event ID and byte-for-byte payload check resolve ambiguous retries.
  Initial repository identity and retention floor are explicit operator setup,
  not auto-created when a configured journal is unexpectedly empty. Before
  recovery, fence all journal writers and verify every required sequence/hash
  through the current head, including complete pagination where used. An empty
  listing is not proof of no deletions. Pruning advances a validated retention
  checkpoint only after covered restore targets become ineligible. This detects
  missing/corrupt required records, but does not promise detection of a coherent
  rollback by a privileged repository operator or compromised journal-writing
  authority; that compromise is outside
  Q288's timed disaster scope. Do not enable locked bucket-wide retention on
  mutable backup/head metadata by default; IAM and explicit cleanup ownership
  are the initial protection boundary.
- **Q301 — Incomplete deletion experience:** Introduce an internal durable
  deletion-in-progress operation, not a new public account status. After fresh
  authenticated confirmation, fence that account from new credentials/relay
  use and competing account mutations; close existing relay sessions within
  the accepted ten-second revocation bound. Keep the identity unavailable for
  re-registration until online deletion finishes. Persist/reconcile the stable
  journal event outside short database transactions, then atomically finalize
  the existing identity detach, current-week usage retention and deletion audit.
  Return a clear incomplete/retrying result during uncertainty, never "deleted"
  prematurely. Retries/restarts resume the same operation; finalization after
  durable journal intent is irreversible. A pending deletion does not capture
  a stale week's usage and import it into a later week. The current online
  post-deletion purge deadline begins at completed deletion; pending-state
  backlog must be bounded/observable and is not permission to retain a completed
  deletion's online identity. Ordinary unrelated accounts remain usable during
  a journal outage. Mechanical race/crash tests must establish these guarantees.
- **Q302 — Account recovery review and portal information:** Keep the review
  hold separate from normal Pending/Active/Suspended/Denied status. Show it in
  the existing administrator account page with an explicit fresh-authenticated
  confirmation after review of current status, plan and overrides; ordinary
  sign-in, read-only page visits or quota edits cannot silently clear it. The
  account portal explains that relay access awaits review. Where Q289 reset
  usage, show zero with a current-week "usage reset during recovery" notice;
  don't imply that no traffic occurred. Keep available history and mark known
  incomplete historical data rather than fabricate totals. Record privileged
  review and adjustment events under existing audit/privacy rules, not public
  account-ID logs. No new permanent device or deleted-identity linkage.
- **Q303 — Explicit idempotent recovery run:** Use an operator-invoked offline
  recovery command/Job with an explicit run ID and pinned source/target/schema/
  journal references, not automatic restore at startup. Perform and record
  credential invalidation, deletion reconciliation, quota preservation/reset,
  review holds and current-time cleanup before issuing a validation report.
  Bind completed steps to the exact run and target; retrying the same completed
  run must not reset consumption accrued after reopening. Validate the actual
  current state rather than accepting an old report, verify old serving writer
  absence, and require deliberate cutover. An incomplete run keeps public
  serving closed; changing source/target requires a new explicit run. The
  current operator-controlled administrator allowlist, provider/DB credentials
  and trust configuration must be revalidated; restoring a recovery bundle is
  not permission to reinstate a removed administrator or obsolete credentials.
  Restore-required historical key versions do not become current authority.
  The
  four-hour objective includes these gates and required review; the zero-usage
  exception alone does not authorize activation of restricted accounts.
- **Q304 — Backup degradation:** Backup/WAL failures and an RPO breach should
  raise clear operator-visible incidents, not automatically reset quota, restore
  data or disable every otherwise healthy relay session. Preserve unarchived
  WAL; never discard it to make an archive command appear successful. Apply
  explicit protective admission/maintenance action before storage exhaustion,
  with numeric warning/stop thresholds and bounded work settings owned by
  [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md).
  Journal failure follows Q291/Q301's deletion-specific behavior. Report actual
  recoverable history and unmet objectives without requiring Prometheus to be
  installed; delivery/runbook verification remains in the acceptance ticket.

The remaining dependent branches are the selected backup helper's execution
and permission model, precise journal/deletion transaction and checkpoint
invariants, and a final consistency check against deployment and privacy
contracts. The prerequisites above are now accepted; apply them without
reopening policy or treating unverified integration as evidence. This ticket
remains claimed; no subsequent ticket is claimed.

### Fourth round — Q305–Q307 (accepted)

The user accepted the entire final operational batch. All Q280–Q307 decisions
are accepted, with the explicit Q289 revision and Q293/Q297/Q305 qualifications.
The [consolidated recovery contract](../recovery-contract.md) incorporates the
accepted mechanical invariants; numeric controls retain their existing limits
ticket owner. No implementation, provisioning or successful restore is implied.

The [bundled backup execution facts](../research/bundled-backup-execution-facts.md)
establish the candidate's prerequisites, not a tested deployment. These are
the remaining operational choices identified after Q297–Q304; exact mechanics
that merely implement accepted guarantees are consolidated below, not turned
into another user policy question.

- **Q305 — Same-Pod backup execution and its trust boundary:** Run a small
  scheduler sidecar beside bundled PostgreSQL using the same pinned pgBackRest
  build, a distinct non-root backup OS user, read-only PGDATA and separate
  writable lock/scratch paths. PostgreSQL remains its own main process and
  performs WAL archiving using its local helper. Give the scheduler a dedicated
  non-superuser backup SQL role with only verified backup-control grants, not
  bootstrap credentials or general table-write authority. Use an explicitly
  permissioned private shared Unix socket for that local SQL connection, with
  an exact local authentication mapping/rule that cannot authenticate the backup
  OS user as a superuser. This is an explicit exception to the blanket verified
  database-TLS wording for this same-Pod local socket only; network database
  connections still require verified TLS. The helper can read the entire
  physical database, so it remains a highly trusted component despite its
  restricted SQL role. Preserve group-read permissions across first start and
  restarts through the derived image; do not rely on the unmodified entrypoint
  preserving them, share PID namespaces, or add root permission repair.
  Bootstrap provisions the role; the scheduler waits without blocking initial
  PostgreSQL readiness, and backup verification gates first serving separately.
  Account for scheduler/archive work within the existing database workload
  budget in the operational-limits ticket; an increased budget requires an
  explicit decision, not an unbudgeted sidecar. No Kubernetes exec permission,
  SSH server, cross-node PGDATA attachment or database process supervisor is
  introduced. Denied writes/superuser login and successful backup/restore are
  release gates; failed verification cannot silently broaden privileges.
- **Q306 — Cleanup ownership and brief deletion pauses:** Use an explicitly
  scheduled maintenance workload with separate cleanup credentials for
  chain-aware backup expiry and Recovery Journal pruning. Ordinary backup
  work does not automatically gain deletion authority; configure automatic
  expiry accordingly. For journal pruning, conditionally acquire a short
  maintenance state on the journal head, defer new deletion commits, publish
  the validated retention checkpoint/restore cutoff, remove only ineligible
  records, then release the state. Existing relay traffic and unrelated portal
  work remain available; affected deletions show pending rather than success.
  Writer/head generations fence in-flight appends, so a timed-out cleanup run
  cannot later prune against an obsolete checkpoint. Interrupted cleanup
  resumes or requires explicit reconciliation, never assumes its partial work
  succeeded. Database-backup expiry preserves required base/WAL dependencies
  and covers manifests, versions and incomplete uploads within Q293's policy.
  Q300's privileged-authority threat boundary remains unchanged. Numeric cadence,
  batch limits, deadlines and alerts belong to the operational-limits ticket;
  retention deadlines themselves are not relaxed.
- **Q307 — Verified baseline before opening:** Require an initialized verified
  journal plus a successful off-cluster full backup and WAL-recovery check before
  first public serving. Before a schema-changing migration, verify a recent
  suitable pre-change recovery point and journal coverage while following the
  accepted stop/migrate sequence. After a disaster restore, finish structural
  reconciliation and establish a new verified backup baseline before cutover.
  This may delay an installation, migration or recovery when storage is
  unavailable; don't silently bypass the gate or claim the four-hour objective
  was met. Ordinary healthy process restarts do not require a new full backup.
  The baseline check does not replace the accepted full restore exercises.

### Mechanical consolidation required by accepted policy

These are implementation obligations arising from accepted policy, not new
privileges or an additional policy round:

- Account deletion first records its durable in-progress operation and fences
  admission; no account/identity database lock spans object-storage I/O. Append
  the stable event and verify its conditional head commit before online
  deletion finalizes. The committed journal sequence defines accepted recovery
  intent. An uploaded but uncommitted event is not proof of a completed deletion;
  reconcile an ambiguous head update before retry or finalization. Resume using
  the same operation ID, never a fresh destructive operation per network retry.
- Serialize single-process appends and use conditional head generation checks
  against other authorized maintenance writers. Validate event bytes, sequence,
  hashes, repository identity and retained floor. A checkpoint cannot authorize
  a pre-checkpoint restore target that needs already-pruned deletion records.
- Deletion replay uses old random account IDs and cannot delete a newly
  registered account with a different ID. Replayed deletions and incomplete
  local deletion operations use the same idempotent finalization guarantees;
  do not recursively create new journal events merely for replay. Current-week
  usage follows revised Q289; no stale week is imported across the reset.
- Restore-only credential invalidation and quota adjustments are tied to a
  durable run/target marker. Repeating validation after cutover cannot repeat a
  quota reset. An old successful report never substitutes for inspecting current
  schema, journal coverage, privileges and writer absence.
- The portal may reopen after structural recovery/deletion/credential gates
  pass, with per-account Recovery Review Holds enforced. Administrator review
  takes place there; do not create a deadlock requiring portal-based review
  while that portal is closed. Clearing one account's hold does not require
  every other account to have been reviewed. Report portal availability,
  reviewed-account relay availability and outstanding holds distinctly when
  assessing the recovery objective.
- Keep authoritative current operator secrets/allowlist separate from restored
  database contents. Preserve only eligible historic material needed for recovery;
  do not restore old provider access or administrator authority by default.

After this batch, consolidate the recovery contract and check these invariants
against the selected helper/cleanup shape. Ask only a genuinely new policy
conflict if that review finds one; the provisional numeric settings already
have an owner in the operational-limits ticket. This ticket remains claimed.

## Answer

Resolved after the user accepted Q305–Q307, completing the Q280–Q307 dialogue
and confirming the final operational choices. The consistency review found no
additional user-policy conflict; it only required mechanical consolidation of
the local-socket TLS exception and pending-versus-completed deletion wording.
Earlier "claimed" and "remaining" notes above record the dialogue at that time;
this resolution and the accepted amendments are the final state.

The accepted [PostgreSQL backup, restore, and deletion recovery contract](../recovery-contract.md)
is the implementation handoff. It specifies:

- Fifteen-minute RPO and four-hour validated-recovery targets for the declared
  surviving-region/repository scenario, measured rather than assumed.
- Daily full backups and continuous WAL, provisional sixty-second switching,
  a seven-day useful PITR target and thirty-day eligibility/cleanup-request
  ceiling with monitored asynchronous provider deletion.
- pgBackRest in a pinned derived PostgreSQL image, same-Pod distinct-UID backup
  scheduling with read-only PGDATA, the narrowly accepted private-socket TLS
  exception, separate cleanup authority and no new implicit resource budget.
- Off-cluster operator-managed repositories and recovery material, provider-side
  encryption, separate scoped identities and no automatic infrastructure setup.
- A minimal deletion-only Recovery Journal, proven committed intent before
  deletion finalization, restart/idempotency guarantees and safe checkpointed
  pruning that may defer deletions but not unrelated relay traffic.
- Explicit isolated restores, credential invalidation, deletion reconciliation,
  current authority validation and per-account Recovery Review Holds.
- The user's Q289 exception: reset unrecoverable current-week consumption to
  zero, preserve recoverable usage/history, and display the recovery adjustment.
  This permits additional quota after disaster restoration, not normal refunds,
  zero-on-error behavior or removal of account restrictions.
- Verified first-install/pre-migration/post-restore baselines, quarterly and
  change-triggered recovery exercises, and honest degradation reporting.

The [deployment contract](../deployment-contract.md) and
[persistence contract](../persistence-contract.md) now point to the accepted
amendments. [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md)
owns the resource split, concurrency, retry/deadline, cleanup cadence and
storage/lag thresholds; its dependency now includes this resolved ticket.
[Define acceptance, release, and operating criteria](13-define-acceptance-release-and-operating-criteria.md)
owns executable verification and runbooks. No extra unticketed policy was
identified, and no subsequent ticket was claimed during this resolution.

Validation here was document consistency, local-link checks and `git diff
--check`, not a database restore or integration test. No production code,
chart, image, bucket, credential, cluster or backup was created or modified.
