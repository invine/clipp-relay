# 06: Complete administrator account changes inside Auth

**What to build:** Administrator account actions use distinct complete Auth operations with truthful outcomes, preserving current policy and portal behavior through concurrent changes and uncertain commits.

**Blocked by:** 05

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_06`. Baseline: `781a970add2bbdabf4f1f9433330e0219bdbb291` on `codex/account-session-ownership-integration`.

- [ ] Migrate current account status, assignment, override, and recovery-review actions onto the private completion machinery from ticket 05 while keeping their action-specific authority and audit rules explicit.
- [ ] Preserve administrator/allowlist/Portal Session rechecks, consistent actor/target protection, current revisions, immutable-plan rules, and authoritative account state after waits.
- [ ] Return classified refusal/conflict, success, uncertain-commit, and incomplete-invalidation outcomes rather than mutating pointer results or rendering responses inside protected work.
- [ ] Retain immediate logical account/session-cap effects, conservative identity fencing, quota policy application without usage loss, and manager-owned cleanup after protection releases.
- [ ] Preserve existing HTTP forms, redirect destinations, status codes, retry behavior, cookies, and catalog-compatible diagnostics; Quota Plan creation/archival remain outside this extraction.
- [ ] Verify stale revisions, authority changes while waiting, known rollback, ambiguous suspension/commit, incomplete invalidation, cap changes, and pending usage through current HTTP/PostgreSQL/libp2p seams.

**Spec coverage:** I1–I4, I8–I10; T1–T4, T6–T7.


## Resolution evidence

- Source `f0243b72936985ed1525cb4d463fbef94e08d62b` on `codex/account-session-ownership-06`; integrated as `9b21a73c256f3bcf37aeb731e0784a97292ec5ab`.
- The controlled in-memory HTTP regression failed before the refactor because a blocked conflict response held account protection and passed afterward. Source branch Auth/Relay suites, focused race, vet, build, and diff checks passed. Integrated Auth/Relay suites and focused race passed. PostgreSQL-backed scenarios were unavailable (`CLIPP_TEST_SERVING_CONFIG` unset).
- Admin actions now return classified private outcomes and render after protection releases while retaining action-specific authority/audit rules.
