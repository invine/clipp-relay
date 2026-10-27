# Complete Relay Account mutations and private Relay Session ownership

Status: ready-for-agent

Owner: relay.  
Date: 2026-10-28 (Asia/Dubai).  
Implementation: not started.  
Decision source: [accepted decisions Q1–Q5 and testing confirmation](decisions.md).  
Baseline: `e58b7289537de3df49f779802fe3879e771f936b`.  
Scope: architecture-review candidates 1 and 2, combined because mutation completion and connection cleanup share an ownership contract.

## Problem Statement

The maintainer cannot safely change Relay Account access behavior by understanding one complete operation. Administrator account changes, Account-wide Revocation, and Relay Account Deletion admission each coordinate persistence admission, logical protection, authoritative rechecks, commit uncertainty, fencing, invalidation, physical cleanup, and HTTP responses. Repeating that knowledge across callers makes an omitted or reordered step difficult to detect.

Relay Session ownership has already been centralized conceptually, but protocol handlers and traffic reporting still reach into the manager's locks, indexes, and lease state. The manager refers back to the whole Relay Server, and Auth still receives connection-close callbacks. This makes the interface larger than the domain operation and weakens locality across authentication, replacement, revocation, Rendezvous, and accounting cutoff.

Source inspection also found that physical closure is handled through several paths and that detached connections are not represented by an explicit terminal per-connection closing state. This is a concurrency concern to reproduce through controlled tests, not a claim of an observed production failure. The accepted contract already requires a closing connection to remain unauthorized even while its physical close is delayed.

## Solution

Deepen the existing Auth and Relay Session modules. Keep distinct complete Auth operations for administrator account changes, Account-wide Revocation, and deletion admission, sharing private completion machinery. Each operation returns a classified outcome after its required logical effects; the HTTP adapter renders that outcome without coordinating locks, transactions, or connection cleanup.

Make the existing Relay Session module privately own connections, authentication attempts, indexes, timers, reservations, Rendezvous Leases, and physical cleanup. Protocol and traffic-reporting adapters cross a complete-operation seam. The module owns all semantic Rendezvous checks, including ownership rechecks around request reading and signature verification.

Depth comes from hiding ordering and lifecycle obligations behind the interface. Locality concentrates those obligations with their owner; leverage lets every affected mutation and protocol adapter reuse them. Preserve the existing persistence adapter, Quota ownership, protocol behavior, and single-session admission contract.

## User Stories

