# Relay, Quota and Auth: periodic accounting and complete operations

Status: ready-for-agent
Decisions consolidated: 2026-10-24
Testing seams: confirmed by the user on 2026-10-24.
Implementation: not started; this document does not claim runtime verification.
Scope: Clipp Relay, based on the architecture investigation at revision cca0bb2.

## Problem Statement

Relay usage is currently charged through prepaid blocks. Owners can lose allowance for unused reserved credit, and the data plane repeatedly coordinates balance reads, funding and byte splitting while forwarding traffic. Accounting knowledge is spread across callers instead of being owned by Quota.

Account mutations and Relay Authentication also rely on ordering rules that are larger than their declared interfaces. Startup wiring makes policy decisions, tests copy that wiring, and optional method discovery can conceal required synchronization. This makes it difficult to change account policy without risking stale admission, missed invalidation or duplicated accounting.

The owner wants predictable periodic accounting, correct durable totals with safe retries, and clear ownership across Relay, Quota and Auth. The deployment currently has only the owner's test account, so preserving prepaid accounting data is unnecessary. Historical event replay is not required.

## Solution

Record observed Charged Relay Traffic against each Relay Account's Weekly Quota Window, settling aggregated usage on a one-second target cadence. Share accounting across all sessions of the account. Accept a soft traffic allowance and possible loss of unsettled observations on crash, while preventing duplicate charging and preserving all confirmed durable totals.

Quota owns observation aggregation, settlement, retries, reconciliation and accounting eligibility. Auth owns credentials, account policy and complete protected mutation/invalidation operations. Relay owns report-time attribution and physical session admission, replacement and closure. Deepen these existing modules without adding a forwarding coordinator.

Use lightweight, in-process CQRS: complete commands and consumer-oriented query models in the same process and PostgreSQL database. Weekly usage totals remain authoritative, supported by bounded reconciliation evidence. Preserve immediate account invalidation, the existing one-session-per-Peer-ID contract, privacy-preserving deletion and recovery protections.

## User Stories

