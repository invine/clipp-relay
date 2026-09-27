# Operational budget consistency check

Research date: 2026-09-07. Supports [operational Safety Limits](../issues/16-define-remaining-operational-limit-defaults.md), following [admission facts](operational-admission-facts.md). Checked pinned go-libp2p v0.49.0 module-cache source and official Go documentation. These are candidate-budget checks, not accepted configuration, a complete scope derivation or benchmark results.

## Candidate arithmetic

**Subsequent transport check:** the arithmetic below does not establish that
the accepted memory budget supports the acceptance workload. Pinned WebRTC
Direct reserves about 2.51 MiB per connection at medium priority, making the
512-MiB system budget incompatible with 5,000 WebRTC connections. See Q336 and
its source calculation in [the operational-limits ticket](../issues/16-define-remaining-operational-limit-defaults.md#newly-verified-sizing-conflict-behind-q336).
Do not use “numerically plausible” below as evidence that the transport memory
requirements fit; final sizing is pending an explicit capacity decision.

| Candidate | Consistency result |
| --- | --- |
| System connections 8,192 total / 8,192 inbound / 512 outbound | Valid intersecting ceilings: inbound plus outbound may not exceed 8,192. This is not 8,704 slots and does not reserve 512 outbound slots. |
| Transient connections 256 | A shared pre-peer establishment ceiling, not an additional 256 outside system capacity. Directional/FD/memory limits also need explicit alignment. It does not count the complete post-peer/pre-account-authentication interval. |
| Peer connections 4 | Allows limited replacement overlap within a peer's aggregate scope, but only if global and directional capacity remains. It does not reserve replacement slots or grant four Relay Sessions. |
| System streams 32,768; relay service 24,576 | Valid nested totals. If relay service is full, 8,192 system stream slots remain arithmetically available for other services, subject to all other scopes. This is not a priority guarantee. |
| HOP inbound 12,288; STOP outbound 12,000 | Can constrain successful active circuits to at most 12,000 using existing stream admission, provided combined/directional/service limits are consistent and STOP is the relay's circuit leg. See headroom caveat below. |
| Peer streams 64 | Counts all peer streams across that peer's connections, not 64 per connection. Protocol/service-peer limits can reject sooner. |
| Resource Manager memory 512 MiB; `GOMEMLIMIT=1536MiB`; container budget 2 GiB | Numerically plausible nested/overlapping budgets, not additive allocations or proof against OOM. |
| Resource Manager FD 8,192; process soft `nofile` at least 16,384 | Leaves arithmetic OS descriptor headroom, not a guaranteed 8,192-descriptor reserve. Verify the actual running-process limit and non-libp2p descriptor use. |

Source for total/directional intersection and per-scope accounting: [resource checks](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L168-L296), [scope membership](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/rcmgr.go#L784-L966). These table results are arithmetic/inferences, not measured capacity.

## STOP cap: useful headroom, not reserved control admission

Each established circuit occupies an inbound HOP and an outbound STOP stream. The relay calls `NewStream(..., ProtoIDv2Stop)` and handles its failure without forwarding that circuit, so a STOP outbound cap can impose an upper bound without adding a new custom circuit-protocol gate. STOP setup streams count too: 12,000 is an upper bound, not a guaranteed count of completed circuits. [Relay HOP/STOP implementation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L359-L405).

At 12,000 settled circuits, the arithmetic is 24,000 relay-service streams, leaving 576 below the proposed service total; HOP admits only 288 more inbound streams before its proposed 12,288 ceiling. Full HOP plus full STOP is 24,288, still 288 below the service total. Both protocol **combined** limits and the service/system directional limits must also permit those values. Raising only `StreamsInbound` or `StreamsOutbound` while inheriting a smaller combined limit will not work. [Directional/total checks](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L168-L205), [upstream protocol defaults](https://github.com/libp2p/go-libp2p/blob/v0.49.0/limits.go#L78-L106).

**Important limitation:** the 288 HOP slots are shared by reservation requests, pending CONNECT requests and malformed/slow HOP traffic. New CONNECT requests occupy HOP resources before the relay attempts STOP. Therefore STOP's lower ceiling prevents established circuits alone exhausting HOP capacity, but does not reserve slots specifically for reservation renewals or guarantee their progress during a CONNECT flood. Call it control headroom under settled load, not control priority. Guaranteed class-specific admission would require further application-level separation/scheduling, beyond this numeric proposal. [HOP dispatch ordering](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L137-L183).

## Memory and FD interpretation

Resource Manager tracks explicit resource reservations; much of that memory is also Go-managed memory governed by `GOMEMLIMIT`. Do not sum 512 MiB and 1,536 MiB as independent consumption. The Go limit is soft and excludes memory sources outside runtime management; the proposed 512 MiB difference below 2 GiB is safety margin, not a hard guarantee. GC may exceed its limit to avoid thrashing. Shared-container/Pod terminology must be exact: a limit on the relay container is not automatically a common budget for every sidecar. [Resource accounting model](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/README.md), [Go memory-limit guidance](https://go.dev/doc/gc-guide#Memory_limit).

TCP/WebSocket connection scopes charge an FD; WebRTC uses shared UDP infrastructure and does not charge one per connection. Database, HTTP, log and other sockets/files can be outside Resource Manager's accounting. A larger process soft limit is necessary headroom, not evidence of a complete FD bound. Startup should verify the real limit; deriving transport mix and other descriptor demand remains follow-on work. [Connection FD accounting](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L229-L241), [WebRTC scope](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/listener.go#L167), [soft-limit discovery](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/sys_unix.go).

## Supported ways to make admission policy explicit

- `libp2p.ConnectionManager` accepts a `NullConnMgr`; that manager's trimming methods do nothing. Resource Manager and application admission remain separate, active controls. This is simpler than relying on peer protection while an emergency trim path can ignore it. [Connection-manager option](https://github.com/libp2p/go-libp2p/blob/v0.49.0/options.go#L246-L259), [null manager](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/connmgr/null.go).
- Resource Manager subnet concurrency defaults can be replaced with **non-nil empty slices** via `WithLimitPerSubnet`; `nil` preserves the defaults. `WithNetworkPrefixLimit` has the same nil-versus-empty behavior for prefix overrides. Keep the address allowlist empty if it must not add an alternative resource pool. [Concurrency options](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/conn_limiter.go#L75-L104).
- `WithConnRateLimiters` can take an explicit empty `rate.Limiter`, or one with only the intended global limit: zero RPS means no rate limiting and empty subnet/prefix rules introduce no IP-based limits. Do **not** pass a nil pointer as a guessed disable flag; `OpenConnection` invokes `Allow`. Source-address-verification rules are derived separately from configured concurrency prefixes/subnets, so review the resolved option set, not just one knob. [Connection-rate option](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/conn_rate_limiter.go#L40-L57), [rate semantics](https://github.com/libp2p/go-libp2p/blob/v0.49.0/x/rate/limiter.go#L40-L137), [verification-rule construction](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/conn_limiter.go#L299-L342).

## Still-required detailed derivation

Explicitly resolve system/transient stream directions, transient FD/memory, peer directions/FD/memory, per-connection and per-stream memory, relay service directions/memory, HOP/STOP combined and peer limits, other enabled protocol/service scopes, and any allowlist budgets. Retained defaults can reject far below proposed totals. Also account for application post-peer authentication queues, transport fixed handshakes, peer circuit limits, reservation counts/IP/ASN settings and service request-rate controls. A 512 MiB system reservation budget alone does not enlarge lower service memory budgets. [Full default hierarchy](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/limit_defaults.go), [relay service/protocol defaults](https://github.com/libp2p/go-libp2p/blob/v0.49.0/limits.go#L78-L106).

No hard contradiction was found in the candidate totals. Their interpretation must retain the caveats above; validation still needs a fully resolved configuration and transport-specific saturation/replacement/renewal tests, not only arithmetic.
