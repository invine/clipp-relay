# Evidence for initial quota and safety-limit experiments

Research date: 2026-09-05. Supports [Choose initial Quota Plan and Safety Limit defaults](../issues/12-choose-initial-quota-and-safety-limit-defaults.md). These are source facts and proposed experiments, **not approved defaults, a load-test result, or a production implementation**.

## What has actually been demonstrated

The accepted asset is `prototype/connection-scoped-relay-authentication` at `d95c6d0`, directory `prototype/relay-authentication/`. Its committed `README.md`, `harness/go/go.mod`, `harness/go/main.go`, and `harness/client.mjs` were inspected using `git show`; the prototype was not rerun for this note. The module pins Go 1.25.7 and go-libp2p v0.49.0. The [resolved prototype ticket](../issues/04-prototype-connection-scoped-relay-authentication.md) is the durable pointer to that primary asset.

- The recorded real probe delivered a 65,536-byte payload through stock Circuit Relay v2 over TCP, WebSocket, and WebRTC Direct. Its observed source/destination endpoint charges were respectively 68,683/68,748, 68,687/68,752, and 68,765/68,830 bytes. These are examples of protocol overhead, not a universal multiplier.
- A 16,384-byte payload with the same account at both endpoints produced 39,091 charged bytes. Do not deduplicate that account when simulating the accepted policy.
- A 131,072-byte application frame completed while the source crossed a 10,298-byte account threshold, after which its Relay Sessions were removed and connection aborted. The test sets the threshold to prior usage plus 8,192 bytes; 10,298 is the recorded run's resulting total, not a fixed setting. Reporter callbacks cannot refuse a pending read/write. This is evidence of asynchronous cutoff, **not** evidence that overshoot is bounded to one frame or 131,072 bytes under concurrency.
- Fixture settings were 4 MiB per account, four sessions per account, 2 MiB circuit data per direction, one-minute circuit duration, and five-minute reservations (`main.go:37–45,512–514`). They were chosen to exercise cases. Session authentication uses deliberately short test lifetimes. No durable credit allocator, PostgreSQL, 1,000-account workload, or 5,000-session ARM64 measurement exists in that asset. The [topology decision](../issues/06-define-distributed-quota-and-coordination-semantics.md) calls the latter an acceptance target still to prove.

## Pinned stock relay boundaries

The v0.49.0 module-cache source was inspected directly, with the official tagged upstream links below for reproducibility. `DefaultResources()` specifies:

| Resource | Stock value |
| --- | --- |
| Global simultaneous reservations | 128 |
| Reservation lifetime, including refresh | 1 hour |
| Reservations per Peer ID | One; deprecated field is set to 1 |
| Reservations per observed IP | 8 |
| Reservations per ASN | 32, with applicability caveat below |
| Simultaneous circuits touching each Peer ID | 16 |
| Per-circuit copy buffer | 2 KiB in each direction |
| Circuit wall-clock duration | 2 minutes |
| Circuit data | 128 KiB in **each direction**, not combined |

