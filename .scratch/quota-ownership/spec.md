# Quota ownership of usage and live Relay Session counts

Status: ready-for-agent

## Problem Statement

An Administrator Identity can lower a Relay Account's session cap while Relay Authentication is in progress. Quota can read the old cap from PostgreSQL, then attach the in-memory revision created by the edit to that old result. Relay's final revision check accepts the pairing. With three live Relay Sessions and a cap reduced from five to three, that ordering can admit a fourth session. The existing race test pauses before the database read and therefore exercises a different ordering.

Auth currently compares the newly committed session cap with Relay's live count and decides whether to retire sessions. Auth also handles live counts, Recorded Relay Usage and history for the Portal, and coordinates work that reads or retains usage during deletion, registration and recovery. Moving the cap decision to Quota requires preserving the ordering provided by Auth's current live-count query. Reading Quota's previously reported count alone can miss a session that has passed Relay's final check and is about to be installed.

## Solution

Give Quota responsibility for account-level admission, weekly usage, aggregate live Relay Session counts and retirement decisions for both Relay Quota limits. Auth validates credentials and commits account policy. Relay owns exact connections and applies Quota's retirement decisions. Protect the database-read window with a Quota policy version check and reuse Relay's existing registry lock to order final admission against application of committed limits.

Move Portal usage and live count reads to the Portal read path. Move deletion accounting to the deletion workflow and Quota, with retained-usage operations participating in the existing account transactions. Preserve the wire behavior, the Portal's protected reads and deletion's accounting guarantees.

## User Stories

1. As an Administrator Identity, I want a committed session cap to govern admissions once applied locally, so that a racing authentication cannot use an older cap.
2. As an Administrator Identity, I want a cap equal to the live count to preserve those sessions, so that lowering a cap to current occupancy causes no interruption.
3. As an Administrator Identity, I want a cap below the live count to retire all affected sessions, so that the existing enforcement rule stays predictable.
4. As an Administrator Identity, I want a cap reduction to include sessions that finished admission first, so that enforcement uses the actual occupancy.
5. As an Administrator Identity, I want a weekly allowance reduction to use recorded and unsettled usage, so that recent traffic counts toward the new allowance.
6. As an Administrator Identity, I want a later allowance increase to supersede a stale exhaustion assessment, so that an obsolete cutoff cannot retire valid sessions.
7. As an Administrator Identity, I want Quota Plan reassignments and overrides to follow the same limit application flow, so that the source of the edit does not change enforcement.
8. As an Administrator Identity, I want successful cap changes that require retirement to remove permission before acknowledgement, so that new relay work cannot continue on those sessions.
9. As an Administrator Identity, I want an uncertain policy commit to retain its fail-closed outcome, so that ambiguous persistence cannot grant access.
10. As a Clipp client, I want an assessment interrupted by a cap change to produce a retryable refusal, so that I can obtain a fresh decision.
11. As a Clipp client, I want a fresh assessment at the session cap to give the existing session-limit refusal, so that I can distinguish capacity from transient accounting trouble.
12. As a Clipp client, I want a renewal on the same connection to keep its slot, so that reauthentication does not consume additional capacity.
13. As a Clipp client, I want same-account replacement for my Peer ID to use one slot, so that reconnecting works at capacity.
14. As a Relay Account owner, I want cross-account replacement to update both accounts' counts together, so that capacity follows the actual session association.
15. As a Relay operator, I want Relay to enforce the global Safety Limit, so that account-level headroom cannot exceed Relay Instance capacity.
16. As a Relay operator, I want a disconnect, expiry or revocation to release the corresponding account slot, so that counts follow the live registry.
17. As a Relay operator, I want background accounting to preserve live counts while evicting idle usage state, so that first admission and later disconnect remain observable.
18. As a Relay Account owner, I want usage cutoff to remove permission while physical closure proceeds separately, so that a slow socket does not delay enforcement.
19. As a Relay Account owner, I want the Portal to continue showing confirmed weekly usage and history, so that I can understand my consumption.
20. As a Relay Account owner, I want the Portal to distinguish configured caps, live Relay Sessions and active Login Grants, so that I can tell which limit I am approaching.
21. As a Relay Account owner, I want an unavailable live count to remain visibly unknown, so that a sampling failure is distinguishable from zero sessions.
22. As a Relay Account owner, I want a concurrent revocation to prevent a stale owner profile read, so that the Portal continues to protect usage information.
23. As an Administrator Identity, I want the selected account view to retain authorized access to confirmed usage, so that account review remains possible.
24. As a Relay Account owner, I want deletion to remove credentials and live permission promptly, so that the account cannot keep relaying during finalization.
25. As a Relay Account owner, I want deletion to attempt a bounded final accounting flush after Recovery Journal proof, so that pending usage has an opportunity to settle.
26. As a Relay Account owner, I want deletion to finish when the best-effort flush fails, so that pending accounting cannot indefinitely delay erasure.
27. As a Relay Account owner, I want confirmed current-week usage retained atomically with the deletion seal, so that re-registration preserves the correct usage and late settlement cannot charge the replacement account.
28. As a returning Relay Account owner, I want Retained Quota Usage imported once into the fresh Pending Relay Account in the same Weekly Quota Window, so that deletion cannot reset that week's consumption.
29. As a recovery operator, I want offline recovery to preserve the accepted Retained Quota Usage rules, so that a restored database does not reintroduce deleted accounts or lose confirmed usage.
30. As a maintainer, I want Auth to handle account policy and credential facts without receiving usage figures, so that accounting changes remain local to Quota and its readers.

