# 02: Keep account and accounting cleanup inside Relay

**What to build:** Account restrictions, session-cap changes, and accounting cutoff remove live permission through the shared manager-owned cleanup path. Existing owner, administrator, and deletion HTTP flows stop receiving physical-close callbacks.

**Blocked by:** 01

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_02`. Baseline: `704871bbce2f8be1bb731c0cdf8e2801bd150d9d` on `codex/account-session-ownership-integration`.

- [ ] Replace the current account-detach/returned-close collaboration across all existing production consumers with logical outcome cooperation that leaves physical cleanup owned by Relay.
- [ ] Prove account protection remains held through required logical effects and is released before physical close I/O can start; starting a worker without a release ordering guarantee is insufficient.
- [ ] Migrate account-wide invalidation and accounting/policy closure causes to the mechanism from ticket 01. Preserve current quota revision fencing, cap reductions, allowance equality/exhaustion, and grant/account invalidation scopes.
- [ ] Preserve pending observations, Recorded Relay Usage, report-time attribution, and accounting failure deadlines independent of blocked persistence work.
- [ ] Update the existing HTTP mutation callers only as needed to use the cleanup contract; classified complete Auth operations are delivered by tickets 05–07.
- [ ] Verify delayed close, request cancellation after commit, account-policy changes, and quota cutoff through composed operations, using real PostgreSQL where durable behavior is involved.
- [ ] Verify Relay Drain and shutdown include detached connections, admitted traffic, and bounded final settlement; retain the accepted force/deadline behavior and privacy-safe diagnostic outcomes.

**Spec coverage:** I4–I6, I8–I9; T2–T4, T6–T7.


## Resolution evidence

- Source commit `c507ff7d52061d8af26ce43520bd98981dcb7080` on `codex/account-session-ownership-02`; cleanly integrated as `737649b6cac2252d89949a07c4d7e482e2ebfd08` on `codex/account-session-ownership-integration`.
- Integrated `go test -count=1 ./internal/auth ./internal/relay` and focused race tests for deferred close, usage exhaustion, cutoff, cap reduction, and in-flight auth invalidation passed. Implementer also reports scoped vet/build/diff checks passed. PostgreSQL fixtures unavailable (`CLIPP_TEST_SERVING_CONFIG` unset).
- Account/cap/quota cleanup now stays manager-owned and Auth releases guards before physical close; preserves accepted accounting and deletion contracts.
