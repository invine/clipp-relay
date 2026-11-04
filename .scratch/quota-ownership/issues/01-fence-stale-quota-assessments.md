# 01: Reject stale Quota assessments

**What to build:** A cap edit that completes while PostgreSQL returns an earlier assessment cannot authorize a fourth Relay Session or trigger an obsolete weekly cutoff. This slice works through existing administrator HTTP and Relay Authentication paths.

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] Hold a cap-five result after the persistence adapter captures it, commit and locally apply cap three, then return the old result. The fourth Relay Authentication gets `temporarily_unavailable`, the three existing sessions remain permitted, and a fresh attempt gets `session_limit_exceeded`.
- [x] Repeat the read-over-edit ordering for first assessment with no local accounting entry. Show that a concurrent policy edit invalidates the captured result before it can authorize or update accounting state.
- [x] Preserve the existing protected wire case where the edit occurs before the read. Use deterministic barriers at the persistence seam, with real libp2p for admission and disposable PostgreSQL for the committed edit.
- [x] Hold an exhausted assessment across an allowance increase and hold an already-issued cutoff across policy recovery. In both cases valid sessions stay permitted.
- [x] Retain receipt reconciliation, monotonic confirmed usage, week rollover and independent accounting health coverage. Keep persistence I/O outside the state locks.

**Spec coverage:** I4–I5, I8 · T1, T2, T6. See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-01`, source commit `f5d081f2d0074b64f6c93825fc2672b39a59899f`; integrated unchanged into `codex/quota-ownership-integration` at the same commit. Baseline was integration `bdddb922cd87b9605b43819cfda59215c0017866`; source merged integration tip `11d6cee098cabf66e9bef8d0cd98b0a800106637` before handoff. Focused regressions and `GOCACHE=/private/tmp/quota-ticket-01-gocache go test -count=1 ./internal/quota ./internal/auth ./internal/relay` passed on the integrated revision. The integration worktree was clean. The final Standards/Spec review will cover this ticket again at the combined revision.
