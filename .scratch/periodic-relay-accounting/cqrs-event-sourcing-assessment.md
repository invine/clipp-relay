# CQRS and event sourcing assessment

Date: 2026-10-23. Read-only investigation; no implementation or changes to the agreed design. Assumes “sqrc” means CQRS and the requested scope remains Clipp Relay's Relay, Quota and Auth modules.

## Recommendation

Include lightweight, in-process CQRS in the current refactor. Keep whole-application event sourcing separate. Decide **before writing the implementation spec** whether Quota needs an authoritative settlement history: selective event sourcing there is feasible now and avoids replacing its accounting persistence twice, but is not necessary merely to implement reliable periodic settlement.

This recommendation concerns module responsibilities and persistence. It does not require a command bus, broker, independent databases, or more runtime processes.

## Distinguish the choices

CQRS separates write models expressing domain operations from read models shaped for consumers. Microsoft explicitly documents separate models sharing one database and says messaging is optional. Thus commands such as account revocation and usage settlement can retain synchronous transactional correctness, while owner/admin usage queries return dedicated read representations. Merely renaming every method Command or Query provides little depth. [Microsoft CQRS](https://learn.microsoft.com/en-us/azure/architecture/patterns/cqrs)

For this assessment, event sourcing means persisted events are authoritative and selected state is reconstructible from them. It is different from a best-effort diagnostic log, an audit table alongside authoritative account rows, or an outbox that reliably delivers notifications. The distinction is whether replay is the supported way to reconstruct state. Event sourcing adds event evolution, replay, concurrency and retention obligations; Microsoft recommends selective use where historical reconstruction or auditability justifies the cost. [Microsoft Event Sourcing](https://learn.microsoft.com/en-us/azure/architecture/patterns/event-sourcing)

Replaying recorded changes and retaining current state are compatible. Replay must isolate external effects; rebuilding a model must not resend notifications or repeat live operations. [Fowler, Event Sourcing](https://martinfowler.com/eaaDev/EventSourcing.html)

## Concrete fit

| Area | Include now | Separate decision/build |
| --- | --- | --- |
| Quota | Complete commands for observed usage and settlement; presentation-oriented usage queries; stable settlement identity and deduplication | Authoritative settlement event history and rebuild tooling, if required |
| Auth | Protected complete account commands; distinct portal/admin reads | Reconstructing account policy and credentials from event streams |
| Relay | Explicit admission and closure operations; read-only live state where needed | Durable event sourcing of physical sessions: no current need |

The current design already puts aggregation, reconciliation and accounting health behind Quota's interface and guarded mutation completion behind Auth. CQRS reinforces that locality. Preserve those complete operations: two separate reads followed by installation would reopen the admission/invalidation race. A presentation read model must not become stale authority for admission. Sources: [agreed design](design.md), [Auth credential validation](../../internal/auth/relay.go), [Auth account guards and invalidation](../../internal/auth/auth.go).

## Strongest case for selective Quota event sourcing now

The existing weekly row stores a sequence and only the latest operation receipt; `fund` reconciles uncertainty before installing prepaid credit. That persistence must change anyway. A stable batch ID and an immutable account/week usage delta offer a natural settlement record. In one PostgreSQL transaction, insert the unique event and update the weekly total only on a new insert; a retry must verify that the same ID has the same payload. A synchronous projection is possible here: async projection lag is an optional architecture choice, not an unavoidable property of recording events. Sources: [current schema](../../internal/database/database.go), [funding and reconciliation](../../internal/quota/quota.go). The proposed transaction is an engineering recommendation, not existing behavior.

If the stream is authoritative, the weekly total becomes a rebuildable projection. Include settlement identity, account incarnation, observation week, byte delta, sequence/version as needed, and event schema version. A retained-usage opening balance on re-registration must also be reconstructible; usage delta events alone are insufficient. Deduplication receipts alone, with totals remaining authoritative and no replay contract, are a simpler valid alternative.

This is the best timing to add selective sourcing **if** the user wants explanations of how totals accumulated, rebuilding damaged projections, or future usage views. Neither the discussion nor the existing thoughts currently establishes a need for historical replay. Do not silently turn one weekly total into an indefinitely retained traffic history.

Cost illustration, not a benchmark: one nonempty event per continuously active account per second yields 86,400/day and 604,800/week/account. At 100 continuously active accounts that is 60,480,000/week. Idle ticks need no event; window crossing or extra flushes can change the count. The current cleanup keeps the current week and twelve preceding weekly windows, deleting older rows. Applying that retention period to second-level events needs an explicit storage/compaction decision. [Cleanup](../../internal/auth/cleanup.go)

## Why broader sourcing should be separate

- **Deletion and retention are real constraints.** Deletion removes identity/assignments, retains only confirmed current-week quota in a privacy-minimal form, and later erases account rows and credentials. Audit history expires after 180 days; ordinary quota history has its own cutoff. Keeping an account event history sufficient to restore erased state would conflict with these contracts unless deliberately redesigned. [Deletion](../../internal/auth/deletion.go), [cleanup](../../internal/auth/cleanup.go), [glossary](../../GLOSSARY.md).
- **Restore has an independent protection.** The Recovery Journal prevents an older database restore resurrecting deleted accounts. An event store restored with that same database does not replace the independent journal. Replay must apply the existing deletion/restore rules. [README](../../README.md), [journal and finalization](../../internal/auth/deletion.go).
- **Live effects remain live.** Relay Sessions are ephemeral; one process owns invalidation and closes connections after guarded authority detachment. An event log does not eliminate this ordering or make sockets replayable. Keep immediate in-process invalidation; an outbox becomes useful only if a genuine durable asynchronous consumer is introduced. [Glossary](../../GLOSSARY.md), [current composition](../../cmd/clipp-relay/main.go), [agreed design](design.md).
- **Accepted loss is unchanged.** Settlement events only preserve successfully committed batches. They cannot recover observations lost before commit and need not durably record every traffic callback. A late batch must never recreate a deleted account or debit its replacement. [Agreed behavior and obligations](design.md).

## Decision recorded 2026-10-24

The user confirmed that correct totals with safe retries are sufficient. Include lightweight CQRS in the current refactor, with authoritative weekly totals and bounded deduplication/reconciliation state. Do not introduce Quota event sourcing or a replay contract. Broader Auth/Relay event sourcing also remains outside scope. This decision is incorporated in the [agreed design](design.md); implementation has not started.

## Decision alternatives considered

Ask: **Do we need to rebuild Recorded Relay Usage from settlement history, or only maintain correct totals with safe retries?**

If totals suffice, specify CQRS plus periodic settlement now, retaining compact deduplication/reconciliation state. If replay matters, add a bounded Quota sourcing workstream to the same spec: authoritative events, atomic projection, duplicate/lost-reply tests, rebuild comparison, retained-usage opening state, deletion races, retention/compaction, and recovery qualification. Keep Auth/Relay sourcing out of that scope.

No tests were run: this is an architectural assessment grounded in source inspection and primary-source documentation.
