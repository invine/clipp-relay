# 01: Make grant invalidation terminal and manager-owned

**What to build:** Revoking a Login Grant removes its live session permission before acknowledgment, while the Relay Session module owns bounded physical cleanup and rejects reauthentication on a connection whose close is delayed. This is the first working path through the shared cleanup mechanism.

**Blocked by:** None (can start immediately)

**Status:** resolved

Claimed by: coordinator `/root`; implementation owner: `/root/01-terminal-grant-invalidation`.
Baseline: `e58b7289537de3df49f779802fe3879e771f936b` on `codex/account-session-ownership-integration`.

- [ ] Introduce explicit terminal closing semantics for tracked physical connections and check them at authentication entry and final application; same-connection renewal remains a nonterminal transition.
- [ ] Through the existing grant-revocation path, atomically retire pending attempts and invalidate only matching established sessions; preserve unrelated established sessions and the accepted conservative pending-attempt cancellation.
- [ ] Provide manager-owned bounded cleanup with idempotent ownership and no dropped closures or unbounded close goroutines. A blocked physical close does not hold the state lock or delay logical acknowledgment.
- [ ] For grant mutations, establish release of applicable Auth protection before physical close I/O without transferring connection handles or close callbacks to Auth. Preserve request-independent committed notification and refresh-replay behavior.
- [ ] Use controlled delays with the existing HTTP/libp2p seams to prove both invalidation/completion orderings, rejection of a new attempt while closing, and continued use of an unrelated session. Reproduce the suspected old gap before claiming a bug fix.
- [ ] Include pending closures in shutdown ownership and preserve current diagnostics. Existing account/quota and other closure paths remain functional during subsequent migrations; targeted tests and compilation pass.

**Spec coverage:** I4–I6, I8–I9; T1, T3–T4.


## Resolution evidence

- Integrated source commit `8d30a481820cb410621bee499d71c77cc8f1140f` into `codex/account-session-ownership-integration` as `704871bbce2f8be1bb731c0cdf8e2801bd150d9d`.
- Reproduced the delayed-close regression before the fix: a second authentication on the revoked physical connection succeeded while close was stalled.
- On the integrated revision: `go test -count=1 ./internal/auth ./internal/relay` passed; `go test -race ./internal/relay -run 'TestGrantRevocation|TestAccountInvalidation' -count=1` passed. The implementer also reports `go test -count=1 ./...`, `go vet ./...`, and `go build ./...` passed. The optional PostgreSQL fixture was skipped because `CLIPP_TEST_SERVING_CONFIG` was unset.
- Scope/decision: terminal authorization, manager-owned bounded physical cleanup, and release of Auth protection before physical close preserve Q1–Q5 and the one-session-per-Peer-ID contract.
