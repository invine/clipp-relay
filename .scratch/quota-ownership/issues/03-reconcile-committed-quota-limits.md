# 03: Let Quota reconcile committed caps against admission

**What to build:** Administrator edits hand committed limits to Quota. Quota decides retirement for both limits against the exact live count, and Relay applies that decision before the edit acknowledges. Both orderings of the final admission race are verified.

**Blocked by:** [02: Move final admission and live counts into Quota](02-quota-admission-live-counts.md)

**Status:** resolved

- [x] Exercise a Quota Plan reassignment and an override through protected administrator HTTP. Auth hands off confirmed limits and receives a completion outcome without reading live counts to decide retirement. Quota enters the Relay reconciliation operation before taking its own state lock.
- [x] Cap wins. Pause a completed cap-five assessment before final admission, complete the edit to cap three, and release it. The fourth AUTH returns the temporary refusal and the three existing sessions retain permission.
- [x] Admission wins. Pause after final validation with the registry lock held and before publication. Let the edit commit and reach local reconciliation, then resume installation. Quota sees four sessions and all four lose permission before the edit acknowledges. Hold physical close independently.
- [x] At count equal to the cap, preserve existing sessions and refuse a fresh additional slot. If recorded plus unsettled bytes reach the new weekly allowance, Quota requests retirement while preserving the existing diagnostic classification.
- [x] Exercise ambiguous commits and request cancellation after commit through the protected HTTP seam. Preserve fail-closed invalidation and reconciliation, apply confirmed caps only, and release physical cleanup after Auth guards.
- [x] Use barriers at the existing Relay-to-Quota seam and assert on wire outcomes, live counts and permission. Run the captured-read regression from ticket 01 against the new policy path.

**Spec coverage:** I1, I3–I5, I8 · T3, T4, T7. See the [parent specification](../spec.md) and [decision record](../decisions.md).

## Resolution

Implemented on `codex/quota-ownership-03`, source implementation commit `9c699921e3d7143cd808e24e7206e05a20259e7b`; the ticket branch incorporated integration tip `81c8796854607dbeb50444f9beb40a5e2c6743e3` and integrated at `cee38fbc7a1aae3a1a6e8060465f9c9a4be05e85`. Fresh integrated `go test -count=1 ./internal/quota ./internal/auth ./internal/relay`, `go vet ./internal/quota ./internal/auth ./internal/relay`, and `git diff --check HEAD^ HEAD` passed. PostgreSQL-backed composed coverage was not run because no serving fixture was available. Final Standards/Spec review will cover the combined revision.
