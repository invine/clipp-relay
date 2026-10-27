# 03: Own authentication and traffic operations in the session module

**What to build:** Relay Authentication, renewal, replacement, HOP/STOP permission, traffic attribution, and live session observations use complete session-module operations instead of reading its indexes and lock.

**Blocked by:** 01

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_03`. Baseline: `704871bbce2f8be1bb731c0cdf8e2801bd150d9d` on `codex/account-session-ownership-integration`.

- [ ] Move authentication attempt lifecycle, exact-connection permission, session indexes/counts, and timer-driven transitions behind the session module’s interface. Callers cannot manipulate these facts directly.
- [ ] Use explicit authority, accounting, diagnostic, and connection collaborators for migrated operations. Keep Auth credential validation connection-independent and Quota unaware of Login Grants.
- [ ] Migrate expiry, preauthentication expiry, failed-initial-authentication closure, and replacement closure to manager-owned lifecycle cleanup, preserving required response-before-close behavior.
- [ ] Preserve fixed expiry, failed renewal, successful renewal/lease continuity, one effective session per Peer ID, cross-account replacement at capacity, failed replacement, and current quota/cap checks.
- [ ] Route byte reporting and live-count observations through the module while preserving current-Peer-ID attribution, unattributed traffic, separately sampled portal counts, and already-admitted bytes.
- [ ] Retain real-wire authentication, transport, HOP/STOP, reporter, and accounting coverage. Deterministically test superseded attempts, late validation/eligibility results, disconnect, and stale expiry/cleanup callbacks.
- [ ] Keep the checkout compiling and affected tests passing. Remaining Rendezvous state access is explicitly reserved for ticket 04; any transitional forwarding is removed in ticket 08.

**Spec coverage:** I5–I6, I8–I9; T1, T3–T4, T6.


## Resolution evidence

- Source commits `8ceaf13` and `27c79108bf8dd8fd88af399bf643a9ef59a94542` on `codex/account-session-ownership-03`; integrated as `b1c098bc16839303956e1a995923d4428e609b8d`. Ticket 03 also merged ticket 02 at `737649b` before handoff.
- Integrated `go test -count=1 ./internal/auth ./internal/relay` and focused lifecycle race checks passed. `go vet ./...`, `go build ./...`, and `git diff --check` passed on the source branch after the fixture compatibility update. Optional PostgreSQL fixtures were unavailable.
- Session authentication, traffic, permission, closure and live-count operations now route through the manager; one fixture signature was updated for ticket 02's callback contract.
