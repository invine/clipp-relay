# 34: Try supported relay transports before failing

**What to build:** Try every eligible relay transport before declaring transport failure, retaining one effective session unless an accepted ADR changes that policy.

**Blocked by:** [12: Manage independent relays in Clipp](12-manage-independent-relays-in-clipp.md).

**Status:** claimed

Repository scope: Clipp.
Source: user instruction and clarification on 2026-10-05: try all transports before failing; persistent concurrency was not requested. [Accepted specification](../../managed-relay-service/spec.md), I4/I7, retains the single-session baseline. Architecture decision: accepted Clipp `docs/adr/0012-relay-transport-fallback-and-concurrent-sessions.md`.

## Contract and scope

Try supported advertised TCP, WSS and WebRTC Direct alternatives through the usable relay setup boundary; do not repeatedly select a route that connects but cannot complete transport-local setup while starving others. Preserve per-runtime eligibility, one host/Device Identity, expected-peer verification before credentials, policy refusal/backoff semantics and owned-resource cleanup. Different configurations remain independent. Accepted ADR-0012 requires one effective connection, with Electron preference TCP → WSS → WebRTC Direct and Android/extension WSS → WebRTC Direct. Persistent concurrency is not accepted.

## Acceptance criteria

- [ ] Demonstrate fallback across supported families, including transport-local failure after verified dial and exhaustion of all families.
- [ ] Preserve identity, credentials, admission limits and unrelated connections; retain one effective session under the baseline policy.
- [ ] Pass focused TDD checks, repository verification and independent Standards/Spec reviews.
- [ ] Accept the architectural decision, reconcile candidate code and verify integration before central resolution.

## Comments

- Centrally claimed 2026-10-05 after the listed predecessor was verified resolved. Fresh context and isolated worktree required. Coordinator owns canonical claims, integration and resolution. No push, publication, deployment or shared-cluster mutation; capacity ticket 33 remains deferred.

- User clarification 2026-10-05 supersedes the earlier concurrency interpretation and checklist. Concurrent candidate is preserved, not accepted as ticket completion. Claim remains centrally owned; further implementation/integration is paused for ADR-0012. No result is resolved.

- User accepted ADR-0012 on 2026-10-05 and requested lower overhead first. Fresh-context isolated implementation resumes from clean canonical main at Clipp `2727ad9` / relay `15a59a3`, with the accepted ADR recorded before code changes. Coordinator owns integration and resolution. Candidate branches and uncommitted work remain preserved.