## Implementation Decisions

### I1. Module ownership

- Auth owns External Identity, account status, configured policy, Portal Session proof, administrator authority, Login Grants and Relay Access Token validation. It commits account policy and supplies confirmed configured limits to Quota. The Relay Authentication credential result carries account and grant identity, credential generation and expiry. Admission obtains the effective session cap from Quota.
- Quota owns the weekly accounting state, recorded and unsettled usage, aggregate account occupancy, account admission decisions and both Relay Quota retirement decisions. The live count is process-local. Relay supplies facts about committed registry transitions.
- Relay owns connection identity, one effective Relay Session per Peer ID, exact attempt and connection ownership, reservation and Rendezvous Lease state, global Safety Limits, logical retirement and bounded physical close. A retired connection loses permission immediately. Previously admitted streams and buffered traffic keep their existing behavior.
- Auth continues to own the policy decision to revoke credentials or deny account access. A quota-driven retirement leaves the account and its Login Grants intact.

### I2. Final interfaces for admission

These names describe the agreed method shapes. Types carry only the facts needed by their consumer.

| Interface operation | Inputs and outcome | Contract |
| --- | --- | --- |
| Quota `Assess` | Request context and Relay Account ID. Returns an opaque `Assessment` or the existing refusal/error classification. | Reads current durable caps and usage. Captures policy version before persistence I/O and checks it before applying the result to accounting or health state. The assessment binds the account, current accounting state identity, revision and Weekly Quota Window. |
| Quota `CommitAdmission` | Assessment, `SessionChange` and an install function returning final absolute counts for affected accounts. Returns admission or a classified refusal. | Relay holds its registry lock. Quota checks freshness and account allowance/cap, then executes the in-memory install and publishes the final counts under Quota's state synchronization. On refusal the install function never runs. |
| Quota `PublishLiveCounts` | Final absolute counts for the Relay Accounts affected by disconnect, expiry, retirement or other removal. | Relay reports under the registry lock. Quota updates related accounts as a batch. Idle accounting eviction cannot lose a successful session's count. |

Relay verifies the credential with Auth, obtains `Assess` without its registry lock, prepares the installation, then takes the registry lock and rechecks the exact attempt, connection and Relay Instance Safety Limits. `SessionChange` identifies a new session, same-connection renewal or replacement. A new session consumes one slot. Same-connection renewal adds no slot and keeps its existing eligibility semantics. Same-account replacement counts its displaced slot and remains subject to the existing cap semantics. Cross-account replacement consumes one slot in the target account and releases one in the previous account upon successful installation. The install function performs in-memory state changes only and never calls back into Quota. External effects and blocking work run outside the Quota state lock. Relay sends the existing success response only for a successful installation.

### I3. Final interfaces for committed cap changes

| Interface operation | Inputs and outcome | Contract |
| --- | --- | --- |
| Quota `ApplyCommittedLimits` | Relay Account ID and confirmed configured weekly byte allowance and session cap. Returns `PolicyApplication` or a classified error. | Auth calls after committing policy and while the existing account protection is held. Quota enters the Relay reconciliation operation before taking its own state lock. The outcome can indicate deferred cleanup and carries no usage or count data to Auth. |
| Relay `ReconcileAccountSessions` | Relay Account ID and a Quota decision function accepting the exact live count and returning a retirement decision. | Under the same registry lock used by final admission, Relay supplies the exact count. Quota refreshes its count, applies both limits and returns the decision. Relay completes any requested logical retirement and publishes the resulting count before unlocking. |

The Quota decision function does in-memory work only. It advances the policy version even when an account has no current accounting entry. Weekly exhaustion occurs at or above the weekly allowance. A session cap reduction retires all affected Relay Sessions when the live count exceeds the new cap. At equality the current sessions stay and an additional slot is refused. Preserve existing diagnostic precedence for quota exhaustion and account access invalidation. Auth's confirmed commit, synchronous logical effects and post-guard physical cleanup retain their ordering. An uncertain commit follows the existing fail-closed invalidation and reconciliation flow. Speculative limits from an uncertain commit cannot be applied as confirmed policy.

