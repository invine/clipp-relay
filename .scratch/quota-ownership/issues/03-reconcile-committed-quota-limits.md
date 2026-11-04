# 03: Let Quota reconcile committed caps against admission

**What to build:** Administrator edits hand committed limits to Quota. Quota decides retirement for both limits against the exact live count, and Relay applies that decision before the edit acknowledges. Both orderings of the final admission race are verified.

**Blocked by:** [02: Move final admission and live counts into Quota](02-quota-admission-live-counts.md)

**Status:** ready-for-agent

- [ ] Exercise a Quota Plan reassignment and an override through protected administrator HTTP. Auth hands off confirmed limits and receives a completion outcome without reading live counts to decide retirement. Quota enters the Relay reconciliation operation before taking its own state lock.
- [ ] Cap wins. Pause a completed cap-five assessment before final admission, complete the edit to cap three, and release it. The fourth AUTH returns the temporary refusal and the three existing sessions retain permission.
- [ ] Admission wins. Pause after final validation with the registry lock held and before publication. Let the edit commit and reach local reconciliation, then resume installation. Quota sees four sessions and all four lose permission before the edit acknowledges. Hold physical close independently.
- [ ] At count equal to the cap, preserve existing sessions and refuse a fresh additional slot. If recorded plus unsettled bytes reach the new weekly allowance, Quota requests retirement while preserving the existing diagnostic classification.
- [ ] Exercise ambiguous commits and request cancellation after commit through the protected HTTP seam. Preserve fail-closed invalidation and reconciliation, apply confirmed caps only, and release physical cleanup after Auth guards.
- [ ] Use barriers at the existing Relay-to-Quota seam and assert on wire outcomes, live counts and permission. Run the captured-read regression from ticket 01 against the new policy path.

**Spec coverage:** I1, I3–I5, I8 · T3, T4, T7. See the [parent specification](../spec.md) and [decision record](../decisions.md).
