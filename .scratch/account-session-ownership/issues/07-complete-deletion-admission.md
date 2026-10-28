# 07: Complete Relay Account Deletion admission inside Auth

**What to build:** An owner starts Relay Account Deletion through a complete Auth operation that returns a truthful admission result while preserving the existing independent finalization workflow.

**Blocked by:** 05

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_07`. Baseline: `781a970add2bbdabf4f1f9433330e0219bdbb291` on `codex/account-session-ownership-integration`.

- [ ] Migrate deletion admission to the shared private completion machinery while retaining its distinct authority, recent-authentication, capacity, and operation-identity rules.
- [ ] Own authoritative rechecks, protected durable admission, uncertain-outcome fencing, logical invalidation, and accounting-deletion coordination; physical cleanup stays in Relay and starts only after applicable protection releases.
- [ ] Distinguish refusal, uncertain admission, committed admission with incomplete invalidation, and admitted-but-not-finalized outcomes. Render once outside protected work with the existing operation location, cookies, retry guidance, and diagnostics.
- [ ] Keep Recovery Journal proof, object I/O outside SQL locks, bounded best-effort final settlement, retained current-week usage, and finalization/sealing under their current owners.
- [ ] Verify late authority loss, stale recent authentication, capacity refusal, ambiguous admission/rollback, request cancellation, and delayed close through existing composed seams.
- [ ] Run relevant real PostgreSQL deletion/retention tests proving failure to flush does not alone block finalization and late settlement cannot charge a fresh Pending registration. No journal format or proof-module extraction is included.

**Spec coverage:** I1–I4, I8–I10; T1–T3, T6–T7.


## Resolution evidence

- Source `d47147b9afaef0ed426a774d05b8bacf5fde24a4` on `codex/account-session-ownership-07`; integrated as `693bb5a94f3150c3b3ed62c0bf2f204302c13724`.
- Integrated `go test -count=1 ./internal/auth ./internal/relay` passed; focused deletion race tests and `git diff --check` passed. Source branch also passed Auth/Relay suites, focused races, vet, and build. PostgreSQL deletion/retention fixtures were unavailable (`CLIPP_TEST_SERVING_CONFIG` unset).
- Deletion admission now classifies outcomes and renders after protected work, retaining operation identity and existing Recovery Journal/accounting finalization ownership.