### I4. The two freshness windows

1. **Database read in flight.** Snapshot a private Quota policy epoch before I/O. Increment it under Quota synchronization on each locally applied committed policy update, including updates to accounts with no in-memory entry. If it changed when a read returns, discard the assessment before changing receipts, accounting health or eligibility. Return the existing temporary refusal. A Quota-wide epoch can conservatively invalidate an unrelated account's overlapping assessment. Keep persistence I/O outside both the registry lock and Quota state lock.
2. **Assessment complete, installation pending.** Revalidate the assessment under Quota synchronization during `CommitAdmission`, with the registry lock held. Cap reconciliation uses that same registry lock. The account association, accounting state identity, revision and week must still match. Recreating an idle account entry cannot make an old assessment current.

With three live sessions and a completed assessment for cap five, a cap reduction to three has two valid orderings:

| Registry lock winner | Sequence and result |
| --- | --- |
| Cap reconciliation | Auth commits the edit → Quota applies cap three using live count three → Quota advances the revision and keeps the existing sessions → the fourth authentication fails the final freshness check. A fresh attempt sees cap three and receives the session-limit refusal. |
| Final admission | Quota validates the assessment → Relay installs the fourth session → Quota publishes count four → Quota applies cap three using count four → Relay synchronously retires all four and defers physical close. |

The ordering point is in-process application of the committed cap. The design preserves the existing separation from the PostgreSQL commit and makes no new cross-process synchronization assumption.

### I5. Accounting cutoffs and live counts

- Preserve Quota's existing usage observation, settlement, independent health deadlines and account cutoff delivery. An exhaustion decision from the background worker must still match the current assessment when it marks an account blocked. Relay verifies a delayed cutoff's freshness when it applies logical retirement. Deliver asynchronous cutoffs after Quota releases its state lock.
- The Quota live count reflects authorized Relay Sessions. Pending authentication and sockets awaiting close consume no count in this projection. Count changes accompany registry changes. Samples distinguish a known zero from an unavailable observation.
- Preserve the existing processing-time attribution of Charged Relay Traffic, Weekly Quota Window and confirmed settlement receipts. Concurrent assessment application cannot regress a newer confirmed receipt.

### I6. Portal ownership

- The Portal read path owns owner and administrator usage projections, recorded totals, history decoding, and the live count sample from Quota. Auth supplies owner or administrator authorization and credential facts, including the active Login Grant count.
- The protected owner read rechecks the Portal Session and snapshots persisted account policy and Recorded Relay Usage in the same database operation, as today. The read adapter may consume a scoped authorization proof supplied by Auth. The usage result goes directly to the Portal. Preserve administrator authorization, deletion visibility rules, and existing refusal/error behavior.
- Continue to display confirmed Recorded Relay Usage and history. Sample the live count separately from the durable profile and configured limits. Preserve the presentation of an unknown sample and existing Portal routes, forms and rendering behavior.

### I7. Deletion and related retained-usage workflows

- Auth verifies authority for Relay Account Deletion and owns credential invalidation. The deletion workflow owns the account lifecycle handoff to Quota, using the existing `BeginAccountingDeletion`, `FlushDeletionAccounting` and `ForgetAccounting` operations. Auth receives lifecycle outcomes and credential facts.
- Preserve synchronous Relay permission removal during deletion admission and deferred physical cleanup. After committed Recovery Journal proof, the deletion workflow asks Quota for the existing bounded, best-effort flush. Run the flush outside Auth protection and before taking the finalization account-row lock. A failed or unresolved flush alone cannot delay erasure.
- Preserve the finalization transaction that locks the account, retains confirmed current-week Recorded Relay Usage, seals against late settlement, detaches identity and records completion and audit. Confirm a lost commit acknowledgement before forgetting in-memory accounting. Keep the existing idempotent retry behavior.
- Move retained-usage reads, import decisions, retention key coverage and recovery accounting out of Auth's usage path. Use transaction-scoped accounting operations inside the existing registration, deletion and offline recovery transactions. A returning External Identity imports only current-week Retained Quota Usage once as the fresh account's opening total. Retention cleanup remains bounded and recovery continues to require the existing writer fence, journal proof and Recovery Review Hold behavior.

### I8. Compatibility

Reuse the production PostgreSQL adapter and existing functional transaction ownership. The design requires no schema migration, new remote interfaces or change to Clipp clients. Keep Relay Authentication responses and retries, session expiry, replacement, resource limits, grant and account revocation, and transport fallback. Preserve the semantics of exact connection authorization, Charged Relay Traffic, and Retained Quota Usage.