1. As a maintainer, I want administrator account changes to be complete Auth operations, so that HTTP handlers do not reproduce their completion sequence.
2. As a maintainer, I want Account-wide Revocation to be a complete Auth operation, so that Sign out everywhere shares the same completion guarantees.
3. As a maintainer, I want deletion admission to be a complete Auth operation, so that starting deletion does not require HTTP-owned coordination.
4. As a maintainer, I want those three operations to remain distinct, so that their different authority, audit, and completion rules stay explicit.
5. As a maintainer, I want shared completion machinery to remain private to Auth, so that callers do not learn a generic guard or transaction protocol.
6. As a maintainer, I want mutation outcomes classified explicitly, so that the caller does not infer completion from combinations of booleans and callbacks.
7. As a Relay Account owner, I want a refused action distinguished from an unavailable dependency, so that the portal explains the actual outcome.
8. As an administrator, I want stale revisions reported as conflicts, so that an older form cannot overwrite newer policy.
9. As a Relay Account owner, I want an uncertain commit reported as uncertain, so that a lost reply is never presented as a proven rollback or success.
10. As a Relay Account owner, I want incomplete logical invalidation reported separately from a committed mutation, so that completion claims remain truthful.
11. As a Relay Account owner, I want deletion admission distinguished from completed deletion, so that a pending operation is never described as erased data.
12. As a maintainer, I want HTTP rendering outside protected mutation work, so that presentation does not prolong logical protection or own completion decisions.
13. As an operator, I want storage work admitted before waiting for logical protection, so that overload limits remain effective.
14. As an operator, I want physical transactions acquired only when needed, so that logical waits do not occupy scarce database connections.
15. As an administrator, I want authority rechecked after waits, so that an earlier observation cannot authorize a later forbidden action.
16. As a Relay Account owner, I want Portal Session and recent-authentication rules preserved, so that refactoring does not weaken protected actions.
17. As an administrator, I want mutation and required audit persistence to remain atomic, so that one cannot succeed without the other.
18. As a Relay Account owner, I want successful revocation to include logical session invalidation, so that the response does not precede permission removal.
19. As a Relay Account owner, I want committed invalidation to survive cancellation of the initiating request, so that closing a browser cannot preserve revoked access.
20. As a maintainer, I want conservative fencing retained for uncertain mutations, so that stale authority cannot regain access during reconciliation.
21. As an operator, I want physical close work outside logical protection and the session lock, so that a slow transport cannot hold protected state changes.
22. As a maintainer, I want the Relay Session module to own physical cleanup, so that Auth and HTTP adapters never receive connection-close responsibility.
23. As an operator, I want all session-ending causes to share bounded cleanup ownership, so that closures cannot be lost between separate mechanisms.
24. As an operator, I want slow physical closure independent of logical invalidation completion, so that one socket cannot delay permission removal for other sessions.
25. As a maintainer, I want connection state private to the session module, so that callers cannot bypass its invariants through shared maps or locks.
26. As a maintainer, I want the session module independent of the whole Relay Server, so that its dependencies are explicit and limited.
27. As a Clipp user, I want each connection tracked before authentication, so that merely opening a transport grants no relay permission.
28. As a Clipp user, I want a closing connection unable to authenticate again, so that a delayed socket close cannot restore access.
29. As a Clipp user, I want late authentication results rejected after invalidation, so that stale work cannot revive a connection.
30. As a Clipp user, I want older attempts unable to overwrite newer attempts, so that retrying authentication has a defined result.
31. As a Clipp user, I want failed renewal to preserve an otherwise valid session until its original expiry, so that transient failure does not unnecessarily disconnect me.
32. As a Clipp user, I want same-connection renewal to preserve valid reservations and Rendezvous Leases, so that renewal does not interrupt reachability.
33. As a Clipp user, I want same-Peer-ID replacement to retain one effective Relay Session, so that reconnecting preserves the accepted admission contract.
34. As a Clipp user, I want failed replacement to preserve the prior usable session, so that an unsuccessful attempt cannot destroy working connectivity.
35. As an operator, I want account and global session caps enforced during replacement, so that reconnects do not bypass capacity limits.
36. As a Relay Account owner, I want revoking one Login Grant to invalidate only its established sessions, so that other grants retain their valid connections.
37. As a Relay Account owner, I want Account-wide Revocation and access restrictions to invalidate all affected account sessions, so that their account-wide scope remains intact.
38. As a maintainer, I want pending attempts retired consistently during invalidation, so that correctness covers the interval after validation returns but before application.
39. As an operator, I want expiry enforced locally, so that delayed background work cannot extend a Relay Session indefinitely.
40. As a Clipp user, I want protected protocol operations denied on the wrong physical connection, so that authentication is never inherited from another connection.
41. As a maintainer, I want protocol adapters limited to framing and response encoding, so that session and lease rules have one owner.
42. As a Clipp user, I want Rendezvous ownership rechecked after request input is read, so that a request cannot act on a superseded session.
43. As a Clipp user, I want Rendezvous ownership and reservation validity rechecked after signature verification, so that work outside the lock cannot install a stale lease.
44. As a Clipp user, I want invalid Signed Peer Records rejected, so that encapsulation preserves record authenticity and existing bounds.
45. As a Clipp user, I want both Rendezvous protocol versions to retain their framing and response behavior, so that existing clients remain compatible.
46. As a maintainer, I want no mutable session snapshot exposed to protocol adapters, so that callers cannot mistake an earlier observation for continuing permission.
47. As a Relay Account owner, I want quota policy changes to preserve pending observations and Recorded Relay Usage, so that access changes do not reset consumption.
48. As an operator, I want accounting cutoff independent of blocked persistence work, so that the accepted failure deadline remains meaningful.
49. As a maintainer, I want Quota to remain unaware of Login Grant state, so that accounting and authorization retain their separate responsibilities.
50. As an operator, I want traffic reporting to preserve report-time Peer ID attribution, so that this refactor does not silently change Charged Relay Traffic accounting.
51. As a Relay Account owner, I want admitted traffic and buffered bytes treated under the existing invalidation contract, so that logical invalidation is not misrepresented as instantaneous byte cessation.
52. As an operator, I want Relay Drain to reject new work while allowing its bounded finish period, so that cleanup changes preserve graceful shutdown.
53. As an operator, I want shutdown to account for already-detached connections, so that pending cleanup cannot leak across termination.
54. As an owner deleting my Relay Account, I want Recovery Journal proof and best-effort final accounting retained, so that simplifying admission cannot weaken finalization safety.
55. As an owner registering again after deletion, I want late accounting unable to charge the replacement account, so that the old operation cannot alter my fresh registration.
56. As a maintainer, I want tests through existing HTTP and real libp2p operations, so that verification survives private implementation changes.
57. As a maintainer, I want controlled adapters for delayed results and closes, so that concurrency tests prove both orderings without relying on sleeps.
58. As a maintainer, I want real PostgreSQL verification of storage guarantees, so that controlled adapters do not stand in for transaction correctness.
59. As an operator, I want bounded and privacy-preserving diagnostics retained, so that observing the refactor does not expose account or credential material.
60. As a maintainer, I want readiness for implementation separated from execution evidence, so that a completed spec is not mistaken for tested code.

