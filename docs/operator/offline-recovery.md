# Offline accounting recovery

This command performs local application reconciliation of an **isolated restored
database**. It does not fetch/restore backups, replay WAL, choose a provider
restore point, reset quotas, fence external writers, change journal retention,
or qualify a live recovery. Record the backup and journal coverage evidence,
source revision, schema revision, target identity and outcome separately from
provider/cluster RPO/RTO qualification.

1. Stop and fence every serving and maintenance writer to the restored target.
   Keep the target isolated from service listeners. Independently fence journal
   writers and obtain the current authoritative journal, repository identity,
   attested coverage floor and floor hash. The backup must fall within that
   qualified coverage; a database copy of a head is not independent proof.
2. Use an explicit private configuration for the isolated target, migration
   credentials, the current independent journal signing identity, current
   administrator allowlist and required retained-usage pepper keyring. Run the
   normal offline schema migration first. No old binary may serve the new schema.
3. Generate and retain one random recovery-run UUID. Assign an opaque immutable
   source-backup identifier (without identity metadata or secrets). The target
   identifier must exactly match the configured/current PostgreSQL database name
   and differ from the source. Double-check the configured host/TLS authority and
   database identity against the isolated restore receipt before executing:

   ```sh
   clipp-relay -command recover -config /absolute/private/restored-target.json \
     -recovery-run 01234567-89ab-4cde-8fab-0123456789ab \
     -recovery-source backup-opaque-identifier \
     -recovery-target isolated-restored-database-name
   ```

4. Treat a nonzero exit and `service.startup/failed/recovery_unconfirmed` as
   incomplete recovery. Keep the target fenced. Missing/wrong journal evidence,
   missing chain pages, changing head/ETag, unavailable keys or database failures
   never produce successful completion. Retry the **same** source, target and run
   UUID after correcting the cause. Do not invent a new run just because a reply
   was lost. A committed receipt makes the same run a no-op, including after
   later administrator review. Conflicting reuse fails closed.
5. Retain the maintenance-only `recovery_runs` completion receipt (run/source/
   target/schema, qualified journal head and completion time). Its insertion is
   atomic with all reconciliation changes. Serving roles need no access to this
   table; maintenance needs SELECT/INSERT, and migration owns it. Do not grant
   serving writes or copy a receipt between different target identities.
6. Before routing traffic, use fresh current Google authentication and the
   current authoritative administrator allowlist. Review each surviving
   account's status, plan and overrides. On its admin page choose **Complete
   recovery review of status and quotas**, supplying the ordinary CSRF, reason
   and current revision. This protected `recovery-review` action clears only the
   independent hold, increments revision and writes the existing
   `account_reviewed` audit event. Approving/reactivating an account, changing a
   quota, visiting a page or restarting does not clear the hold. The account's
   ordinary status and quota restrictions continue to apply after review.

The reconciliation command opens no listener and never appends to the independent
journal. It verifies the complete ordered hash-linked path from the configured
floor through the frozen current head and rereads head/ETag before SQL locks.
Valid orphan uploads are excluded. Local SQL cannot atomically fence an object
store writer; external offline fencing is a prerequisite, not a claim of the
command. The exact current schema/checksums must match.

In one SQL transaction it invalidates **all** restored portal sessions, Login
Grants, access/refresh credentials, codes and unfinished flows; advances surviving
accounts' credential generation; and applies a Recovery Review Hold independently
of Pending/Active/Suspended/Denied. Fresh portal authentication remains possible
for review. Held accounts cannot obtain/refresh credentials, authenticate Relay,
use Discovery or pass Quota eligibility. It applies deletion only to committed
old random UUIDs, including a restored account whose deletion bookkeeping did
not exist at backup time. A fresh UUID sharing an External Identity is untouched.

Survivors' weekly Recorded Relay Usage, sequence and compact latest receipt remain
unchanged. No live session, unused prepaid credit, abandoned operation or lost
observation is reconstructed. Old UUIDs are sealed under account-row serialization;
only confirmed current-week usage is retained in the existing pepper/digest form.
Existing retained amounts cannot be reduced or doubled by restoring the same
logical deletion. Current-week registration imports the retained total once into
a fresh Pending UUID with sequence zero and no invented receipt. Prior-week usage
never moves into a new window. Ordinary current-time retention remains in force.

The `recover` command is for recorded accounting. In the staged development
revision, normal serving remains prepaid until the final accounting migration;
this local tool does not select or activate a serving mode. The final cutover
renames the legacy retained amount column along with its explicit prepaid reset.
