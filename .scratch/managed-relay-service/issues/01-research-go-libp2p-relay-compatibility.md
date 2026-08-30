# Research Go libp2p relay compatibility

Type: research
Status: resolved
Blocked by:

## Question

Against official libp2p specifications, Go package documentation, and source code, what exact Go stack and extension hooks can provide Circuit Relay v2 HOP/reservations, Noise, Yamux and current-client Mplex compatibility, WSS, raw TCP, WebRTC Direct UDP, Identify, Ping, connection-scoped authentication before reserve/connect/Rendezvous, traffic metering, and forced circuit/session termination while remaining wire-compatible with the current JavaScript Clipp runtimes?

## Comments

## Answer

[Go libp2p relay compatibility](../research/go-libp2p-relay-compatibility.md) finds that go-libp2p v0.49.0 provides the required interoperable transport and protocol stack, with Yamux sufficient for every current Clipp runtime. Stock Circuit Relay v2 preserves the correct wire protocol but lacks connection-aware admission, account-attributed traffic hooks, and circuit handles, so the authentication prototype must validate a narrow pinned Clipp-owned adaptation before the relay design is finalized.
