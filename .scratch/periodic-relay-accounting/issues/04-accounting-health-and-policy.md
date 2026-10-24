# 04: Enforce configurable accounting health and policy changes

**What to build:** Let operators configure bounded accounting failure tolerance, and ensure account-policy changes affect active sessions immediately without losing already observed usage or treating uncertainty as exhaustion.

**Blocked by:** 03 — Account for live traffic periodically.

**Status:** resolved

Parent: [Relay, Quota and Auth specification](../spec.md), especially I5, I6 and I8. User stories: 15–25, 29, 30. Breakdown approved 2026-10-24.

- [x] Expose the accounting failure deadline through supported operator configuration, defaulting to three seconds and taking effect on restart. Validate a finite positive value at least as long as the one-second cadence; zero cannot disable enforcement.
- [x] Staleness uses monotonic elapsed time and enforcement runs independently of blocked persistence. Preserve database work budgets and conservative clock handling; clock regression cannot extend permission.
- [x] With pending or uncertain usage, successful reads do not refresh health. Success for one batch/week cannot conceal an older unresolved batch. With no pending usage, an authoritative balance read can refresh health without an empty debit.
- [x] At the deadline, all affected account sessions close and new admission fails until authoritative accounting eligibility is healthy. Distinguish proven exhaustion from temporary persistence, reconciliation and clock failures in protocol outcomes and existing diagnostics.
- [x] Quota cap edits apply to recorded plus pending consumption without reset or refund. Equality is exhausted when no positive allowance remains; the prepaid-credit equality exception is removed.
- [x] Cap increases/reductions, session-cap changes, suspension and account-wide revocation use protected immediate local effects rather than waiting for a settlement tick. A reduced session cap below the live count closes all sessions and constrains reconnection.
- [x] Revocation/generation changes do not erase valid pending observations. Preserve grant-local termination and fixed-expiry behavior. Late settlement or eligibility results cannot undo a newer invalidation.
- [x] Controlled-time tests cover valid/invalid configuration, failed-write/successful-read combinations, idle refresh, a hung worker, independent cutoff and recovery. Composed HTTP/libp2p/PostgreSQL tests cover quota edits, cap equality and policy mutations racing settlement/admission.
- [x] Configuration propagation, operator guidance and diagnostic wording are updated with this behavior. Tests remain outcome-based and use production composition; serving activation remains with ticket 08.

## Implementation notes

This slice completes the configurable behavior around ticket 03's default cutoff. It does not introduce a fixed byte-overrun promise, asynchronous authorization projections or a new analytics surface.

## Answer

Configurable validated accounting deadline and immediate protected policy effects implemented. Source `ed79b585392eb74a9f89a692d1972c2f759909ef`; integration `c426d55d7a14e7071c1df13741bfa8b33b438dd9`. Combined05 PG18/race, config/chart suites and vet passed; integrated health/policy/Auth/config checks passed. Focused Standards/Spec assessment found no issues. Artifacts `/private/tmp/clipp-periodic-04-handoff.md`, `/private/tmp/clipp-periodic-04-integration.md`. Combined readiness checks include invalidation fence; final08 profiles remain required.