1. As a Relay Account owner, I want allowance consumed by observed Charged Relay Traffic, so that unused prepaid blocks do not reduce my available allowance.
2. As an owner, I want all my Relay Sessions to share one account allowance, so that opening more sessions does not multiply the available balance.
3. As an owner, I want the portal to show Recorded Relay Usage, so that I can distinguish confirmed consumption from pending observations.
4. As an owner, I want remaining allowance displayed as zero when usage exceeds the cap, so that overrun does not create misleading negative availability.
5. As an owner, I want prior weekly totals preserved within the existing retention policy, so that I can review consumption without a detailed traffic history.
6. As an owner, I want unused allowance to expire at Monday 00:00 UTC, so that weekly accounting remains predictable.
7. As an owner, I want traffic observed before Monday charged to the preceding week even if persistence finishes later, so that settlement delays do not move consumption between windows.
8. As an owner, I want database retries to preserve the same total, so that lost replies do not charge the same traffic twice.
9. As an owner, I want confirmed usage to survive process restart, so that restarting the relay does not reset consumption.
10. As an operator, I want periodic account-level settlement instead of per-block persistence, so that higher throughput does not require proportionally more funding transactions.
11. As an operator, I want bounded pending state and background work, so that accounting failures cannot create an unbounded queue.
12. As an operator, I want all attributable overrun recorded, so that the durable total does not conceal traffic beyond the configured allowance.
13. As an operator, I want HOP and STOP endpoints charged independently, so that the existing conservative traffic measure is preserved.
14. As an operator, I want unattributed reporter tails kept as aggregate observations, so that the relay does not invent account attribution it cannot prove.
15. As an operator, I want sessions closed when accounting stays unhealthy, so that a persistence outage does not grant unlimited relay use.
16. As an operator, I want the accounting failure deadline configurable with a three-second default, so that I can select an appropriate bounded tolerance.
17. As an operator, I want invalid deadline configuration rejected, so that a zero or invalid value cannot silently disable enforcement.
18. As an operator, I want a successful balance read unable to hide pending-write failures, so that accounting health reflects the ability to record traffic.
19. As an operator, I want idle accounts checked without empty usage writes, so that accounting remains healthy without generating unnecessary persistence work.
20. As an administrator, I want account suspension and revocation to invalidate live relay use immediately through the protected mutation workflow, so that polling cannot prolong access.
21. As an administrator, I want quota changes to preserve already recorded and pending usage, so that changing policy does not erase consumption.
22. As an administrator, I want an exhausted traffic allowance to close all account sessions, so that individual connections cannot continue with independent balances.
23. As an administrator, I want a session-cap reduction below the current count to close all account sessions and constrain reconnection, so that the existing deterministic policy is preserved.
24. As an owner, I want Sign out everywhere to retain its account-wide effect, so that all existing credentials and live Relay Sessions are invalidated together.
25. As an owner, I want grant-local termination to retain its existing behavior, so that this refactor does not unexpectedly change the lifetime of already admitted sessions.
26. As a Clipp user, I want authentication checked for the exact physical connection, so that another connection cannot inherit its authority.
27. As a Clipp user, I want same-Peer-ID replacement to remain atomic, so that reconnecting does not exceed session capacity or destroy a valid session after a failed replacement.
28. As a Clipp user, I want failed renewal to preserve a still-valid prior session under the existing rules, so that transient failure does not introduce new disruption.
29. As a Clipp user, I want committed account invalidation to defeat authentication already in flight, so that stale credential observations cannot restore access.
30. As a Clipp user, I want temporary accounting failure distinguished from proven exhaustion, so that capacity and outage responses remain meaningful.
31. As an owner deleting my account, I want live sessions closed immediately, so that deletion stops ongoing use.
32. As an owner deleting my account, I want failed preservation of pending usage not to block deletion, so that an accounting failure does not indefinitely retain my account.
33. As an operator, I want deletion to retain only confirmed current-week usage in the existing privacy-minimal form, so that re-registration cannot evade confirmed consumption.
34. As an owner, I want late accounting work unable to recreate my deleted account or debit a fresh registration, so that deletion remains final.
35. As an operator, I want graceful drain to continue settlement and attempt a final flush, so that shutdown preserves as much observed usage as its existing budget permits.
36. As an operator, I want forced shutdown to remain bounded, so that accounting cannot indefinitely delay process termination.
37. As an operator recovering a database, I want the Recovery Journal and credential-reset protections preserved, so that accounting changes cannot resurrect deleted accounts or credentials.
38. As a maintainer, I want Quota to expose complete accounting operations, so that Relay does not need to know batch sizes, retry state or persistence sequencing.
39. As a maintainer, I want Auth to expose complete protected account operations, so that mutation callers do not duplicate commit, fence, invalidate and cleanup rules.
40. As a maintainer, I want required module cooperation explicit, so that construction or compilation catches missing synchronization instead of silently skipping it.
41. As a maintainer, I want commands separated from presentation queries, so that read models can evolve without weakening mutation and admission correctness.
42. As a maintainer, I want tests to use production composition and observable outcomes, so that they catch integration mistakes without duplicating policy logic.
43. As the current operator, I want a simple reset of obsolete test accounting during migration, so that legacy prepaid semantics need not be maintained alongside observed usage.

## Implementation Decisions

### I1. Module ownership and interfaces

- Relay owns peer/protocol report-time attribution; exact-connection Relay Authentication; physical session installation, renewal, replacement, rollback, expiry and closure; live capacity; and Relay Drain. It retains account identity and fixed expiry as session references, not authoritative copies of account policy or a per-connection allowance.
- Quota owns all pending account-week usage, cadence, in-flight settlement, stable retry identity, uncertain-commit reconciliation, authoritative usage reads, accounting health and exhaustion decisions. Its interface accepts attributed observations, assesses accounting eligibility, processes account/lifecycle changes and exposes usage queries and closure outcomes. Relay does not read/fund/split/consume prepaid blocks or run settlement loops.
- Auth owns credentials, Login Grant validation, authoritative account policy, shared account protection and complete mutation completion. It coordinates commit outcomes, conservative fencing, synchronous local invalidation and cleanup after protection is released. Quota never interprets Login Grants.
- Required collaboration is explicit and installed before serving. Remove optional method-discovery paths that silently omit database work admission, shared account protection or invalidation. Concrete Go declarations remain implementation choices constrained by this contract.
- Startup composition connects modules; it does not decide quota exhaustion, account policy or session-cap effects. Do not add a generic access module or coordinator that merely forwards calls.

