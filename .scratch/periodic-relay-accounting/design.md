# Relay, Quota, and Auth architecture

Status: behavior and ownership for architecture candidates 1, 2 and 3 agreed on 2026-10-23; CQRS and accounting persistence scope agreed on 2026-10-24; implementation not started.

Scope: Relay, Quota, and Auth in Clipp Relay. This records complete accounting ownership (candidate 1), periodic settlement (candidate 2), and protected account operations with session admission (candidate 3), based on revision cca0bb2 and .scratch/thoughts.md.

## Agreed behavior

1. Record observed Charged Relay Traffic instead of prepaid credit. Preserve independent HOP/STOP endpoint charging, including twice for endpoints belonging to one Relay Account. These counts are not exact application payload measurements.
2. Share usage across all sessions of a Relay Account.
3. Settle on a one-second target cadence. The allowance is soft; no fixed maximum byte overrun is promised.
4. Close affected sessions when accounting has not successfully refreshed within a configurable deadline, default three seconds. A balance read cannot mask unconfirmed pending usage. Configuration changes take effect on restart.
5. Accept crash loss of usage since the last successful settlement. Durable totals survive restart. This is not a hard one-second loss guarantee.
6. Record all attributable overrun without clamping usage to the allowance; display remaining allowance as zero. Preserve current report-time attribution. The stock reporter identifies a peer, not the original physical connection; observations without an account association remain aggregate observations.
7. Preserve immediate account invalidation for revocation, suspension, and administrative policy changes. Polling does not replace admission/invalidation ordering.
8. Preserve current grant-local termination behavior for now. Auth checks Login Grants during authentication and renewal; existing sessions have no live grant association and retain their fixed expiry after grant-local termination. Account-wide revocation still closes them.
9. During graceful shutdown, continue settlement through drain and attempt a final flush within existing shutdown limits. Failed settlement must not indefinitely delay exit.
10. On deletion, close sessions immediately and attempt final settlement. Failure to preserve pending usage must not block deletion. Retain only confirmed durable usage, and prevent late settlement from recreating deleted account state. This supersedes the earlier suggestion to leave deletion pending solely for accounting settlement.
11. The transition may reset quota accounting: only the owner's test account is active. Prefer resetting accounting over deleting unrelated accounts or credentials. No data has been purged in this design session.
12. Preserve Monday 00:00 UTC weekly windows without rollover. Attribute observations to the week when the reporter processes them, even when settlement occurs later.

## Ownership

| Module | Owns |
| --- | --- |
| Relay | Report-time attribution, physical sessions, exact-connection admission and replacement, closure, drain, live capacity. |
| Quota | Pending account-week usage, settlement cadence, retries, uncertain-commit reconciliation, balance refresh, accounting health and exhaustion decisions. |
| Auth | Credentials, Login Grant validation, authoritative account policy, protected account operations and complete mutation/invalidation sequencing. |

Quota does not interpret Login Grants. Relay does not orchestrate balance reads, settlement timers or persistence retries. Keeping account references and session expiry in Relay does not transfer ownership of the account to Relay.

## Complete accounting ownership (candidate 1)

This candidate is absorbed into the periodic accounting design rather than implemented as a separate prepaid refactor. Quota owns the complete observation-to-accounting operation. Relay must not read usable credit, split observations into 64 KiB blocks, fund credit, or coordinate persistence retries.

Remove the old caller-managed read/fund/split/consume sequence. Keep concurrent observation aggregation, settlement, rollover and reconciliation inside Quota. Pending usage represents traffic already observed; account revocation must not reinterpret the old credit-discard behavior as permission to erase that usage. Deletion retains its explicitly agreed best-effort exception.

The deletion test is concrete: removing the deep accounting operation would force aggregation and reconciliation knowledge back into Relay callers. Moving that sequence behind Quota's interface gives it locality and gives all reporters leverage from one implementation.

## Protected account operations and session admission (candidate 3)

Deepen the existing Auth, Relay and Quota modules. Do not add a separate Relay Account access module or a forwarding coordinator.

Auth owns protected account operations and complete mutation completion: authoritative rechecks, shared account synchronization, mutation commit outcomes, conservative fencing after uncertain commits, local policy effects, and sequencing of cleanup after protection is released. Relay participates in protected admission while retaining ownership of physical session installation, replacement, rollback and closure. Quota retains accounting decisions.

Required cooperation must be explicit in the interface. Production correctness must not depend on optional method discovery or a silent fallback that omits required synchronization or invalidation. Concrete Go declarations and internal helper placement remain design work for the specification and implementation.

Preserve these ordering guarantees:

- Admit database work before waiting for peer or identity protection; nested operations reuse the existing database module's admitted work budget.
- Protect all involved accounts in consistent order, including both accounts during cross-account same-peer replacement.
- Keep account protection across the authoritative recheck, accounting eligibility assessment and session installation. A committed invalidation must not be followed by installation from an older authority observation.
- Apply local invalidation under account protection. Detach session authority before performing connection-close I/O outside logical guards.
- Handle mutation uncertainty conservatively and prevent admission until authority is reconciled; never resurrect closed sessions.
- If the outer admission operation fails after session installation, remove the installed session.
- Preserve one-session-per-Peer-ID replacement, failed-renewal behavior, session caps, expiry and existing revocation semantics.

