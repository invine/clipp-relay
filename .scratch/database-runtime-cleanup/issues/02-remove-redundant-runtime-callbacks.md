# 02: Remove redundant runtime callbacks and qualify the cleanup

**What to build:** A smaller exported database runtime interface that retains admission control and all existing resource and diagnostic guarantees. PostgreSQL adapter callers keep `StartUnit`; the redundant exported `WithUnit` and `Unit` callbacks disappear, with their necessary behavior retained inside the runtime. The completed change has fresh behavioral and real PostgreSQL verification evidence.

**Blocked by:** 01: Verify admission and database behavior through surviving interfaces.

**Status:** ready-for-agent

Parent: [database runtime cleanup specification](../spec.md). Owning repository: relay. Coverage: all Implementation Decisions, Testing Decisions and Out of Scope constraints. Ticket 01 supplies passing coverage through the surviving interfaces before this ticket changes production implementation.

- [ ] Remove exported `WithUnit` and `Unit` without compatibility aliases or equivalent exported generic callbacks under new names. Keep `StartUnit` and its contract unchanged, alongside existing SQL execution and transaction methods.
- [ ] Retain private helpers where they concentrate lifecycle behavior, or fold the single-caller behavior into `Exec` where simpler. Do not require a particular helper count, duplicate lifecycle policy across callers, rename unrelated types or introduce new modules or interfaces.
- [ ] Preserve the shared 64-operation admission budget, immediate overflow refusal, three-second ordinary operation deadline, 500 ms pool-acquisition limit, eight-connection serving pool and existing statement, lock and idle limits. Preserve admission before application guards where currently required and defer physical connection acquisition until database work begins.
- [ ] Preserve nested slot/deadline reuse, idempotent owner release, earlier parent cancellation and resource cleanup on success, failure and panic unwinding. Preserve the post-work deadline check and error precedence: an expired context replaces an otherwise successful result, while an existing work error remains authoritative. Do not return early through detached work that still owns SQL resources.
- [ ] Preserve row, rows and transaction resource lifetimes, statement-context handling, commit/rollback behavior and uncertain-commit classification. Add no transaction retries and do not treat cancellation as proof of rollback.
- [ ] Preserve admission, acquisition, SQL and deadline diagnostic observations on surviving execution paths, including policy neutrality, caller-cancellation handling, no-row classification and privacy. Admission without SQL must not establish recovery.
- [ ] Run the behavioral coverage prepared in ticket 01 against the final implementation. Review the removed callback bodies and their callers to account for every preserved lifecycle and diagnostic behavior; supplement coverage only where a genuine gap remains at an agreed existing seam.
- [ ] Run the repository's routine and race verification profiles and relevant tests against disposable supported PostgreSQL 17 and 18 fixtures using existing qualification tooling. Record exact tested revisions, commands, versions and results; distinguish failures, skipped tests and unavailable checks. Required database checks must actually execute before claiming qualification.
- [ ] Complete structural, Standards and Spec review of the final revision. Confirm no remaining production or test callers of the removed methods, no replacement exported callbacks and no new driver dependencies in functional modules. Use review and compilation rather than source-text tests. Fix in-scope findings and renew affected verification.
- [ ] Keep schema, migrations, credentials, privileges, Relay Quota accounting, Relay Session policy and client runtimes unchanged. Do not change admission policy, capacity, fairness, deadlines, retry rules or diagnostics design; record unrelated findings separately.
- [ ] Record completion evidence and any remaining external checks honestly. Local implementation and verification do not establish hosted CI, publication, deployment or live acceptance, and those actions are outside this ticket's scope.

## Comments

- 2026-10-30: The user approved this two-ticket breakdown and explicitly requested publication. The dependency on ticket 01 protects behavioral coverage before exported callback removal. Publication does not claim or start implementation.
