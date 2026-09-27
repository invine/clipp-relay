# Prototype Relay Authentication and conservative quota enforcement

Type: prototype
Status: resolved
Blocked by: 01, 03

## Question

Can a minimal single-process Go relay and JavaScript client harness demonstrate that `/clipp/relay-auth/1.0.0` associates an approved Relay Account with the exact Noise-authenticated connection before stock Circuit Relay v2 reserve/connect and exact Rendezvous use, while a small HOP gate and stock bandwidth reporter enforce conservative Charged Relay Traffic and close displaced, suspended, or exhausted Relay Sessions across every required transport without changing Clipp's end-to-end trust semantics?

## Comments

- Reduced-scope throwaway prototype: branch `prototype/connection-scoped-relay-authentication`, latest commit `d95c6d0`, under `prototype/relay-authentication/`. `npm run review` serves a live controller whose `/api/run` endpoint starts a fresh real probe, mirrors ordinary child logs to the terminal, and streams progress, logs, and evidence into the page; no transport results are embedded in the HTML.
- Executable result: the unmodified Circuit Relay v2 handler passed stock reservation/connect and conservative HOP/STOP endpoint accounting over TCP, WebSocket, and WebRTC Direct. Displacement, suspension, expiry/renewal, session quotas, and account-wide quota exhaustion also passed.
- Human verdict: accepted. The exposed tradeoff is that BandwidthReporter enforcement is asynchronous: quota exhaustion closes all Relay Sessions for the account, but an already-buffered frame may complete.

## Answer

Yes. The accepted prototype demonstrates that a small host adapter can require Relay Authentication on the exact live connection before handing HOP traffic to the unmodified Circuit Relay v2 handler, while the same gate protects exact Rendezvous use. It passed over raw TCP, WebSocket, and WebRTC Direct with Noise or the transport's embedded Noise identity.

Conservative BandwidthReporter accounting can charge HOP and STOP endpoint bytes to the active Relay Account and terminate every Relay Session for an exhausted account. The prototype also validated same-connection renewal, per-Peer-ID cross-account displacement, administrator suspension, session quotas, and expiry. V1 accepts asynchronous enforcement: a frame already buffered when the quota is crossed may finish before account-wide connection closure takes effect.

The prototype is an implementation guide, not production code. Its accepted asset is branch `prototype/connection-scoped-relay-authentication` at commit `d95c6d0`, under `prototype/relay-authentication/`.
