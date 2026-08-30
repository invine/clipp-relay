# Go libp2p relay compatibility

Research date: 2026-08-30

## Question

What Go libp2p stack and extension boundary can provide Clipp's required Circuit Relay v2 service, current transports and protocols, connection-scoped Relay Authentication, account-aware quota metering, and forced termination while staying compatible with the current JavaScript runtimes?

## Conclusion

Use `github.com/libp2p/go-libp2p` **v0.49.0** as the initial compatibility baseline. It contains the required TCP, WebSocket, WebRTC Direct, Noise, Yamux, Circuit Relay v2, Identify, Ping, resource-manager, and Prometheus instrumentation packages. The release is current as of this research and its module requires Go 1.25.7. Pin the version rather than following `master`, because go-libp2p is still pre-v1 and Clipp will depend on relay internals that can change. [The v0.49.0 release is commit `5c7e6ec`](https://github.com/libp2p/go-libp2p/releases/tag/v0.49.0), and [its module declares Go 1.25.7](https://github.com/libp2p/go-libp2p/blob/v0.49.0/go.mod#L1-L4).

The stock `p2p/protocol/circuitv2/relay` service implements the same standardized wire protocol Clipp uses, but it is **not sufficient unchanged** for the agreed Relay Account semantics. Its ACL receives Peer IDs and multiaddrs, not the concrete `network.Conn`; its metrics tracer reports only global byte increments; and its circuit streams/copy loop are private. Therefore it cannot reliably:

- require authentication on the exact connection that opens HOP or Rendezvous;
- attribute every forwarded payload byte to the distinct participating Relay Accounts;
- stop one circuit when either account exhausts quota; or
- retain handles for targeted circuit termination.

The implementation-ready direction is a **small, pinned Clipp-owned adaptation of the v0.49.0 relay service**, preserving the standard HOP/STOP protobufs, protocol IDs, status codes, reservation vouchers, and resource-manager integration while adding explicit policy/circuit hooks. Keep the adaptation narrow and diff it against each upstream upgrade. The connection-scoped-authentication prototype must validate this seam before the specification treats it as final.

No HOP or STOP wire extension is needed. Relay Authentication remains a separate Clipp protocol on the already Noise-authenticated connection, and unmodified Circuit Relay v2 remains the data-plane protocol after authentication.

## Current Clipp compatibility surface

Clipp currently resolves to js-libp2p 3.1.2 and the following relevant packages: Circuit Relay v2 4.1.2, Noise 17.0.0, Yamux 8.0.1, Mplex 12.0.10, WebSockets 10.1.2, TCP 11.0.9, WebRTC 6.0.10, Identify 4.0.9, and Ping 3.0.9. These are declared in [Clipp's root package manifest](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/package.json#L3-L23) and confirmed at those exact installed versions by `package-lock.json` in the researched checkout.

The shared node configuration establishes the actual wire surface:

| Concern | Current Clipp behavior | Go compatibility consequence |
|---|---|---|
| Relay | Uses Circuit Relay v2 client transport and obtains a reservation by listening on `<relay>/p2p-circuit`. | Preserve `/libp2p/circuit/relay/0.2.0/hop` and `/stop` exactly. |
| Security | Offers only `/noise` for ordinary TCP/WebSocket connections. | Configure Go Noise explicitly. WebRTC Direct performs its specified Noise handshake over its negotiated data channel. |
| Multiplexing | Offers Yamux first, then Mplex. | Go Yamux alone has a mutual protocol with every current client. Mplex is not required for compatibility. |
| WebSocket | All runtimes configure WebSockets. | Serve WS internally; advertise WSS when TLS terminates at Ingress. |
| TCP | Enabled by Electron only. | Expose raw TCP and let Noise provide libp2p security. |
| WebRTC Direct | Configured in the shared node and explicitly enabled by Electron and Android. Browser runtimes can dial it. | Expose UDP with the complete `/webrtc-direct/certhash/...` address returned by the live Go listener. |
| Identify and Ping | Both services are installed in every shared Clipp node. | Keep Go's built-in Identify and Ping enabled. |
| Rendezvous | Uses the Clipp-specific `/clipp/rendezvous/1.0.0`, not the standard libp2p Rendezvous protocol. | Implement the existing Clipp JSON exchange exactly and gate it by Relay Authentication. |

The supporting code is in Clipp's [`createClipboardNode`](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/node.ts#L56-L100), [WebRTC setup](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/node.ts#L109-L173), and [security/muxer/service configuration](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/node.ts#L195-L253). The current engine explicitly dials relays and then creates missing circuit listeners, as shown by its [startup sequence](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/engine.ts#L210-L224) and [reservation code](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/engine.ts#L1297-L1345).

One current-runtime asymmetry must be carried into the prototype: the shared node defaults relay reservations off in browser-document runtimes. Android overrides that default to `true`, but the Chrome offscreen runtime does not. Electron therefore reserves by default, Android reserves explicitly, and the current extension may dial through a relay without maintaining its own reservation. See the [browser-dependent default](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/node.ts#L80-L89), [Android override](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/apps/android/src/client.ts#L245-L259), and [extension configuration](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/apps/extension/src/offscreen.ts#L79-L95). The client-integration decision must state whether managed-relay rollout intentionally changes extension reachability.

## Exact Go baseline

Construct a deliberately narrow host rather than accept every go-libp2p default:

- module: `github.com/libp2p/go-libp2p v0.49.0`;
- transports: `p2p/transport/tcp.NewTCPTransport`, `p2p/transport/websocket.New`, and `p2p/transport/webrtc.New`;
- ordinary-transport security: `p2p/security/noise.New`, protocol `/noise`;
- stream multiplexer: `p2p/muxer/yamux.DefaultTransport`, protocol `/yamux/1.0.0`;
- services: the Clipp-owned Circuit Relay v2 adaptation, built-in Identify, built-in Ping, `/clipp/relay-auth/1.0.0`, and `/clipp/rendezvous/1.0.0`;
- standard relay client transport: disabled on the dedicated Relay Instance unless a later requirement needs the Relay Instance itself to reserve on another relay;
- resource manager: enabled with explicit service/protocol limits;
- metrics: a dedicated Prometheus registry exposed through the application's `/metrics` HTTP handler; no Prometheus Operator is required.

Go v0.49.0's defaults prove that these are first-class components: TCP, WebSocket and WebRTC are in the default transport set; Noise is in the default security set; and Yamux is the default muxer. The defaults also include QUIC, TLS, and WebTransport, which Clipp does not require for this relay, so an explicit transport/security list reduces exposed behavior. See [v0.49.0 `defaults.go`](https://github.com/libp2p/go-libp2p/blob/v0.49.0/defaults.go#L26-L51).

Creating a BasicHost installs Identify unconditionally and Ping when it is not disabled. See the [BasicHost Identify and Ping setup](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/basic/basic_host.go#L189-L207) and [Ping setup](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/basic/basic_host.go#L282-L295).

### Transport addresses

The three public address shapes returned by Relay Discovery should be:

```text
/dns4/<instance-host>/tcp/<wss-port>/wss/p2p/<relay-peer-id>
/dns4/<instance-host>/tcp/<tcp-port>/p2p/<relay-peer-id>
/ip4/<public-ip>/udp/<udp-port>/webrtc-direct/certhash/<hash>/p2p/<relay-peer-id>
```

DNS may replace the IP portion of the WebRTC address only if the selected go-multiaddr/WebRTC versions accept and advertise that form; the compatibility test should use the exact multiaddr emitted by the live transport and rewrite only the externally mapped host/port. The certhash must never be synthesized by the Coordinator.

A TLS-terminating Ingress may accept the public `/wss` endpoint and proxy a plaintext WebSocket upgrade to a `/ws` listener in the pod. The libp2p addressing specification distinguishes `/ws` from TLS-encrypted `/wss`, and the Go WebSocket transport can alternatively terminate TLS itself through `WithTLSConfig`. See the [libp2p addressing specification](https://github.com/libp2p/specs/blob/master/addressing/README.md#websocket) and [Go WebSocket package](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/p2p/transport/websocket).

WebRTC Direct is supported by the in-tree Go transport and uses UDP, a certificate hash in the multiaddr, and its specified Noise binding. The installed Clipp `@libp2p/webrtc` 6.0.10 source identifies its handshake as `libp2p+webrtc+v1/`. Go v0.49.0's listener accepts both this original SDP-munging handshake and the new v2 handshake, while Go dialing defaults to v1. That makes it an appropriate listener for the current client, but this must be exercised in the interop matrix rather than assumed from compilation. See the [Go WebRTC package documentation](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/p2p/transport/webrtc) and [WebRTC Direct specification](https://github.com/libp2p/specs/blob/master/webrtc/webrtc-direct.md).

## Circuit Relay v2 compatibility

Circuit Relay v2 standardizes two streams:

```text
/libp2p/circuit/relay/0.2.0/hop
/libp2p/circuit/relay/0.2.0/stop
```

A destination first sends HOP `RESERVE` and keeps its relay connection alive. A source later sends HOP `CONNECT`; the relay verifies a reservation, opens STOP to the destination, and after both status handshakes bridges the two streams. The endpoints then secure and multiplex the relayed connection as they would another transport. This is the behavior Clipp's JS Circuit Relay v2 client expects and is specified in the [Circuit Relay v2 protocol](https://github.com/libp2p/specs/blob/master/relay/circuit-v2.md#interaction).

Go's relay implementation uses those exact protocol IDs and standard protobuf messages. It already supplies:

- reservation vouchers and expiry;
- global reservation, per-peer circuit, IP, and ASN safety constraints;
- standard `PERMISSION_DENIED`, `NO_RESERVATION`, `RESOURCE_LIMIT_EXCEEDED`, and other statuses;
- duration/data limits carried in the standard relay response;
- resource-manager scopes for relay streams and buffers; and
- aggregate Prometheus-compatible relay metrics.

The standard resource options are service-global or Peer-ID-oriented, not Relay-Account quotas. Their definitions are in [v0.49.0 `resources.go`](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/resources.go#L7-L43). Retain these as Safety Limits underneath the account policy; do not substitute them for the agreed weekly and concurrent account quotas.

## Extension-hook audit

### What can be used unchanged

1. **Custom protocol handlers.** `host.SetStreamHandler` receives a `network.Stream`; its `Conn()` exposes the Noise-authenticated remote Peer ID and a process-unique connection ID. This is sufficient to implement `/clipp/relay-auth/1.0.0` and `/clipp/rendezvous/1.0.0` and to keep ephemeral state keyed by `network.Conn.ID()`. See the [Host handler API](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/host/host.go#L47-L58) and [Conn/Stream APIs](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/core/network).

2. **Connection lifecycle notifications.** `network.NotifyBundle` exposes `ConnectedF` and `DisconnectedF`, allowing deterministic removal of Relay Session state when a connection closes. See [the v0.49.0 Notifiee interface](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/network/notifee.go#L8-L46).

3. **Forced Relay Session termination.** `network.Conn.CloseWithError` closes the connection and therefore every stream on it. Error codes are best effort; transports that cannot carry a code behave like `Close`. `Conn.ID()` uniquely identifies the connection for the current Relay Instance process. See [the v0.49.0 Conn interface](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/network/conn.go#L64-L99).

4. **Forced stream termination.** `network.Stream.ResetWithError` or `Reset` closes both ends of a captured HOP/STOP stream. This is enough to terminate an individual relayed circuit once the relay implementation exposes and retains both stream handles. See [the Stream interface](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/network/stream.go#L12-L38).

5. **Protocol status compatibility.** The upstream relay's existing protobuf reader/writer, status responses, STOP handshake, voucher construction, resource spans, and cleanup logic should be retained with minimal changes. Its core handler is visible in [v0.49.0 `relay.go`](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L137-L181).

### What is insufficient

1. **`ConnectionGater` cannot perform Relay Authentication.** It can allow or deny before accept, after Peer-ID security, or after upgrade, but it has no application-token exchange and no opportunity to await an auth protocol stream. It remains useful for transport/IP safety policy, not Relay Account authentication. See [the official ConnectionGater lifecycle](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/core/connmgr#ConnectionGater).

2. **`relay.WithACL` is not connection-scoped.** `AllowReserve` receives only `(peer.ID, multiaddr)` and `AllowConnect` receives `(source peer.ID, source multiaddr, destination peer.ID)`. If one Peer ID has an authenticated connection and a second unauthenticated connection, the ACL cannot tell which connection supplied the HOP stream. This directly conflicts with the Relay Session definition. See [the ACL interface](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/acl.go#L9-L17) and [where it is called](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L183-L202).

3. **The stock tracer cannot implement account traffic charging.** `BytesTransferred` receives only an integer count, with no source, destination, stream, circuit, or connection context. It is called after successful writes in the private copy loop. See [the MetricsTracer interface](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/metrics.go#L106-L127) and [the copy loop](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L557-L592).

4. **Circuit lifecycle is private.** The HOP and STOP streams are local variables in `handleConnect`, the bridging goroutines are private, and `Relay.Close` is the only exported shutdown method. Stock code can stop the entire relay but cannot select a circuit or account. See [circuit creation and bridging](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L258-L378) and [the bridge startup](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go#L440-L484).

5. **Resource Manager is not an account quota ledger.** It constrains libp2p resource scopes such as system, service, protocol, peer, connection, and stream. Relay Accounts deliberately do not own Peer IDs, so those scopes cannot express two-sided distinct-account charging. Keep Resource Manager enabled for process protection, while the Clipp quota coordinator remains a separate policy layer. The upstream [resource-manager documentation](https://github.com/libp2p/go-libp2p/tree/v0.49.0/p2p/host/resource-manager) describes its scope-based role.

## Required Clipp-owned relay seam

Copy or narrowly fork the v0.49.0 `p2p/protocol/circuitv2/relay` package into an internal package and introduce an application-owned policy interface at these exact points:

1. **Before HOP dispatch:** look up `stream.Conn().ID()` and reject an unauthenticated or expired Relay Session with the standard `PERMISSION_DENIED` response.
2. **Before reservation commit:** atomically acquire/renew the Relay Account's reservation quota, then associate the reservation with the exact authenticated Relay Session for its finite lifetime. The durable system still stores no device-to-account ownership.
3. **Before circuit admission:** resolve the source Relay Account from the HOP connection and the destination Relay Account from the live reservation/session, reduce them to a distinct account set, and atomically acquire one circuit slot for each account.
4. **When bridging starts:** create a process-local circuit record containing a random circuit ID, source/destination Peer IDs, distinct account IDs, HOP/STOP streams, start time, and cleanup-once guard.
5. **After every successful payload write:** debit the bytes against each distinct participating account through the pre-agreed block allocator. Do not meter HOP/STOP protobuf handshakes. Share one circuit budget object across both copy directions so A-to-B and B-to-A bytes contribute to the same account totals.
6. **When a quota block cannot be renewed, an account is suspended, an administrator terminates the circuit, or the circuit Safety Limit expires:** reset both HOP and STOP streams, run cleanup exactly once, and release both accounts' circuit slots.
7. **When a Relay Session is terminated:** call `Conn.CloseWithError`, remove it on `DisconnectedF`, revoke any reservation tied to it, and let the circuit registry reset all affected circuits.

The adaptation must retain upstream tests and add a mechanical upgrade check against v0.49.0. Do not rewrite the Circuit Relay protobuf or reservation voucher implementation.

An alternative upstream contribution could add connection-aware admission and circuit-observer interfaces to go-libp2p. That is desirable long term, but it cannot be a first-release dependency because no such public hooks exist in v0.49.0.

## Relay Authentication ordering

Relay Authentication occurs only after libp2p transport setup has cryptographically established the remote Peer ID. The server should register `/clipp/relay-auth/1.0.0` and keep a process-local state machine keyed by `network.Conn.ID()`:

```text
connected/unauthenticated -> authenticated(account, token expiry) -> closed
```

The auth handler validates the Clipp Relay token, records the Relay Account against the exact connection, applies the agreed same-Peer-ID account-switch rule atomically, and closes superseded connections. HOP and Clipp Rendezvous consult this exact-connection state. Identify and Ping may remain available before account authentication, subject to Resource Manager and request-rate Safety Limits; they expose ordinary libp2p metadata but no account capacity.

The client integration must change its current ordering. Today it dials, immediately asks the Circuit Relay transport to listen/reserve, and starts Rendezvous. The revised flow must:

1. establish the selected Relay Instance connection;
2. authenticate that exact connection;
3. only after success request the reservation and use Clipp Rendezvous; and
4. authenticate every replacement physical connection before allowing protected streams.

A `connection:open` callback alone is not enough if reservation startup races it. The prototype must prove that js-libp2p's reservation is made on the same `Connection` that completed Relay Authentication, or introduce an explicit client-side connection/auth barrier around the reservation operation.

## Mplex decision

Do **not** include Mplex in the initial Go Relay Instance merely because Clipp currently lists it. Every current Clipp runtime lists Yamux before Mplex, while Go v0.49.0 provides `/yamux/1.0.0`; therefore there is already a common muxer on TCP and WebSocket connections. WebRTC Direct uses its transport-native data-channel muxer.

Go's optional `github.com/libp2p/go-libp2p-mplex v0.11.0` adapter still exposes `/mplex/6.7.0`, but its own README marks Mplex deprecated and recommends Yamux. See the [v0.11.0 adapter](https://github.com/libp2p/go-libp2p-mplex/tree/v0.11.0) and [deprecation notice](https://github.com/libp2p/go-libp2p-mplex/blob/v0.11.0/README.md#deprecation-notice). Adding it would widen the exposed stack without enabling a current Clipp runtime that Yamux cannot already serve.

Make this an interop assertion: TCP and WSS tests must verify that the negotiated multiplexer is Yamux. Add Mplex only as a temporary, explicitly reviewed compatibility fallback if an actually supported deployed Clipp build lacks Yamux.

## Clipp Rendezvous compatibility

`/clipp/rendezvous/1.0.0` is not the standard libp2p Rendezvous protobuf protocol. Current clients send one JSON object per stream operation:

- `register`: `{ action, topic: "clipp", signedPeerRecord: number[] }`;
- `lookup`: `{ action, topic: "clipp", peerId }`;
- `unregister`: `{ action, topic: "clipp" }`.

The expected response is `{ ok: true }` plus `peer` for registration or `record: { peer, signedPeerRecord }` for a successful lookup. See the current [client implementation](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/rendezvous.ts#L93-L164) and [protocol identifier](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/rendezvousProtocol.ts#L1).

The Go handler should use a bounded JSON decoder, enforce one request and one response per stream, verify the signed envelope with `record.ConsumeEnvelope(..., peer.PeerRecordEnvelopeDomain)`, require the record's Peer ID to equal `stream.Conn().RemotePeer()` for registration, and then persist only the finite lease in the agreed shared store. The official Go PeerRecord documentation describes domain-separated signature validation and typed record recovery. See [`core/peer` PeerRecord](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/core/peer#PeerRecord).

The current protocol relies on message/chunk boundaries and has no explicit length prefix. A single Go `Write` response is the closest compatible behavior, but stream fragmentation/coalescing must be tested over Yamux, Mplex only if enabled, and WebRTC Direct. The later control-protocol decision should either codify a maximum size and framing for v1 or introduce a versioned v2 protocol; do not silently change framing under `/1.0.0`.

## Required compatibility prototype and acceptance matrix

The already-charted connection-scoped authentication prototype should exercise a real Go v0.49.0 host against the actual Clipp dependency set. At minimum:

| Case | Required observation |
|---|---|
| Electron -> raw TCP | Noise authenticates Peer ID, Yamux negotiates, auth succeeds, reserve/refresh succeeds, circuit carries bytes. |
| Electron -> WSS | TLS terminates at the configured edge, WebSocket Upgrade reaches Go, Noise/Yamux/auth/HOP all succeed. |
| Electron -> WebRTC Direct | Discovered certhash address dials and authentication/HOP work on the resulting connection. |
| Android/Capacitor -> WSS | Browser policy accepts the public certificate and reserve/auth/Rendezvous sequence succeeds. |
| Android/Capacitor -> WebRTC Direct | Current browser WebRTC implementation interoperates with the Go listener. |
| Chrome offscreen -> WSS and WebRTC Direct | Authentication and outbound HOP CONNECT work; reservation behavior is recorded because current extension defaults differ from the other runtimes. |
| Unauthenticated HOP RESERVE/CONNECT | Standard `PERMISSION_DENIED`; no reservation/circuit state changes. |
| Unauthenticated Clipp Rendezvous | Indistinguishable denial; no record existence leakage. |
| Same Peer ID, new account | Prior connection is closed and its reservations/circuits are cleaned up before the new account becomes authoritative. |
| Two accounts on one circuit | Each forwarded payload byte is charged once to both; same-account endpoints charge that account once. |
| Quota exhaustion | At most the configured allocation-block overshoot, then both circuit streams reset and both circuit slots release once. |
| Forced session termination | Exact `Conn.ID()` closes across TCP, WSS, and WebRTC Direct; unrelated same-account sessions remain. |
| Relay process restart | New Peer ID and WebRTC certhash register with the Coordinator; stale addresses are no longer discovered. |

For Circuit Relay itself, also run the upstream relay tests unchanged against the adapted package. Wire compatibility is not established by a single happy-path connection; reservations, refresh, status errors, vouchers, per-direction limits, half-close, reset, and disconnect cleanup all need to remain covered.

## Decision impact

- The Go implementation is feasible without inventing transports or replacing Circuit Relay v2.
- v0.49.0 plus Yamux is compatible with the current Clipp clients; Mplex is optional and should be omitted initially.
- The separate Relay Authentication protocol is feasible and can bind an account to an exact live `network.Conn`.
- Stock go-libp2p admission/metrics hooks do not satisfy the Relay Account quota model. The next prototype must validate a narrow Clipp-owned relay adaptation, including cross-transport forced termination and exact-account metering.
- Clipp Rendezvous needs a custom Go handler; a standard Rendezvous package is not wire-compatible with the current application protocol.
- The later protocol ticket must explicitly settle JSON framing/versioning and error semantics discovered by the interop prototype.

## Primary sources

- [Clipp networking dependencies](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/package.json#L3-L23)
- [Clipp shared libp2p node](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/node.ts#L56-L253)
- [Clipp Circuit Relay reservation lifecycle](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/engine.ts#L1285-L1355)
- [Clipp Rendezvous client](https://github.com/invine/clipp/blob/44ddde117c9badf2f2128d1e7acd85a80bb2a819/packages/core/network/rendezvous.ts#L93-L164)
- [libp2p Circuit Relay v2 specification](https://github.com/libp2p/specs/blob/master/relay/circuit-v2.md)
- [libp2p WebRTC Direct specification](https://github.com/libp2p/specs/blob/master/webrtc/webrtc-direct.md)
- [go-libp2p v0.49.0 release](https://github.com/libp2p/go-libp2p/releases/tag/v0.49.0)
- [go-libp2p v0.49.0 relay source](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/protocol/circuitv2/relay/relay.go)
- [go-libp2p v0.49.0 relay API](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/p2p/protocol/circuitv2/relay)
- [go-libp2p v0.49.0 network API](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/core/network)
- [go-libp2p v0.49.0 WebSocket transport](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/p2p/transport/websocket)
- [go-libp2p v0.49.0 WebRTC transport](https://pkg.go.dev/github.com/libp2p/go-libp2p@v0.49.0/p2p/transport/webrtc)
- [go-libp2p-mplex v0.11.0](https://github.com/libp2p/go-libp2p-mplex/tree/v0.11.0)