## Testing Decisions

Use the existing composed administrator HTTP and real libp2p Relay Authentication seams for the concurrency contract. Use a controlled adapter at the existing persistence read or Relay-to-Quota call to pause a specific operation. Barriers establish ordering, and assertions inspect wire results, observable Relay Session counts or permission, or durable PostgreSQL outcomes. Tests should survive internal restructuring and avoid sleeps, mutex inspection and assertions on private maps. Use disposable PostgreSQL for database snapshot, retention and transaction guarantees. Pure accounting cases can use the existing in-memory persistence adapter.

| ID | Coverage |
| --- | --- |
| T1 | Baseline. Preserve the existing protected wire case that edits the cap before the database read. A fresh assessment sees cap three, the fourth AUTH reports the session-limit refusal, and the count stays three. |
| T2 | **Window 1.** Have the persistence adapter capture the actual cap-five result and hold its return. Complete the administrator edit to cap three, then release the captured result. Assert the fourth AUTH gets the existing temporary refusal, all three existing sessions remain permitted, and a fresh attempt sees the session-limit refusal. Cover an edit during first assessment before a local accounting entry exists. |
| T3 | **Window 2, cap wins.** Hold a successfully completed cap-five assessment before Relay enters final admission. Complete the cap-three edit, then deliver the old assessment. Assert the temporary refusal and that no fourth session is authorized. |
| T4 | **Window 2, admission wins.** Pause the fourth attempt at final admission after validation, with the registry lock held and before the count is published. Start the cap edit and establish that it has committed and is attempting local application. Release admission. Verify that the cap decision sees four and all four lose permission before the edit acknowledges. Hold physical close to distinguish logical invalidation from socket I/O. |
| T5 | Live counts and admission behavior. Exercise initial admission, at-cap same-connection renewal, same-account replacement, cross-account replacement, disconnect, expiry and revocation. Verify both affected account counts on replacement, absence of a slot for refused attempts, and preserved global Safety Limit and one-session-per-Peer-ID behavior. |
| T6 | Weekly cutoff and failure. Hold an exhausted assessment across an allowance increase and ensure stale work cannot retire valid sessions. Hold a delayed cutoff across policy recovery and assert the same. Retain usage settlement, uncertain receipt, week rollover and independent accounting health coverage. |
| T7 | Mutation protection. Exercise an ambiguous commit and post-commit request cancellation through existing protected HTTP fixtures. Verify existing fail-closed outcomes, synchronous logical invalidation when required, and deferred completion outside Auth guards. |
| T8 | Portal. Through existing authenticated owner and administrator routes with disposable PostgreSQL, assert current-week Recorded Relay Usage, history, configured caps, live session count and active Login Grant count. Cover unavailable versus zero counts, owner session revocation between authentication and the protected read, and the single durable policy-and-usage snapshot. |
| T9 | Deletion. Through existing deletion admission and worker seams, assert that journal proof precedes the bounded flush, an unavailable flush cannot stop finalization, and a confirmed or independently reconciled commit retains confirmed usage and seals late settlement. Verify in-memory state is forgotten only after proof of completion. |
| T10 | Re-registration and recovery. Retain existing PostgreSQL coverage for same-week import exactly once, week expiry, pepper coverage, duplicate logical retained usage, uncertain commits and restored-account deletion. Verify behavior through protected registration and the offline recovery operation. |

Prior art includes `TestProductionProtectionReadsCurrentCapAfterAuthReturns`, the delayed-close Relay Authentication tests, `TestOwnerProfileUsesOnePolicySnapshotAcrossConcurrentChange`, and the existing observed deletion, retained usage and offline recovery suites.

## Out of Scope

- The separate malformed-authentication terminal-close proposal and the Quota Plan assignment picker proposal.
- Public Address Snapshot consolidation, native token exchange rendering changes and a split of the Relay Session and Rendezvous registries.
- Changes to transport selection, wire protocols, Relay Quota semantics, accounting cadence, capacity limits, or the deletion and Recovery Journal contracts.
- New schema, multi-Relay-Instance accounting, deployment or live qualification.

## Further Notes

- This design supersedes the earlier assignment of account-level live session-cap decisions to Relay. It also moves usage figures out of the current Auth implementation. Relay still owns session lifecycle and cleanup. The existing persistence and transport contracts continue to apply. See the [previous ownership specification](../account-session-ownership/spec.md#i8-auth-quota-and-relay-collaboration), [decision record](decisions.md), and [glossary](../../GLOSSARY.md).
- The source review was pinned to relay `4586a48b60a3e92455c560bbb7648dd733f27625`. The race is a source-derived interleaving. T2–T4 are the required implementation evidence.