### I2. Lightweight CQRS

- Separate task-oriented command operations from query models shaped for portal/admin consumers, within existing modules and the existing PostgreSQL database. Command results may report outcomes; this is not a requirement for every mutation to return nothing.
- Commands encapsulate authoritative validation, protection and side effects. Do not decompose admission into unprotected account and balance queries followed by installation.
- Presentation reads never grant authority. Preserve fresh credential/policy validation and protected accounting eligibility for admission; existing database-outage fail-closed behavior for new authentication, discovery and credential operations remains.
- Weekly totals are authoritative. No event store, historical replay contract, command bus, broker, separate read database or asynchronous projection is introduced.

### I3. Traffic observations and weekly accounting

- Accept the existing stock reporter's Charged Relay Traffic measure. HOP and STOP endpoints count independently, including twice for one account, and control bytes may be included. This is not exact application payload measurement.
- Attribute using the peer's current account association when the reporter processes the callback. Preserve the known limitation that an old-connection tail can be attributed to a replacement association. Without an association, record aggregate unattributed bytes only; do not reconstruct a connection history.
- Assign each observation to its processing-time Weekly Quota Window. Weeks start Monday 00:00 UTC; unused allowance never rolls over. A delayed settlement retains its original week rather than selecting the transaction's current week.
- Aggregate observations across every session of an account without waiting for a database round trip in the reporter. Use checked nonnegative counters; never wrap, clamp recorded overrun to the cap or silently discard observations during ordinary concurrency.
- Settle nonempty aggregated usage on a one-second target cadence. This is a target, not a transaction completion guarantee or a fixed maximum byte-overrun promise. Empty observations require no debit transaction.
- Keep pending observations separate from an immutable in-flight batch. Acknowledgment removes only the confirmed batch; observations arriving during persistence survive for later settlement. Scheduling and concurrency are bounded; avoid one worker or durable row per traffic callback.
- Admission and exhaustion decisions account for confirmed usage plus locally known unsettled usage exactly once. A confirmed batch included in a refreshed total must no longer also count as pending. Confirmed exhaustion closes all account sessions; closure may leave buffered/unattributed tails under the existing reporter contract.
- A new week does not require reauthentication solely because the week changed. Continuing use requires healthy, authoritative new-week eligibility; unsettled prior-week work remains associated with its original week and cannot be discarded or hidden by a new-week read.

### I4. Durable totals and safe retries

- Replace prepaid committed-credit persistence with a nonnegative Recorded Relay Usage total per account and week. Settlement deltas are observed bytes and are not limited to the old 64 KiB allocation size. Update portal, retained-usage import and recovery consumers consistently.
- Use a stable operation identifier, immutable account identity, observation week, positive byte delta and an ordered settlement sequence. One account-week has at most one unresolved settlement operation; newer observations accumulate separately until its outcome is reconciled.
- Atomically update the total and the latest reconciliation receipt in one short PostgreSQL transaction. A compact latest receipt plus monotonic sequence is sufficient; no per-settlement historical ledger is required. The receipt identifies the operation and its delta so an identifier cannot be accepted with a different payload.
- A repeat of the latest matching operation confirms its outcome without adding bytes. A new operation advances the expected sequence once. An older sequence, a conflicting identity/payload or an unexplained gap must never be converted into a new debit. Do not automatically renumber a stale retry.
- After a lost commit reply, retain and reconcile the same operation before admitting its successor. Distinguish a known rollback from an unknown result; generic blind retries of ambiguous writes are prohibited. A receipt can be replaced only after the previous operation is resolved and the sequence prevents older work from applying again.
- Initialize new process-local accounting from durable state. Do not reconstruct lost observations or replay an abandoned operation as new usage after restart. Delayed work and restart reconciliation must obey database serialization and sequence checks. Preserve the singleton writer assumption; this change does not support multiple serving Relay Instances.
- Account-row serialization must protect settlement against policy mutation, deletion finalization and retained-usage import. Settling already observed traffic is distinct from authorization to continue: suspension, revocation, generation changes or exhaustion do not automatically discard valid pending usage.
- Deletion finalization seals the old account against settlement in the same serialized ordering used to capture retained usage. Never upsert an absent/deleted account or redirect an old batch to a new account with the same External Identity.
- Keep receipt storage bounded with the account-week total. Preserve current-week plus twelve preceding weekly windows and existing deleted-account cleanup. Retired weeks cannot be recreated by stale settlement. Pending-state expiry, cleanup and reconciliation must cooperate so normal retry cannot bypass retention or double charge.
- Crash may lose observations not successfully persisted. A committed transaction whose reply was lost remains durable. Neither the one-second cadence nor this loss allowance permits duplicate charging, partial total/receipt updates or corruption of confirmed totals.

