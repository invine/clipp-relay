# Explicit Resource Manager profile: candidates and a transport-capacity conflict

**Decision status, 2026-09-26:** Q338 in
[Define remaining operational Safety Limit defaults](../issues/16-define-remaining-operational-limit-defaults.md#fifth-round--q338q347-accepted-2026-09-26)
adopts the three scope tables below as provisional configuration. The original
research's “unapproved/candidate” wording records its status when researched,
not the current decision. Revised Q337 keeps budgets and the scale goal
unchanged and defers capacity tuning until code is ready; no immediate budget
revision or transport restriction is required. Capacity claims still require
validation. Numerical tables and source findings below are unchanged.

Research date: 2026-09-07. Supports [operational Safety Limits](../issues/16-define-remaining-operational-limit-defaults.md). Inspected the local **go-libp2p v0.49.0** module and its pinned **go-yamux/v5 v5.1.0** source. Tagged upstream links identify those primary sources; the web tool could not retrieve two GitHub pages, so this audit relies on the installed source, not a successful remote re-verification. No production code, benchmark, or accepted limit was changed. All subordinate numbers below are **unapproved engineering candidates**; accepted aggregate ceilings remain constraints.

## Material finding: 512 MiB cannot admit 5,000 WebRTC Direct connections

WebRTC Direct defines `maxReceiveMessageSize = 256<<10 + 1<<10` and `sctpReceiveBufferSize = 10 * maxReceiveMessageSize`. The listener configures that Pion receive-buffer ceiling and then reserves the same **2,631,680 bytes (2.509765625 MiB)** in its connection scope at `ReservationPriorityMedium`, before assigning the remote peer. The reservation stays with the connection scope rather than being released after setup. Outbound setup makes the same reservation. [Message size](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/stream.go#L42-L43), [buffer constant](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/transport.go#L72-L78), [inbound reservation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/listener.go#L222-L224), [outbound reservation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/transport.go#L366-L369).

Medium priority is 152. The actual threshold is `floor((priority + 1) * scopeMemoryLimit / 256)`, not the complete nominal budget. Thus it admits reservations only up to **153/256** of each applicable scope's memory ceiling. [Priority values](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/network/rcmgr.go#L138-L149), [threshold implementation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L105-L138).

| Arithmetic, ignoring every other reservation | Result |
| --- | --- |
| WebRTC connections under 512 MiB system memory | At most **121**, before other limits/use; not 5,000 |
| Reservations for 5,000 WebRTC connections | 13,158,400,000 bytes, approximately 12.255 GiB |
| Minimum system ceiling admitting that amount at medium priority | **22,016,669,282 bytes**, approximately **20.505 GiB**, before any other reservation |
| Minimum connection-scope ceiling for one such reservation at medium priority | 4,403,334 bytes, approximately 4.200 MiB |
| Four established WebRTC connections for one Peer ID | 10,526,720 reserved bytes; peer assignment transfers existing resources at `ReservationPriorityAlways` |

The peer-transfer distinction matters: the inbound reservation occurs before `SetPeer`; existing memory transfers to the peer with Always priority. Do not incorrectly apply the medium multiplier again to that transfer. Later peer-level reservations still use their own priorities. [Peer assignment](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/listener.go#L275), [resource transfer](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/rcmgr.go#L818-L841), [transfer priority](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L649-L661).

These are **accounting reservations and a configured receive-buffer cap**, not proof that each idle connection eagerly allocates 2.51 MiB of resident memory. Conversely, reducing accounting or raising it above the 2-GiB container would not prove real-memory safety. There is no supported production transport option or exported setting in this pinned package to reduce this constant: the production `With...` option found is `WithDialerVersion`; the receive-buffer value is an unexported constant used directly at both call sites. A custom Resource Manager that undercounts it would change the accounting contract, not tune the transport buffer. [Production transport options and fields](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/transport.go#L81-L180).

**Required decision/acceptance clarification:** the 5,000-session target cannot mean 5,000 simultaneous stock-WebRTC-Direct sessions under the accepted 512-MiB Resource Manager profile. The target's required transport mix, a budget revision, or transport implementation change must be explicit. No subordinate matrix can remove this arithmetic contradiction. Mixed-transport capacity still requires measurement.

## A second hidden reservation: yamux windows

The pinned yamux dependency reserves **256 KiB for every opened/accepted muxed stream**, before later receive-window growth. Go-libp2p supplies spans from the **peer scope**, so those muxer reservations count against peer/system memory, not merely the stream's application-buffer limit or relay-service budget. Twelve thousand simultaneous yamux circuits would require at least 24,000 such stream windows, about **5.859 GiB**, before other reservations; they cannot all fit 512 MiB. The accepted stream ceilings already intersect with memory limits and do not promise simultaneous saturation. [Dependency pin](https://github.com/libp2p/go-libp2p/blob/v0.49.0/go.mod#L32), [peer-scoped memory manager](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/muxer/yamux/transport.go#L41-L46), [initial window](https://github.com/libp2p/go-yamux/blob/v5.1.0/const.go#L179-L181), [outbound reservation](https://github.com/libp2p/go-yamux/blob/v5.1.0/session.go#L214-L221), [inbound reservation](https://github.com/libp2p/go-yamux/blob/v5.1.0/session.go#L857-L864).

## Complete candidate scope matrix, conditional on accepting the intersections above

Notation: tuples are **combined / inbound / outbound**. Memory is a configured ceiling, not a reserved allocation. `0` means **block all**, never inherit/default/unlimited. Every table row sets all eight ResourceLimits fields: connection tuple, stream tuple, FD, memory. Fields marked zero that the scope does not normally consume remain explicitly zero. Connection scopes do not aggregate the streams on that physical connection: streams have separate peer/protocol/service edges. [Scope construction](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/rcmgr.go#L516-L600), [assignment edges](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/rcmgr.go#L863-L962).

| Scope | Connections C/I/O | Streams C/I/O | FD | Memory |
| --- | --- | --- | --- | --- |
| System | 8,192 / 8,192 / 512 | 32,768 / 32,768 / 16,384 | 8,192 | 512 MiB |
| Transient, pre-peer connections and pre-protocol streams | 256 / 256 / 256 | 1,024 / 1,024 / 512 | 256 | 512 MiB |
| Peer default; no identity-specific overrides | 4 / 4 / 4 | 64 / 64 / 32 | 4 | 32 MiB |
| Each connection | 1 / 1 / 1 | 0 / 0 / 0 | 1 | 8 MiB |
| Each stream | 0 / 0 / 0 | 1 / 1 / 1 | 0 | 1 MiB |
| Allowlisted system and transient, each | 0 / 0 / 0 | 0 / 0 / 0 | 0 | 0 |
| Unknown service, service-peer, protocol, protocol-peer defaults, each | 0 / 0 / 0 | 0 / 0 / 0 | 0 | 0 |

Transient memory intentionally equals system memory to avoid inventing an additional lower setup-memory ceiling; it still does not admit 256 simultaneous stock-WebRTC setups. Per-peer 32 MiB permits the four-connection overlap in basic reservation arithmetic, but does not promise four fully saturated muxers or unlimited concurrent buffer growth.

For every service/protocol row below, **connections are 0/0/0 and FD is 0**. The same applies to their peer rows. The named custom services are proposed attribution labels, not new network protocols; their handlers must call `SetService` and reserve/release bounded application parsing/serialization buffers. Otherwise a configured service-memory limit cannot account for arbitrary Go allocations. [Service assignment](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/rcmgr.go#L919-L962), [accounting responsibility](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit.go#L1-L10).

| Service | Streams C/I/O | Memory | Service-peer streams C/I/O | Service-peer memory |
| --- | --- | --- | --- | --- |
| `libp2p.relay/v2` | 24,576 / 12,288 / 12,288 | 128 MiB | 32 / 32 / 32 | 2 MiB |
| `libp2p.identify` | 4,096 / 2,048 / 2,048 | 64 MiB | 8 / 4 / 4 | 1 MiB |
| `libp2p.ping` | 1,024 / 1,024 / 512 | 16 MiB | 4 / 4 / 2 | 256 KiB |
| `clipp.relay-auth` (proposed label) | 512 / 512 / 0 | 8 MiB | 8 / 8 / 0 | 256 KiB |
| `clipp.rendezvous` (shared proposed label) | 2,048 / 2,048 / 0 | 64 MiB | 8 / 8 / 0 | 2 MiB |

| Protocol | Streams C/I/O | Memory | Protocol-peer streams C/I/O | Protocol-peer memory |
| --- | --- | --- | --- | --- |
| `/libp2p/circuit/relay/0.2.0/hop` | 12,288 / 12,288 / 0 | 64 MiB | 32 / 32 / 0 | 1 MiB |
| `/libp2p/circuit/relay/0.2.0/stop` | 12,000 / 0 / 12,000 | 64 MiB | 32 / 0 / 32 | 1 MiB |
| `/ipfs/id/1.0.0` | 2,048 / 1,024 / 1,024 | 32 MiB | 4 / 2 / 2 | 512 KiB |
| `/ipfs/id/push/1.0.0` | 2,048 / 1,024 / 1,024 | 32 MiB | 4 / 2 / 2 | 512 KiB |
| `/ipfs/ping/1.0.0` | 1,024 / 1,024 / 512 | 16 MiB | 4 / 4 / 2 | 256 KiB |
| `/clipp/relay-auth/1.0.0` | 512 / 512 / 0 | 8 MiB | 8 / 8 / 0 | 256 KiB |
| `/clipp/rendezvous/1.0.0` | 1,024 / 1,024 / 0 | 32 MiB | 4 / 4 / 0 | 1 MiB |
| `/clipp/rendezvous/2.0.0` | 2,048 / 2,048 / 0 | 64 MiB | 8 / 8 / 0 | 2 MiB |

Names and normal upstream service attachments: [relay defaults](https://github.com/libp2p/go-libp2p/blob/v0.49.0/limits.go#L78-L106), [Identify IDs and service](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/identify/id.go#L38-L46), [Ping](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/ping/ping.go#L24-L49). All numbers in these two subordinate tables are candidates, not values mandated by those sources.

The candidate inventory permits Identify and Identify Push in both directions and inbound Ping/Auth/Rendezvous plus outbound STOP on existing connections. Outbound control streams do not authorize outbound transport dials. No AutoNAT dial-back service, AutoNAT v2, hole-punch service, DHT/pubsub, auto-relay/client relay transport, prototype protocol or unlisted stream protocol is implicitly included. Host construction must explicitly reconcile its automatic behaviors with this inventory and assert the registered protocols at startup; unknown-default blocking is not a substitute for disabling an unwanted background service. BasicHost automatically creates Identify; ordinary host construction also creates AutoNAT, and default options enable client relay transport unless overridden. [BasicHost setup](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/basic/basic_host.go#L196-L248), [AutoNAT construction](https://github.com/libp2p/go-libp2p/blob/v0.49.0/config/config.go#L646-L648), [default relay transport](https://github.com/libp2p/go-libp2p/blob/v0.49.0/defaults.go#L103-L106).

## Resolving configuration without hidden defaults

- Build an explicit fixed configuration against a zero/empty base, **not** `DefaultLimits.AutoScale()` or a populated `SetDefaultServiceLimits` base. `PartialLimitConfig.Build` copies existing per-service/protocol maps, so changing only fallback defaults does not erase lower built-in overrides. For example, start with an empty `ScalingLimitConfig`, call its `Scale(0, 0)`, then build the fully specified partial profile. Verify the resolved result through `ToPartialLimitConfig` before constructing the limiter. [Build/map semantics](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit_defaults.go#L466-L509), [resolved configuration and scaling](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit_defaults.go#L551-L627).
- In Go `ResourceLimits`, numeric zero means **DefaultLimit**, not BlockAll. Use `BlockAllLimit`/`BlockAllLimit64` or fully populated `BaseLimit.ToResourceLimits()` for the table's zero cells; JSON explicit zero has separate conversion semantics. [Limit values](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit_defaults.go#L119-L171), [conversion](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit.go#L105-L126).
- Keep the address allowlist empty and both allowlisted pools blocked. Explicitly use non-nil empty subnet/prefix concurrency lists and an empty, non-nil connection rate limiter; do not retain hidden per-IP limits or pass a nil pointer guessed to mean disabled. This is the accepted no-IP-cap/no-bypass policy. [Supported options](operational-budget-consistency.md#supported-ways-to-make-admission-policy-explicit).
- These scope counts intersect with stock circuit limits, post-security authentication admission, operation-rate gates, memory priorities, transport constants and OS/container limits. They provide no reserved control lane or fairness promise. The 512-MiB shared memory ceiling will bind much earlier than several advertised count ceilings for WebRTC/yamux workloads; the table deliberately does not conceal that limitation.

Implementation/release prerequisites remain: confirm the exact enabled host inventory and service tagging; account for all custom handler buffers; test the resolved limiter with connection replacement and concurrent control work; measure transport mix and idle/active memory; and resolve the explicit WebRTC/5,000-session conflict before claiming the acceptance target is feasible. The matrix is configuration-complete for the listed scopes, **not** performance-complete or approved.
