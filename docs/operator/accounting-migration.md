# Offline observed-accounting cutover

Schema13 is an incompatible accounting transition. Fence every serving process
and database writer before migration; keep the service stopped until the complete
migration and startup verification succeed. Preserve an offline backup and its
independent Recovery Journal coverage evidence. This procedure authorizes no live
purge, deployment or provider/cluster mutation by itself.

1. Stop serving under the existing stopped/migrating/serving procedure. Confirm
   no old process can reconnect. Use the intended immutable new image and a
   separate maintenance credential for `clipp-relay -command migrate -config ...`.
2. Verify the completion record reports revision13. Revisions1–12 retain their
   original checksums. Migration13 atomically drops obsolete `weekly_quota_usage`
   (including allocation receipts), deletes obsolete prepaid retained test amounts,
   and renames the retained amount column to `recorded_bytes`.
3. Verify the serving role has no DDL authority and has DML privileges on
   `weekly_relay_usage` and `retained_quota_usage`. It needs no access to
   maintenance-only `recovery_runs`. Start only the new binary, then verify
   readiness and an authenticated relay/portal observation.

Accounts, credentials, plans, audit, deletion bookkeeping and Recovery Journal
state survive. Already confirmed observed weekly totals, sequences and latest
receipts survive. No prepaid credit is translated into observed traffic. Starting
from the original schema1–10 creates empty observed tables before the explicit
reset. There is no permanent mode flag. New binaries reject unmigrated schemas;
old binaries reject revision13 and cannot use the removed prepaid table.

Each migration is transactional. Failure during13 leaves the old table, retained
amounts/column and revision12 intact; keep serving stopped, correct the failure,
and rerun the same migration. Earlier complete revisions may remain applied.
A committed migration with a lost acknowledgment is safe to rerun: it checks
history and does not repeat the reset. Never edit stored checksums, downgrade the
schema, or start an old binary to bypass a failed migration. If reverting code is
necessary, restore the complete isolated pre-migration backup under the existing
Recovery Journal proof and review-hold procedure before any serving transition.

Ordinary startup, restart and offline recovery do not reset recorded usage.
Recovery preserves confirmed totals and compact receipts, invalidates restored
credentials and holds surviving accounts for explicit current administrator review;
see [offline recovery](offline-recovery.md). Unsettled observations may be lost at
forced exit. The cadence is a target; allowance overrun has no hard byte bound,
reporter tails use current peer association, and only one serving writer is
supported. No historical replay reconstructs lost traffic.