### I5. Accounting health, policy changes and configuration

- Configure an accounting failure deadline, default three seconds, effective on restart. Validate a finite positive duration compatible with the one-second cadence; require at least the cadence. Zero must not disable enforcement. Wire the setting through supported operator configuration and document it.
- Measure staleness with monotonic elapsed time. Establish initial eligibility through authoritative reads; preserve existing conservative handling of unreliable clock information and use database time after lock waits for authoritative expiry checks. Clock regression must not extend access.
- While usage is pending or a write outcome is unresolved, a balance read alone cannot refresh accounting health. Successful settlement/reconciliation can establish progress, but success for one batch or week must not conceal an older unresolved batch. With no pending usage, a successful authoritative balance read can refresh health.
- At the deadline, detach and close affected account sessions and deny new admission until accounting eligibility is healthy again. Deadline enforcement cannot wait behind a blocked settlement worker. Respect existing bounded database work admission and operation deadlines.
- Distinguish proven quota exhaustion from temporary persistence/reconciliation/clock failure. Do not expose ambiguous writes as evidence of zero remaining allowance or grant access from a stale presentation query.
- Quota policy edits retain recorded and pending consumption. The effective cap applies to the current week without refund or reset. The old exception preserving prepaid credit at equality disappears: no positive remaining allowance means exhausted under observed-usage accounting.
- Session-cap reductions below the live count preserve the existing close-all-and-reconnect policy; no chosen survivors. Account policy effects use the immediate protected invalidation path rather than awaiting the next accounting tick. Existing administrative invalidation timing requirements remain.
- Account-wide revocation, suspension and related security changes remove permission to continue. Preserve grant-local termination behavior: already admitted sessions have no live Login Grant association and keep their fixed expiry unless an account-wide event or another existing session-ending condition applies.

### I6. Protected admission and mutation ordering

- Admit database work before waiting for peer or identity protection. Nested operations reuse the admitted work budget rather than consuming independent capacity.
- Preserve the established lock order: identity protection for identity/security operations or peer protection for admission, then all involved account guards in consistent account-ID order, then account rows and subordinate database rows in consistent order, then brief live-registry updates. Do not nest peer and identity guards or acquire these protections in reverse.
- Cross-account same-peer replacement protects both accounts. Keep account protection from authoritative credential/policy recheck through accounting eligibility and physical session installation.
- Committed or conservatively fenced invalidation must defeat authentication based on older observations. Auth completes local policy effects under account protection; Relay detaches registry authority there and performs connection-close I/O after logical guards are released.
- Keep provider, object-storage and connection-close I/O outside database row locks and registry mutexes. Mutation uncertainty prevents admission until reconciled and never resurrects a closed session.
- Preserve atomic one-session-per-Peer-ID replacement, exact-connection protocol gating, failed-renewal semantics, global/account capacity and fixed expiry. If the outer admission operation fails after installation, remove the installed session.
- Preserve existing preauthentication allowances, including required connection setup, Identify and Ping. This refactor does not adopt an Auth-only protocol allowlist or change client transport fallback.

### I7. Deletion, drain and recovery