Startup wiring connects modules; it does not decide account policy, quota exhaustion or session-cap effects. Put accounting decisions in Quota and live session-cap enforcement in Relay, invoked through Auth's complete mutation workflow.

The interface is the test surface. Keep real HTTP/PostgreSQL/libp2p coverage of uncertain revocation, admission racing mutation, session-cap reduction, outer-operation failure after installation and cross-account replacement at capacity. Tests should use the same production composition instead of copying policy logic from startup wiring. Retain controlled authority adapters for interleavings, without making helper-call counts the behavioral contract.

Depth comes from hiding complete-operation ordering, not adding a forwarding module. Removing the deepened account mutation operation would spread commit/fence/invalidate/cleanup knowledge back into each mutation caller.

## Interface direction

The accounting seam expresses complete operations and outcomes: accept attributed observations, assess accounting eligibility for admission, handle relevant account/lifecycle changes, and report exhaustion or accounting failure. Concrete Go methods and data structures remain implementation design work.

Depth hides aggregation, cadence and reconciliation behind the interface. Locality puts accounting rules in Quota; leverage lets every Relay Session share the implementation. Relay retains connection closure.

Keep the production accounting adapter and controlled test adapter at the Relay seam. Verify persistence with real PostgreSQL; do not invent an interchangeable datastore interface.

## CQRS and accounting persistence

Include lightweight, in-process CQRS in this refactor: separate complete command operations from consumer-oriented query models within the existing modules, using the same PostgreSQL database. Auth commands retain protected mutation/invalidation sequencing; Quota commands retain complete accounting operations. Portal/admin query models must not substitute stale presentation state for authoritative admission checks. No command bus, broker, separate database or asynchronous projection is required.

Correct durable totals with safe retries are sufficient. Weekly usage totals remain authoritative, supported by bounded settlement deduplication and uncertain-commit reconciliation state. Do not introduce an authoritative settlement event stream or a replay/rebuild contract. Event sourcing across Quota, Auth and Relay is outside this refactor's scope.

The specification must define stable settlement identity, atomic total updates with reconciliation evidence, and safe receipt retention or retirement so delayed retries cannot debit twice. Concrete receipt layout remains specification work; this decision does not require retaining settlement history. Preserve the agreed crash-loss allowance, deletion despite failed final settlement, and protection against late writes to deleted accounts.

Decision rationale: the user confirmed on 2026-10-24 that correct totals with safe retries suffice. See the [CQRS and event sourcing assessment](cqrs-event-sourcing-assessment.md) for the alternatives considered.

## Implementation obligations

- Accumulate reporter observations without awaiting a database round trip; bound pending state and background work.
- Separate in-flight batches from newer observations so acknowledgment cannot erase concurrent usage.
- Reconcile ambiguous commits without duplicate debit. Crash-loss tolerance does not permit double charging.
- Aggregate by account and week; never debit a new week for an earlier observation.
- Account-wide accounting must not let each connection spend an independent copy of the balance.
- New admission must have authoritative eligibility and usable accounting state.
- Preserve account mutation/admission ordering and one effective session per Peer ID. Stale asynchronous outcomes must not undo newer transitions.
- Revocation removes permission to continue; it does not erase already observed usage.
- Coordinate final settlement, retention and deletion so late writes cannot recreate an account or charge an unrelated re-registration.
- Failed usage persistence cannot be hidden by successful balance reads. With no pending usage, a successful balance refresh can establish accounting health.
- Validate the configured deadline as finite, positive and compatible with the cadence. Zero must not silently disable enforcement.
- Final settlement stays within existing lifecycle budgets.

These are correctness obligations derived from the agreed behavior, not additional product features.

## Verification through the interface

Preserve protocol-level authentication, renewal, replacement, revocation and attribution tests. Replace prepaid-policy assertions where behavior changes.

Cover shared usage across concurrent sessions; both charged endpoints; observations during in-flight settlement; duplicate retries and lost commit replies; full overrun and account-wide cutoff; configurable accounting failure deadlines; failed-write/successful-read combinations; crash loss versus durable totals; Monday-crossing batches; revocation racing admission; bounded graceful flush; deletion despite failed flush; retention and late writes after deletion; and configuration defaults/validation.

Use controlled time and scheduling for cadence tests, real PostgreSQL for settlement/retention consistency, and libp2p tests for physical-session outcomes. The six checks from the original review verify existing behavior, not this unimplemented design.

## Documentation and migration

The README, accepted specification I2/I5, funding receipts, quota tests, diagnostics, owner usage display, retained usage and recovery tooling describe prepaid Quota Committed. Reconcile these with implementation.

Recorded Relay Usage names the portion of Charged Relay Traffic recorded against the weekly allowance. Quota Committed remains the distinct prepaid measure. Preserve existing grant-local termination, exact-connection gating, single-session replacement, and preauthentication protocol allowances. This accounting change does not adopt an Auth-only protocol allowlist.