## Implementation Decisions

### I1. Scope and existing ownership

- Deepen the existing Auth and Relay Session modules in the relay repository. Keep PostgreSQL persistence, Quota, the native issuer, and the Recovery Journal with their current owners.
- Auth extraction covers administrator account changes, Account-wide Revocation, and deletion admission. Administrator account changes include their current status, assignment, override, and recovery-review actions; each retains its existing policy.
- Quota Plan creation/archival, credential issuance/refresh, and deletion finalization are not additional mutation extractions. Their existing collaboration with the changed session interface must still work.
- Preserve one process, existing schema, current client behavior, and one effective authenticated Relay Session per Peer ID. No new durable Device Identity or Relay Session registry is introduced.
- Names, private helper placement, concrete Go declarations, and worker counts remain implementation choices within these requirements.

### I2. Complete Auth operations

- Keep distinct complete operations for the three mutation paths, backed by shared private completion machinery. Operation-specific authority, mutation, audit, and response facts stay explicit.
- HTTP adapters perform bounded request decoding and response rendering. Auth owns authoritative identity/session/administrator rechecks, operation admission, logical protection, transaction completion, uncertainty handling, fencing, and required local effects.
- Render only after protected work has completed or failed and released its resources. Auth does not write HTTP responses inside protected callbacks or return physical-close work to the caller.
- Do not replace the repeated choreography with a public generic callback runner or a forwarding coordinator. The interface is the complete operation and its outcome, including its ordering guarantees.

### I3. Classified outcomes

- Distinguish at least refusal/conflict, completed mutation, uncertain commit, committed mutation with incomplete logical invalidation, and deletion admitted but not finalized.
- Preserve enough operation-specific information to render the existing response truthfully. A deletion admission may be durably accepted while finalization remains pending; an uncertain mutation may still require conservative local effects.
- Preserve current HTTP status, redirect destination, cookie, retry guidance, diagnostics, and operation-identifier behavior. A single undifferentiated error must not erase durable-versus-local completion facts.
- Classifications are internal operation outcomes, not new public wire formats. Exact type representation is an implementation choice.
- An expired/canceled operation context after a commit must not be treated as proof that the mutation rolled back. Reconcile through the existing operation-specific persistence rules; do not add blind generic retries.

### I4. Protection, commit, and local-effect ordering

- Preserve storage work admission before logical waits, established identity/account ordering, and authoritative rechecks after waits. Do not reserve a physical transaction while waiting for logical protection.
- Keep required audit persistence atomic with its mutation. The persistence adapter retains transaction mechanics, resource limits, and uncertain-commit classification; functional policy remains in Auth.
- Retain logical protection through the required fencing and local invalidation steps. Preserve conservative behavior on uncertain outcomes and the existing fallback delivery semantics.
- Successful revocation includes the session module processing logical invalidation, not merely accepting an unprocessed notification. Delivery after a durable change cannot depend on the initiating HTTP request remaining alive.
- Physical connection-close I/O occurs after the applicable logical protection has been released and outside the session lock. Internal cooperation must establish this ordering; immediately waking a cleanup worker while protection is still held is not sufficient.
- That cooperation must not pass a connection handle or physical-close callback to Auth or its HTTP adapters. The Relay Session module retains cleanup ownership; Auth retains responsibility for completing its protected logical mutation.