These are availability bounds, not account allowances. Use the returned initializer rather than the stale per-peer comment mentioning four. [Tagged resource definitions](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/resources.go#L7-L68).

The reservation constraints remove the old Peer ID entry when refreshing. They count the IP parsed from the connection address, and apply ASN lookup only to IPv6 addresses with a nonzero known ASN; IPv4 is not covered by the ASN constraint. Setting the IP/ASN maximum to zero does not disable it: the admission comparison rejects against zero. Matching a sublimit to the global reservation ceiling makes it nonrestrictive without a fork. [Tagged reservation constraints](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/constraints.go#L49-L104).

**Ingress warning:** WebSocket transport derives the remote multiaddr from the underlying connection's `RemoteAddr()`, not an HTTP forwarded-header identity. With a terminating reverse proxy, many WSS clients can therefore occupy one proxy IP bucket; stock eight-per-IP can block a deployment despite different accounts and client IPs. Do not claim client-IP fairness until actual source preservation or trusted proxy handling is established. This is an inference from the [WebSocket connection construction](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/websocket/conn.go#L37-L65) and reservation source above, not a measured OCI result.

Circuit admission checks both endpoint Peer IDs against `MaxCircuits`. The forwarding code independently wraps each direction in `io.LimitReader`, applies the circuit deadline, and reserves two copy buffers. A cap applies to a whole encrypted/multiplexed peer connection, not each Clipp application stream. The same source fixes relay control messages at 4,096 bytes, initial stream/STOP-handshake timeouts at one minute, and STOP connection establishment at 30 seconds; these are upstream constants, not administrator options exposed by `Resources`. [Tagged relay implementation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L28-L41), [admission/forwarding](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L260-L532).

Resource Manager is another independent ceiling. Stock libp2p autoscaling starts from discovered total memory divided by eight and available file descriptors divided by two, rather than a chosen 5,000-session contract. Relay service base limits are 256 streams and 16 MiB, increasing by those amounts per GiB of scaling memory; service-peer base is 64 streams and 1 MiB. Other system, connection and protocol scopes can reject sooner. Explicit coordinated limits are needed; raising reservations alone proves nothing about sustainable load. [Default manager](https://github.com/libp2p/go-libp2p/blob/v0.49.0/defaults.go#L108-L117), [service limits](https://github.com/libp2p/go-libp2p/blob/v0.49.0/limits.go#L78-L106), [scaling and system limits](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit_defaults.go#L630-L705).

## Current Clipp payloads: frame size is not transfer size

The relevant local Clipp core sources match commit `44ddde117c9badf2f2128d1e7acd85a80bb2a819`; unrelated working-tree edits were left untouched.

| Current application boundary | Limit and consequence |
| --- | --- |
| Live Clip | One protobuf body up to 256 KiB, plus length prefix, per stream. [Codec](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/protocols/liveClip.ts#L5-L29) |
| History | Each nonempty batch body up to 256 KiB; arbitrarily many batches in one snapshot stream; 30-second progress/idle timeout. [Codec](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/protocols/history.ts#L5-L74) |
| Membership | Up to 256 KiB per protobuf body. [Reconciliation](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/membership/reconciliation.ts#L9) |
| Pairing | 16 KiB frame body; Pairing Target also bounded to 16 KiB. [Protocol](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/pairing/protocol.ts#L3), [target](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/pairing/v2.ts#L6) |

Thus the stock 128 KiB circuit cap cannot deliver a maximum valid 256 KiB Live Clip even on a fresh circuit. Choosing exactly 256 KiB would also be insufficient once framing, the end-to-end secure handshake, muxing, and other traffic on that connection are included. The relay sees opaque forwarded bytes and cannot enforce a plaintext Clip body cap.

More importantly, increasing the circuit cap above one frame does **not** guarantee history completion. The sender emits a complete eligible snapshot in newest-first order, with no resume cursor or receiver inventory, and a reconnect starts another full snapshot. Already decoded earlier batches survive a later stream failure. However, repeated circuit cutoff at the same small byte/time limit can keep replaying the same prefix and never reach older suffix records. This is a source-based risk, not a reproduced failure. The existing unpinned history defaults are 10,000 Clips and 100 MiB; pinned Clips are outside those bounds. There is no equivalent total snapshot byte cap. [Snapshot and reconnect behavior](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/sync/historyReconciliation.ts#L69-L143), [receive/reconnect handling](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/sync/historyReconciliation.ts#L167-L261), [history policy defaults](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/history/store.ts#L11-L17).

## Proposed simulator inputs and honest outputs

Use a self-contained model to expose tradeoffs, not to assert benchmark capacity. The following are **experimental ranges**, deliberately including problematic cases:

| Input | Trial values | Question exposed |
| --- | --- | --- |
| Weekly account allowance | 256 MiB, 1 GiB, 5 GiB | How many relayed exchanges/history replays fit when each endpoint is charged? |
| Concurrent account sessions | 5, 10, 20 | Ordinary device headroom versus maximum resource use per account. |
| Account-local credit block | 64 KiB, 256 KiB, 1 MiB | Database allocation pressure versus credit stranded on a crash. |
| Circuit bytes per direction | Stock 128 KiB, then 1, 8, 128 MiB | Single-frame viability, repeated history prefixes, and exposure before circuit reset. |
| Circuit duration | 2, 10, 30 minutes | Reconnect churn versus large/slow-transfer completion. |
| Reservation lifetime | 10, 30, 60 minutes | Renewal traffic versus stale reservation lifetime; not a Relay Session extension. |
| Circuits per Peer ID | 4, 8, 16 | Device-network fanout versus total stream and buffer exposure. |
| Global session/reservation target | 5,000; optionally 6,000 headroom | Consistent configured ceilings; not demonstrated throughput. |
| IP/ASN sublimit | Stock values versus global ceiling | Shared proxy/CGNAT collision; do not simulate unverified original-client-IP knowledge. |

For credit calculations explicitly assume one serialized outstanding block **per account per weekly window**, bounded by remaining quota. Under that model, unconsumed reserved credit stranded by a crash is at most one block per funded account (smaller for a partial final block). With 1,000 funded accounts, 64 KiB/256 KiB/1 MiB blocks correspond to at most 62.5/250/1,000 MiB stranded per crash. This is a proposed allocation-model bound, not a bound on reporter or network overshoot. Prefetching a second block or allocating per session changes it. Keep durable reserved allowance and actual reporter-observed bytes distinct in the model; the persistence ticket must settle their reconciliation.

Approximate block-allocation rate is aggregate **charged endpoint bytes per second / block size**, plus partial/idle allocations. At an assumed 100 MiB/s charged rate, the three blocks imply approximately 1,600/400/100 allocations per second; no assertion is made that PostgreSQL or the relay sustains that rate. Direct traffic consumes zero relay bytes. A relayed payload contributes at least its size to each participating endpoint; same-account endpoints both charge that one account. Model actual protocol overhead as an explicit scenario input, not a fitted universal percentage.

With `S` distinct active Peer IDs and cap `C` circuits per Peer ID, there are at most `floor(S*C/2)` simultaneous two-ended circuits before tighter global Resource Manager limits. With 5,000 peers and 16 circuits each, that is 40,000 circuits and about 156.25 MiB of the stock two 2-KiB copy buffers alone. This is not total process memory: connection, muxer, security, WebRTC, kernel, handler and database allocations remain outside that arithmetic. Idle reservations do not imply active circuits.

Include a maximum-frame scenario, a 100-MiB full-history scenario, a shared-account exchange, quota exhaustion with configurable in-flight bytes, process crash with stranded credit, weekly reset with old-window credits invalidated, and shared-proxy reservations. Show incomplete transfers explicitly. Keep in-flight overshoot **unknown/measured separately**; never label the block size or 2-KiB copy buffer as its proven maximum.

Authentication timeout, request rates, drain grace, Resource Manager memory/FD budgets, and soft/hard overload thresholds need measured transport latency and actual OCI ARM64 pod sizing before they can be justified. A simulator can vary them but cannot select safe production values. Subsequent acceptance work must test 5,000 simultaneously authenticated sessions, reconnect/renewal storms, concurrent saturated circuits, maximum valid frames, large history completion, slow peers, backend loss and recovery, proxy source-IP behavior, and observed account-cutoff overshoot on all three transports.
