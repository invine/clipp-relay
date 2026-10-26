# Persistence repository implementation plan

**Status:** prepared from the agreed design; implementation not started.  
**Owner:** relay.  
**Governing contract:** [specification](spec.md) and [decision coverage](decisions.md).

## Establish the implementation run

Inspect the selected relay checkout's current tip, dirty state, worktrees and accepted feature records before choosing a baseline. The inspected architecture baseline is `4e8239cdb6ddc87a5baca56b0522726e0015ca83`; a later implementation run must recheck it. Preserve the existing untracked feature documents, active branches, stashes and private configuration.

Keep one canonical relay-owned feature directory. Use the selected client checkout's coordination procedure where applicable, without creating duplicate client-owned claimable tickets. Record implementation scope, baseline, integration branch, verification environments and authorization before dispatch. Publish numbered tickets only when establishing that run; this plan does not claim work or assert that its steps are completed.

## 1 Establish contracts and migrate one complete credential path

Create the backend-neutral contract module and PostgreSQL adapter with dependency direction from spec I1. Define entity values, projections, operation/transaction scopes and typed outcomes. Keep no SQL/driver escape in those interfaces. Retain existing schema, pool policy and migrations in adapter infrastructure.

Migrate the native token path end to end, including preliminary code/refresh lookup, account lookup, transaction-bound Fosite persistence, required audit and independent refresh-replay reconciliation. Admission must precede logical guards; physical acquisition must follow them. Tests exercise the same composition used by serving, not an alternate repository path that production bypasses.

Prove the full shared-budget/pool-exhaustion behavior and credential rollback/commit/uncertainty cases before broad extraction. Move corresponding transaction fault injection into the adapter. Temporary coexistence during migration is permitted only with an explicit inventory; one operation must not mix transactions or budgets from old and new adapters.

**Evidence:** T1, T3 and T4, including real PostgreSQL. This establishes the seam with both a production adapter and controlled functional test adapters.

## 2 Move accounts plans sessions and required audit

Extract Relay Account, Quota Plan, Portal Session and audit persistence. Migrate Google callback account creation, portal-session workflows, owner/admin mutations, plan assignment/archival, generation changes and account-wide revocation. Keep business eligibility and audit meaning in Auth.

Use shared transaction-scoped repositories for compound changes; preserve lock order, authoritative rechecks, revision conflicts, first-assignment immutability, uncertain completion and required Session Manager notifications. Keep already-adopted Fosite code/grant/access persistence on the same transaction.

**Evidence:** T2–T4 and production protection/race tests. Account policy and native credentials must work through one serving composition.

## 3 Move recorded and retained usage

Move all Quota SQL, including clock sampling, eligibility reads, original-week settlements, durable usage/receipt reads and retirement checks. Keep cadence, pending observations, accounting health and cutoff in Quota. Retain report-time endpoint attribution in Relay.

Move Retained Quota Usage persistence and fresh-registration import into its owner. Establish the repository concurrency guarantee shared by settlement and the deletion seal; do not silently weaken account-row serialization. Preserve confirmed-only retained totals and the existing opening-total representation.

**Evidence:** T2, T3, T5 and relevant T6 cases, including real concurrent settlement versus deletion and week rollover.

## 4 Move deletion recovery and maintenance

Extract deletion-operation/progress/capacity persistence, recovery-run completion and maintenance state. Migrate deletion admission/finalization, retention, pepper coverage, issuer-key verification, explicit native reset and offline recovery SQL.

Keep Recovery Journal effects independent and outside SQL transactions. Preserve proof before finalization, bounded final accounting outside row locks, recovery-specific matching/GREATEST rules, idempotent reset and separate least-privilege roles. Keep legacy credential tables as maintenance details without new serving repositories. Preserve each effective operation deadline; cleanup is a bounded sequence, not one giant transaction.

**Evidence:** T2, T3, T6 and T7 against real PostgreSQL, with controlled journal/commit faults.

## 5 Move read projections and finish composition

Extract owner/admin account/usage reads, quota history, aggregate signals, pagination and bounded cleanup queries. Place joined queries behind dedicated read interfaces while preserving coherent snapshots and separate live samples. Retain fair cleanup batches rather than per-row repository loops.

Wire startup to the final PostgreSQL adapter and repositories, including startup verification and maintenance operations. Remove superseded public pool handles, functional driver types, SQL helpers and pgx-shaped hooks. Reconcile tests and documentation with the final composition; leave no production caller on the compatibility path.

**Evidence:** T7–T8 and all earlier observable behavior. Use Go dependency review and an explicit entity/SQL ownership inventory to verify completion.

## 6 Verify the integrated result

Run the relay's `bash scripts/verify.sh routine` and `CGO_ENABLED=1 bash scripts/verify.sh race`. Run `bash scripts/smoke-postgres.sh` in its disposable local environment for PostgreSQL 17/18 and the relevant database-backed races. Record actual execution and required skips, including unavailable profiles; reuse any integrated strict PostgreSQL verification from operational reliability rather than duplicating it.

Complete Standards and Spec review on the exact final revision, covering every entity owner, joined read consistency, admission/guard/transaction ordering, commit uncertainty and effect delivery. Correct findings and rerun affected checks. Preserve existing wire tests for TCP/WSS/WebRTC Direct, replacement, renewal, invalidation, accounting, Rendezvous and drain.

The refactor is complete when the full scoped persistence surface uses the adapter, no functional module depends on SQL/driver mechanics, all observable guarantees pass through the production composition, and no temporary compatibility route remains. Local verification does not establish hosted CI, provider/native qualification or deployment.

## Implementation discipline

Extract one complete operation at a time while keeping an executable composition. Steps are an extraction sequence, not a mandate to create six parallel tickets: credentials, account changes, settlement and deletion share transaction contracts. Adjust ticket size and dependencies from the current source when implementation starts; do not assign concurrent edits to shared contract files without explicit coordination.

No business glossary changes are needed for repository mechanics. New business requirements, schema changes, altered credential behavior, changed resource budgets or new production backends require a decision assessment against the approved scope; routine implementation choices within this design do not require another user approval.