### I5. Private Relay Session module

- Keep the existing module inside Relay, with private connection tracking, authenticated indexes, pending attempts, expiry/preauthentication timers, reservation ownership, Rendezvous Leases, and cleanup state.
- Remove shared access to its mutable maps and lock. Do not embed the module merely to promote private state into the surrounding Server, and remove its dependency on the whole Server.
- Provide explicit narrow collaborators for existing authority, accounting, diagnostics, and connection effects. Keep real adapters that already vary; do not introduce hypothetical interchangeability solely to make private helpers mockable.
- Session and lease state share the synchronization needed for atomic ownership changes. Avoid independently synchronized registries that require callers to reassemble those guarantees.
- Protocol handlers, traffic reporting, policy invalidation, and live-count observations use complete operations or bounded observations through the module's interface.

### I6. Terminal closing and bounded cleanup

- Represent unauthenticated, authenticated, closing, and closed lifecycle semantics explicitly. Closing is terminal for permission on that physical connection.
- Atomically retire affected pending attempts and remove affected established permission under the session module's synchronization. Reject both late completions and new attempts on a closing connection.
- Consolidate revocation, policy/quota cutoff, expiry, replacement, and shutdown cleanup under one manager-owned mechanism. Preserve cause-specific behavior and existing diagnostic reasons.
- Complete logical invalidation before returning its acknowledgment; physical close may finish afterward. A slow close must not hold the state lock or make logical invalidation wait for every socket.
- Keep pending cleanup and worker activity bounded by the existing connection/resource model. Never silently drop a detached connection because a work queue is full, and do not create an unbounded goroutine per close request.
- Preserve idempotent cleanup and exact ownership: late timer, disconnect, or close completion must not remove a replacement session or its lease.
- Same-connection renewal is not terminal closure. Failed renewal preserves an otherwise valid session and original expiry; successful renewal preserves valid reservation and lease ownership.
- Shutdown includes already-detached connections and pending cleanup, retaining the accepted bounded drain/force behavior and accounting-flush budget. Exact worker organization is not prescribed.

### I7. Complete Rendezvous semantics

- The protocol adapter owns bounded wire reading/parsing, protocol-version encoding, and stream response handling. The session module owns the semantic request lifetime and its authorization/ownership rules.
- Preserve early permission and rate checks before expensive request processing, plus the original request's ownership association across request reading. A later successful authentication must not retroactively authorize a request admitted under an obsolete owner.
- The module owns session/reservation rechecks after request input and Signed Peer Record verification. Do not expose a mutable session pointer or reusable authenticated boolean as continuing authority.
- Perform expensive signature/address work outside the state lock, then recheck the exact session, reservation, expiry, and drain conditions before applying changes.
- Preserve register, lookup, and unregister behavior, current lease lifetime rules, same-connection renewal transfer, and stale-owner rejection.
- Preserve both existing protocol versions, size/resource/rate bounds, half-close compatibility, extra-input handling, and response meanings. This is an ownership refactor, not a wire-protocol revision.

### I8. Auth, Quota, and Relay collaboration

- Auth remains connection-independent for credential validation and produces the existing Principal facts. It cancels its own pending validation; the session module independently retires its pending attempts through result application.
- Preserve grant-local versus account-wide invalidation. The accepted conservative cancellation of unrelated pending authentication remains allowed; unrelated established sessions stay usable.
- Quota retains accounting eligibility, policy application, observations, Recorded Relay Usage, settlement/reconciliation, and independent health cutoffs. Login Grant invalidation is not an accounting failure and cannot discard usage.
- Live session-cap effects belong to Relay; quota allowance effects belong to Quota; Auth owns their required cooperation during a complete account mutation. Encapsulation must not weaken current cap checks or permit stale asynchronous outcomes to overwrite newer policy.
- Preserve report-time Peer ID attribution and unattributed observations. This refactor does not infer the original physical connection from the stock byte reporter.

