# 08: Remove transitional coupling and qualify the combined behavior

**What to build:** The integrated relay delivers both selected architecture improvements with no leftover caller-owned coordination, and fresh evidence demonstrates the preserved external behavior.

**Blocked by:** 04, 06, 07

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_08`. Baseline: `693bb5a94f3150c3b3ed62c0bf2f204302c13724` on `codex/account-session-ownership-integration`.

- [ ] Remove transitional forwarding and compatibility paths after all migrated consumers are present; the session module is not embedded to expose state and does not refer back to the whole Server.
- [ ] Confirm that protocol/reporting/policy callers cannot manipulate private session or lease state, Auth/HTTP callers own no physical-close callback, and all session-ending causes use the manager-owned bounded cleanup mechanism.
- [ ] Verify all three distinct Auth operations use private completion machinery and classified outcomes while retaining operation-specific rules and response behavior; no generic public callback runner remains.
- [ ] Audit spec I1–I10 and T1–T8 against the integrated result and retain evidence for both race orderings, delayed physical closure, cleanup after guard release, protocol compatibility, quota preservation, and deletion sealing.
- [ ] Run routine and race verification plus applicable disposable PostgreSQL qualification at the integrated revision; report skipped/unavailable fixtures explicitly rather than treating them as passed.
- [ ] Complete Standards and Spec reviews, fix findings, and update relevant ownership/contract documentation if needed. No glossary or product-policy change is presumed.
- [ ] Record exact revision and verification outcomes, remaining independent acceptance gates, and transition removal. Do not claim hosted CI, provider/native/live qualification, publication, or deployment from local checks.

**Spec coverage:** I1–I10; T1–T8.


## Resolution evidence

- Ticket branch `codex/account-session-ownership-08` commit `e67e49cc0cedc7441190f85c27adc51ab112ee7d` integrated as `a1cb9f95c8c7a4ddec1e699cdf191747da7688a4`. Final review fix `b1afdb3aa5bf813c242ec496ba374cd10fe5353e` and publication disconnect-test fix `a2368d11822bf386a856deecee42a73861aeb762` are also integrated.
- Final integrated checks on `a2368d11822bf386a856deecee42a73861aeb762`: `bash scripts/verify.sh routine` passed; `CGO_ENABLED=1 bash scripts/verify.sh race` passed; `git diff --check` passed. The stale-peer lifecycle test passed after waiting for server-initiated disconnect before a fresh redial.
- Final Standards review: no documented-standard violations or remaining baseline smells. Final Spec review: shared private `completeAccountMutation` serves all three distinct operations; no other missing behavior, scope creep, or incorrect behavior found.
- PostgreSQL qualification remains unavailable: `CLIPP_TEST_SERVING_CONFIG` and `CLIPP_TEST_MIGRATION_CONFIG` were unset. Routine/race profiles explicitly left PostgreSQL, native image, provider, hosted CI, and live cluster/device qualification not run.
- Transitional session forwarding and manager-to-Server coupling are removed. Each Auth mutation retains its own transaction, authority, result, and response rules; shared private completion owns guard ordering and post-guard physical cleanup.