- On account deletion, invalidate credentials and detach/close sessions through the existing protected workflow. Attempt bounded final settlement of already attributed usage, including reconciliation where possible.
- Failure to preserve pending usage must not stop deletion or leave it pending solely for accounting. Other existing deletion prerequisites, including Recovery Journal proof, remain mandatory. This decision does not waive journal or database correctness requirements.
- Finalization reads confirmed durable current-week usage under serialization with settlement, creates Retained Quota Usage and seals the account against further writes. A batch committing before that boundary is included; one after it is rejected. A late acknowledgment cannot reopen accounting state or restore access.
- Retained Quota Usage remains privacy-minimal, current-week only, and is imported exactly once when the External Identity registers a fresh Pending Relay Account. Older-week usage is not rolled into the new account. Fresh registration does not inherit credentials, approval or physical sessions.
- During Relay Drain, continue settlement for existing traffic and attempt a final flush within existing shutdown limits. Preserve discovery withdrawal, refusal of new work, bounded circuit drain and forced termination. Do not extend shutdown indefinitely for accounting.
- Keep pending state only while needed for live accounting or bounded reconciliation, including after ordinary disconnection/revocation. Do not erase it merely because the last session disappears. Deletion and forced exit retain their explicit loss exceptions.
- Preserve independent Recovery Journal enforcement, restore-time credential invalidation and Recovery Review Hold. Update quota recovery handling for observed totals and receipts; restoration must not revive deleted accounts, obsolete retry operations or live sessions.

### I8. Presentation, migration and compatibility

- Show Recorded Relay Usage as confirmed durable consumption and remaining allowance as the maximum of zero and cap minus recorded usage. Explain periodic update lag and the conservative endpoint measure. Do not describe pending observations as confirmed or recorded totals as exact payload traffic.
- Preserve consistent portal/admin account and usage snapshots and existing historical-window presentation. Live session capacity remains a live sample rather than an implied transactionally frozen historical value.
- Update accounting diagnostics and termination reasons to distinguish exhaustion, staleness and settlement uncertainty using the existing diagnostic surfaces. Do not introduce per-device records, per-buffer logs or a new analytics product.
- Use a versioned, offline accounting migration under the existing stop/migrate/serve procedure. Reset obsolete prepaid accounting and related test retained-usage/receipt state instead of attempting to translate reserved credit into observed traffic. Preserve unrelated accounts, credentials, plans, audit and Recovery Journal data.
- Prevent an old prepaid binary from serving against the new accounting schema. A reset is an explicit migration action, not something repeated on ordinary restart. Test fresh installation and upgrade/reset behavior.
- Reconcile the existing service specification, README, operator configuration, diagnostics, portal wording, retention and recovery documentation with the new accounting semantics. Quota Committed remains the distinct historical prepaid term; it must not silently become a synonym for Recorded Relay Usage.

## Testing Decisions