### I9. Compatibility and deletion guarantees

- Preserve exact-connection Relay Authentication, HOP/STOP permission, account/global capacity, cross-account replacement, fixed expiry, and established transport fallback.
- Logical invalidation denies new protected work; already-admitted streams and buffered traffic retain the existing limits. Do not promise instantaneous cessation of bytes or stronger atomicity at database commit.
- Keep deletion admission separate from finalization. Preserve immediate access removal, required Recovery Journal proof, bounded best-effort final settlement, Retained Quota Usage, and sealing against late writes to deleted/replacement accounts.
- Keep diagnostics bounded, catalog-compatible, and privacy-preserving. Preserve separately sampled portal live counts and current status semantics.

### I10. Depth and completion criteria

- Callers must no longer know the private protection/commit/fence/invalidate/cleanup sequence or directly manipulate session/lease state.
- The deletion test is behavioral: removing a deep operation would force its invariants back into multiple callers. Moving methods between files or adding forwarding operations without removing caller knowledge is insufficient.
- Prefer establishing private session ownership and manager-owned cleanup before adapting Auth completion to that seam. This sequencing is implementation guidance, not a separate product feature or approval gate.
- Remove superseded coordination and test-only paths when the composed behavior is covered. Preserve relevant behavior tests instead of duplicating implementation-shaped tests for every helper.

## Testing Decisions

### T1. Confirmed test seams

The user explicitly accepted the existing composed HTTP and real libp2p operations as the main test surface, controlled adapters for deterministic races and delayed closes, and real PostgreSQL for storage guarantees on 2026-10-28. Prefer the highest existing seam that can observe the requirement. The interface is the test surface.

Use normal production composition for Auth, Relay Session, and Quota cooperation. Focused controlled adapters may arrange lost acknowledgments, delayed validation, delayed closure, and scheduling without making internal map shape or helper-call counts the contract. Do not add a public interface solely to test an extraction.

### T2. Mutation outcomes and ordering

- Exercise all three complete operations through their existing HTTP paths, including normal completion, refusal, stale revision, authority changes while waiting, work-admission failure, known rollback, and uncertain commit.
- Verify committed mutation with incomplete invalidation remains distinguishable from uncertainty and success. Verify deletion admission reports a pending operation rather than completed deletion.
- Verify responses are produced once and preserve status, cookies, redirects, retry guidance, and diagnostic outcomes.
- Verify logical effects precede successful mutation responses and survive request cancellation after commit.
- Prior art: existing administrator-consistency tests for ambiguous account commit, uncertain suspension, incomplete invalidation, and owner/deletion tests for late authority rechecks and rollback.

### T3. Closing is terminal

- Arrange a physical close that cannot complete yet. Verify logical permission is already gone, an unrelated established session remains usable, and affected new authentication/renewal cannot restore permission.
- Release a previously successful validation or eligibility result after closing begins and prove it cannot install a session.
- Prove cleanup I/O cannot start while applicable logical protection remains held, without delegating physical-close work to the HTTP caller.
- Release cleanup and verify completion remains idempotent, including disconnect/expiry callbacks and shutdown with pending cleanup.
- Reproduce the suspected detached-connection gap as a deterministic regression before claiming a bug fix. If the existing composition already prevents an interleaving, document that evidence and still verify the explicit terminal-state contract after refactoring.

### T4. Authentication, invalidation, and replacement

- Exercise both completion-before-invalidation and invalidation-before-completion, including the gap after Auth has returned and while initial quota assessment waits.
- Cover newer attempts superseding older attempts, disconnect during validation, failed renewal preserving original permission, and expiry while work is delayed.
- Cover grant-specific invalidation, Account-wide Revocation, account restriction, and refresh-replay invalidation through the existing composed paths.
- Preserve same-Peer-ID replacement, cross-account replacement at capacity, failed replacement, current cap enforcement, and the rule that cleanup belongs to the exact old session.
- Prior art: existing real-wire authentication attempt, grant revocation, current quota limit, protected mutation, and replacement tests.

### T5. Rendezvous operation lifetime

