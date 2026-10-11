# Prototype the structured logging contract

Type: prototype
Status: resolved
Assignee: Codex (this chat)
Blocked by: 01

## Question

What exact event names, safe outcome and reason enums, correlation lifetime, log levels, rate limits and observation freshness rules make the agreed journeys diagnosable without disclosing identities or creating an unbounded diagnostic stream? Show sample JSON rows for ordinary success, refusal, dependency failure, recovery and unknown state. Have the human react to the examples before fixing the contract. Metric endpoints, collectors and metric snapshots are outside the narrowed scope. Link any prototype asset from this ticket.

## Comments

Use existing accepted stdout, audit, metric, private endpoint, and overload constraints. A portal login success and a relay reservation success need distinct meanings; a server event must not claim device-side acceptance or Clip delivery.

- Human verdict on 2026-10-11: remove metric work from this effort; retain the
  proposed stdout limits and observation freshness rules. The prototype and
  contract were revised to logging only in response.

- Start from the [settled operator journeys](01-decide-operator-diagnostic-journeys.md#answer), including the explicit choice against a periodic stdout heartbeat. The [working specification](../spec.md) collects those requirements but remains draft until this contract is settled.

- 2026-10-11: Claimed in this chat. The human reviewed a synthetic interactive
  prototype and corrected its scope. The durable prototype and local preview
  pointers are recorded in the answer below.

## Answer

Resolved through the live prototype review on 2026-10-11. Adopt the structured
logging proposal with the human's scope amendment: metric endpoints, metric
families, metric scrape snapshots and collector integration are outside this
effort. The limits and freshness policy were explicitly accepted. The revised
prototype removes metric output and preserves logging semantics without claiming
exact traffic-frequency measurements.

The [accepted logging contract](../logging-contract.md) and
[fixed logging catalog](../logging-catalog.json) are the decision assets. The
[specification](../spec.md) compiles them with the settled operator journeys.

### Log shape and meaning

Use fixed `time,level,msg,event,outcome,reason` fields and only the typed optional
fields defined by the contract. Static wording and safe reason enums replace
arbitrary error/request text. Request and physical-connection IDs are separate
server-generated 128-bit random values. Never use credentials, OAuth state,
cookies, caller IDs, stable account/device identifiers or an identity lookup to
correlate the logs. Stock aggregate callbacks omit session IDs and use unknown
transport when their source does not provide a reliable association.

The catalog fixes 46 operation categories and their permitted outcome/reason
dimensions, plus fixed observation and loss-summary control events. It defines
finite rate-limit state rather than allowing caller-created categories. Individual
durations may accompany an observed operation; there is no histogram or traffic
gauge in stdout. Initial observation and later recovery have distinct meanings.

### Accepted limits and freshness

- Activity: 20 rows/sec, burst 40; each fixed tuple: 1 row/sec, burst 5.
- Reserved lifecycle/observation/ERROR budget: 4 rows/sec, burst 8.
- One writer with a non-blocking 256-record queue; at most 2 KiB per record.
- Best-effort loss summary at most once per 10 seconds, only if rows were lost;
  separate rate and queue counts, no correlation lists or periodic status row.
- Shutdown flush waits at most 100 ms. Serving paths never wait for stdout.
- Diagnostic observations: 60-second cadence, 5-second deadline, immediate
  invalidation on failed observation, stale after 180 seconds without success.
  Preserve the quota clock's existing stricter timing and authority.
- Unknown/unavailable/failed/stale observations never manufacture a current
  numeric value or imply healthy infrastructure. Only state and accepted
  threshold transitions produce diagnostic rows; unchanged observations stay quiet.

Finite internal logger counters support loss summaries only. A suppressed row,
failed writer or process exit can lose individual outcomes and pending summaries.
Logs are not a complete event history. The previous proposed metric counter
guarantee is withdrawn with metric work removed from scope.

### Prototype primary source and demonstrated cases

Selected repository: `clipp-relay`. Durable branch:
`codex/prototype-relay-logging-contract`, commit
`218be1e896ecb0143c24726b8d79fc943418ea3f`.
The shareable HTML and supporting proposal live at
`internal/observability/prototype/` in that commit. No production runtime files
were changed by this branch.

Local standalone review copy:
[Relay logging contract prototype](/private/tmp/relay-logging-contract-preview/signal-contract.prototype.html).
If the temporary copy is absent, recover the artifact from the durable branch;
the final contract and catalog above remain in the canonical tracker.

Manual browser walkthroughs demonstrated distinct authentication/reservation
outcomes; 100 refusals limited to five rows plus a loss summary; a blocked writer
holding 256 rows with further loss; and failed observations omitting a stale value.
These are synthetic design demonstrations, not a load qualification or runtime
verification. No tests were added to the throwaway prototype.

### Handoff

The signal-design decisions are settled. Refine the preliminary execution
outlines through `to-tickets` into reviewed implementation slices before
`implement-spec`. This resolution does not implement those slices or close the
full observability release ticket.