1. **Confirmed seams:** use the composed relay's HTTP and libp2p interfaces with real PostgreSQL as the primary acceptance surface. Supplement with focused Quota-interface tests using controlled time and scheduling for settlement, retry, rollover and lifecycle interleavings. The user confirmed these seams on 2026-10-24.
2. Good tests assert externally observable outcomes: durable totals, returned eligibility, protocol responses, account visibility and physical-session closure. Avoid asserting helper names, call counts, mutex layout or incidental SQL text. Use production composition instead of copying startup policy into test setup.
3. Reuse existing controlled authority/accounting adapters at the Relay seam. Concrete PostgreSQL is the persistence implementation; do not introduce a hypothetical interchangeable datastore to enable tests. Fault injection may control timing or commit acknowledgment while assertions remain at the module interface and durable outcome.
4. Verify endpoint attribution, both endpoints on one account, unattributed tails, concurrent sessions and observations arriving during settlement. Show that acknowledgment does not erase newer observations and effective usage never counts an in-flight batch twice.
5. With real PostgreSQL, cover successful settlement, known rollback, committed write with lost reply, repeated identical retry, conflicting payload, stale sequence after receipt replacement and restart. Assert one debit, atomic receipt/total persistence and inability to resurrect retired weeks or deleted accounts.
6. Use controlled time for the one-second target, configurable default and invalid deadline values, blocked persistence, failed-write/successful-read combinations, idle balance refresh, deadline enforcement independent of a hung worker and recovery before readmission. Test no fixed byte-overrun promise; verify full attributable overrun is recorded.
7. Exercise Monday crossings while observations, lock waits, retries and acknowledgments occur on either side. Verify processing-week attribution, independent pending/in-flight values per week, no rollover and healthy new-week continuation without unnecessary reauthentication.
8. Test quota cap equality, reduction and increase against recorded plus pending usage. Preserve session-cap close-all behavior. Verify revocation/suspension does not erase valid pending usage and that stale accounting completion cannot restore access.
9. Preserve HTTP/PostgreSQL/libp2p tests for uncertain revocation, suspension waiting on account-row locks, authentication racing invalidation, same-peer replacement across accounts at capacity, failed renewal, admission before guard waits and outer-deadline cleanup after installation.
10. Exercise deletion despite failed final flush; ambiguous settlement before finalization; finalization racing late writes; retained confirmed usage; fresh registration/import; and hidden-account cleanup. Keep journal-failure and recovery tests to prove that the accounting exception does not waive independent deletion safety.
11. Verify settlement during real circuit drain, final flush within the existing budget and forced shutdown. Restart tests distinguish durable totals from accepted loss of uncommitted observations and ensure no stale receipt becomes a fresh debit.
12. Prior art includes the current reporter attribution wire tests, lost-commit receipt reconciliation tests, account mutation consistency tests, security-mutation/admission interleaving tests, cross-transport replacement tests, deletion journal/re-registration tests, retention tests and stock-circuit drain tests. Adapt or replace prepaid-credit assertions rather than retaining an obsolete parallel test contract.
13. Verify presentation consistency, zero remaining allowance after overrun, history retention, configuration propagation, fresh schema creation, offline migration reset and recovery compatibility. Include stale query results in tests proving that presentation models cannot authorize admission.
14. Run the repository's routine verification and separate race profile for implementation, plus the real PostgreSQL tests needed for this contract. Record revision, environment and skipped checks; routine success alone does not establish database, native-runtime, provider, cluster or scale acceptance. This specification-writing task does not claim those checks ran.

## Out of Scope

- Event sourcing, authoritative settlement history, replay tooling, event schema evolution, messaging infrastructure and separate query databases.
- A new generic Relay Account access module, forwarding coordinator or interchangeable persistence abstraction.
- Hard byte caps, exact application-payload billing, per-connection reporter reconstruction or recovery of observations lost before persistence.
- Changes to grant-local termination policy, credential lifetimes, client transport selection, persistent concurrent sessions per Peer ID or the accepted single-session transport fallback.
- Device Network membership, durable Device Identity enrollment, per-device usage history or a relay protocol redesign.
- Multiple serving Relay Instances, distributed accounting coordination and full-scale capacity proof.
- Preservation of obsolete prepaid test accounting, deployment, data purge during specification work, live provider/cluster qualification or publication of a release.

## Further Notes

This specification consolidates architecture candidates 1 (complete Quota ownership), 2 (periodic settlement) and 3 (protected Auth/Relay operations), plus the 2026-10-24 decision to use CQRS with correct totals and safe retries rather than event sourcing. It supersedes prepaid funding, unused-credit and related cap-equality behavior in the earlier service contract only where explicitly described. Unchanged authentication, privacy, recovery and transport requirements remain in force.

The starting thoughts explored caller-managed balance checks and eventual policy polling. The subsequent accepted decisions give complete accounting to Quota and retain immediate protected account invalidation. Those accepted decisions govern implementation.

References: [agreed design](design.md), [CQRS assessment and decision](cqrs-event-sourcing-assessment.md), [starting thoughts](../thoughts.md), [domain glossary](../../GLOSSARY.md), [existing service contract](../managed-relay-service/spec.md), and [verification procedure](../../docs/verification.md). The companion Clipp repository's accepted ADR-0012 remains authoritative for client single-session transport fallback.

No production code, migration, deployment or data purge is performed by publishing this specification. The next planning step can split this ready specification into implementation tickets without reopening the settled product decisions.
