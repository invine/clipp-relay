# 01: Reject stale Quota assessments

**What to build:** A cap edit that completes while PostgreSQL returns an earlier assessment cannot authorize a fourth Relay Session or trigger an obsolete weekly cutoff. This slice works through existing administrator HTTP and Relay Authentication paths.

**Blocked by:** None (can start immediately)

**Status:** claimed

**Claimed by:** quota-ownership-coordinator (2026-11-05)

- [ ] Hold a cap-five result after the persistence adapter captures it, commit and locally apply cap three, then return the old result. The fourth Relay Authentication gets `temporarily_unavailable`, the three existing sessions remain permitted, and a fresh attempt gets `session_limit_exceeded`.
- [ ] Repeat the read-over-edit ordering for first assessment with no local accounting entry. Show that a concurrent policy edit invalidates the captured result before it can authorize or update accounting state.
- [ ] Preserve the existing protected wire case where the edit occurs before the read. Use deterministic barriers at the persistence seam, with real libp2p for admission and disposable PostgreSQL for the committed edit.
- [ ] Hold an exhausted assessment across an allowance increase and hold an already-issued cutoff across policy recovery. In both cases valid sessions stay permitted.
- [ ] Retain receipt reconciliation, monotonic confirmed usage, week rollover and independent accounting health coverage. Keep persistence I/O outside the state locks.

**Spec coverage:** I4–I5, I8 · T1, T2, T6. See the [parent specification](../spec.md) and [decision record](../decisions.md).