- Hold request input or signature verification at a deterministic barrier, then invalidate, replace, renew, or expire the owning session before resuming. Verify outcomes follow the existing exact-owner rules.
- Verify stale work cannot create or remove a replacement owner's lease, and reservation expiry or loss during verification prevents registration.
- Preserve valid same-connection renewal and lease transfer, existing lookup misses/unregister outcomes, and drain-specific behavior.
- Retain real-wire coverage for both versions, bounded framing, extra input, half-close compatibility, record validation, and resource/rate refusals.
- Prior art: the current Rendezvous wire suite and transport/session ownership tests. Do not replace these with tests that directly mutate private indexes.

### T6. Accounting and traffic

- Preserve account-wide exhaustion/cutoff, accounting failure deadlines independent of blocked database work, policy edits with pending usage, and suspension/revocation without usage loss.
- Retain real reporter coverage for current-peer attribution and unattributed tail bytes; do not assert exact historical-connection attribution.
- Retain actual forwarded-traffic tests during drain and bounded final settlement, distinguishing logical permission loss from already-admitted bytes.
- Prior art: composed observed-accounting tests, reporter wire tests, and real-circuit drain/final-flush tests.

### T7. Deletion and persistence

- Use the existing real PostgreSQL fixtures for authoritative rechecks, atomic mutation/audit behavior, commit ambiguity, retained usage, and late settlement fencing. Controlled adapters establish functional outcomes, not storage correctness.
- Preserve deletion journal I/O outside SQL locks, committed proof before finalization, deletion despite failed best-effort flush, fresh Pending re-registration, and inability to charge a replacement account.
- Retain existing deletion admission/finalization, retained usage, and protected accounting tests. The Recovery Journal itself is not being redesigned.

### T8. Verification and evidence

Run targeted behavioral suites while changing the modules, then the repository's routine and race profiles and applicable disposable PostgreSQL qualification at the integrated revision. Record fixture prerequisites and distinguish skipped tests from executed coverage. Renew checks when subsequent changes invalidate their evidence.

A spec-only change does not establish any runtime result. Native images, provider/OCI qualification, hosted CI, deployment, live devices, and forced live transfers remain independent gates. Review both consistency with accepted decisions and whether the final interfaces actually hide caller coordination.

## Out of Scope

- Architecture-review candidates 3 and 4: whole-process lifecycle extraction and Recovery Journal proof extraction.
- A generic mutation/command framework, callback runner, event bus, broker, durable invalidation history, or new grant registry.
- Replacing the accepted persistence repositories, adding an ORM or another production datastore, or changing the database schema.
- Redesigning credential issuance, refresh/replay policy, Quota Plan creation/archival, account statuses, or administrator authority.
- Changing quota semantics, weekly accounting, settlement receipts, traffic attribution, or deletion/recovery retention.
- Persistent concurrent authenticated transports, changed Peer ID capacity counting, durable Device Identity enrollment, or Device Network ownership.
- New HTTP or relay protocol versions, a portal redesign, or client-runtime changes.
- Changing complete Relay Drain policy or extracting command startup/shutdown composition; only the cleanup ownership needed by the selected session work is included.
- Implementation ticket breakdown, code implementation, commits, publication, deployment, and live qualification as part of this specification-preparation task.

## Further Notes

The five accepted choices and explicit testing confirmation are recorded in [decisions.md](decisions.md). [README.md](README.md) provides source wayfinding and related decision references; [run.md](run.md) records the isolated specification lineage. Specific implementation paths and concrete Go declarations are deliberately absent from the Implementation Decisions section.

This spec completes the previously accepted ownership direction rather than reopening account, quota, persistence, transport, or Relay Session product policy. The current source's physical-close callbacks and synchronous close paths are structural evidence motivating the change; the missing explicit closing state is a testable concern, not fresh execution evidence.

Existing domain terms are sufficient. No glossary or ADR change is required by the five choices. If implementation uncovers a necessary change to an accepted invariant, record and resolve that decision before implementing the changed behavior.

The user's latest instruction authorizes preparing and publishing this specification to the local Markdown tracker. The ready-for-agent status describes specification readiness; it does not claim implementation or authorize unrelated external actions.
