# Throwaway transport-capacity prototype

**Question:** How do real libp2p transport reservations differ from resident
relay memory, and does the provisional 512-MiB Resource Manager budget admit
the desired connections? This is decision evidence, not production relay code.

From this directory, run **`go run .`**. It builds a controller that starts a
fresh relay process and separate Go load-generator process for each transport.
Only generated loopback addresses are accepted. No OCI, Google, PostgreSQL,
account data or public load is used. WSS uses an ephemeral certificate trusted
explicitly by the generator; TLS verification is not disabled.

Optional: `go run . -max 128 -result my-new-results.json`. Results refuse to
overwrite a file. `-transport tcp|wss|webrtc|mixed|all` selects the workload;
`-max 2` is the smoke check. Hard maximum is 160 hosts, whole-run limit five
minutes. The controller stops if a sampled relay RSS exceeds 1.5 GiB. This
sampling guard is not a hard cgroup memory limit. JSON rows are printed after
each action. Upstream diagnostic errors may also appear in the terminal.

For the pressure extension, use `go run . -max 128 -pressure -result pressure-new.json`.
It adds two twelve-second workloads and samples both processes about once per
second during traffic. The 512-MiB system accounting ceiling is unchanged.

If the execution environment restricts local sockets, explicit permission to
bind loopback TCP/UDP and inspect local process memory is required. For an
isolated build cache, set `GOCACHE=/private/tmp/clipp-capacity-go-cache`.

## What actually happens

1. Start a relay with raw TCP, TLS WebSocket and WebRTC Direct listeners.
2. Grow distinct secured Go client connections through 2, 8, 32 and the selected
   maximum. A failed dial stops that growth stage, not an endless retry loop.
3. Exchange a five-byte echo over a prototype protocol, four concurrent
   operations at most, on already-established connections.
4. Make up to sixteen stock Circuit Relay v2 reservations/connections between
   disjoint pairs. Each circuit echoes 64 KiB and remains open for the sample.
5. Close up to eight client identities, replace them with fresh identities,
   then close every client. Sample after each phase, after a 300-ms settle delay.

The target in a row is a requested count, not a success claim. `Clients.OK`
counts successful operations **in that phase**; `Relay.Connections` is the
observed live count. Failed libp2p dials may internally retry: Resource Manager
blocked-event counts are not the number of distinct clients refused.

## Measurements and deliberately missing pieces

- Relay `Accounted`: actual system Resource Manager reservations, in bytes.
- `Heap`: Go HeapAlloc. `GoManaged`: Sys minus HeapReleased, overlapping RSS,
  not a separately additive budget. No forced GC.
- `RSSKiB`: current OS `ps` RSS, including runtime/base cost. `PeakRSSBytes`:
  process-lifetime getrusage maximum, not the peak of an individual phase.
- `CPUSeconds`: cumulative process CPU time; subtract neighboring samples for
  phase CPU. Elapsed time includes controller overhead and the settle delay.
- Separate generator samples prevent confusing client memory with relay RSS.
- Block events retain aggregate types/scope categories, not peer identities.

Pinned go-libp2p v0.49.0 and yamux v5.1.0; dependencies/checksums are captured
in go.mod/go.sum. Recorded runs used Go 1.27.1 on Darwin ARM64, without OCI's
Linux container memory/CPU enforcement. Library defaults are not autoscaled
for the relay: the profile fixes system memory 512 MiB, physical connections
8,192, total streams 32,768, transient memory 512 MiB/256 connections, peer
memory 32 MiB, connection memory 8 MiB and stream memory 1 MiB. IP limits and
allowlist bypass are disabled. The process sets GOMEMLIMIT to 1,536 MiB.

This is an explicitly **diagnostic** profile: service/protocol scopes share
the system ceiling instead of silently pretending the unapproved detailed
production matrix is selected. Clients use a null Resource Manager so the
experiment isolates the relay's limiter; that is not a server bypass.

These are secured transport connections, **not account-authorized Relay
Sessions**. There is no account authentication, credential refresh, Rendezvous,
quota reporting/allocation, database or portal load. Circuit endpoints handle
stock HOP/STOP and echo bytes; they do not run Clipp's nested end-to-end protocol
or its application workloads. Control echo is not a claim to measure Relay
Authentication or Rendezvous validation costs. Go clients are not proof of
Electron, Android or browser-runtime capacity. Churn uses fresh identities,
not same-Peer-ID replacement. This is not a throughput, saturation or longevity
benchmark and must not be extrapolated linearly to 5,000 production sessions.

## Captured first pass — 2026-09-08

Evidence: [smoke](smoke-20260908.json), [staged 128](staged-128-20260908.json).
Values below are from the final initial growth sample, before circuit work.

| Transport | Requested / connected | Accounted MiB | Relay RSS MiB |
| --- | --- | --- | --- |
| TCP | 128 / 128 | 0 | 32.05 |
| WSS with verified TLS | 128 / 128 | 0 | 36.23 |
| WebRTC Direct | 128 / 121 | 303.68 | 72.50 |
| Mixed: 43 TCP, 43 WSS, 42 WebRTC | 128 / 128 | 105.41 | 49.88 |

Zero idle TCP/WSS accounted memory does **not** mean zero real memory. WebRTC's
121 successful reservations total exactly 121 × 2,631,680 bytes. Its next dial
times out while the relay reports `block_reserve_memory:system`; this matches
the source-derived priority ceiling, not OS memory exhaustion.

All four workloads opened sixteen simultaneous circuits and completed sixteen
64-KiB echoes. Holding those circuits raised accounted memory by 8.0625 MiB
for TCP/WSS and 0.0625 MiB for WebRTC. This is consistent with yamux's per-stream
window reservation versus WebRTC's already-reserved per-connection buffer.
During those samples relay RSS was 35.59, 39.88, 92.13 and 58.11 MiB respectively.
The WebRTC churn/replacement phase again stopped at 121; other workloads
returned to 128. Every final cleanup sample had zero relay connections, zero
streams and zero accounted memory. RSS need not return immediately to baseline.
One smoke shutdown printed an upstream already-closed-listener diagnostic;
the process exited and cleanup counters were zero. This is recorded, not hidden
as a transport success metric or treated as a production fix.

## Pressure extension — 2026-09-08

Evidence: [two-client WebRTC smoke](pressure-smoke-webrtc-20260908.json),
[four-transport run](pressure-128-20260908.json),
[WebRTC repeat](pressure-webrtc-repeat-20260908.json).

The extension uses up to sixteen disjoint client pairs (the first 32 clients),
with four concurrent circuits per pair: at most 64 live circuits. Remaining
clients stay connected but do not carry load. A one-byte **prototype payload**
prelude selects echo or slow-reader behavior after the stock STOP handshake;
there is no change to Circuit Relay framing or implementation. Earlier captured
first-pass files predate this prelude and the asynchronous workload counters.

- **Paced sustained traffic:** each lane writes and verifies a 16-KiB echo,
  waits 100 ms, and renews its circuit after 1 MiB. The phase lasts twelve
  seconds, with an aggregate 128-MiB source-payload budget. Echo bytes traverse
  the relay again; this budget is not total wire traffic. This deliberately
  modest duration is a pressure experiment, not a longevity/soak test.
- **Slow readers:** each destination pauses reads for eight seconds, then
  drains. Each source attempts 127 chunks of 16 KiB, remaining below the stock
  2-MiB directional limit including the prototype prelude. Writes have the
  twelve-second phase deadline. Writes over 250 ms and deadline failures are
  counted, not hidden as successes. There is no unlimited payload allocation.
- **Concurrent control:** one five-byte echo exchange every 200 ms, with a
  one-second deadline, alternating busy and idle connections when both exist.
  These are prototype echoes, not real Relay Authentication/Rendezvous; their
  one-second timeout does not prove a production ten/twelve-second deadline
  fails. Busy/idle results are recorded separately.

`AcceptedBytes` means accepted by the sender's local Write, **not delivered**.
`VerifiedEchoBytes` means complete content-checked echoes. `DestinationReadBytes`
counts slow-reader application reads and can still increase after the source
workers finish. A Done workload is not proof all transport buffers drained.
Final host cleanup is checked separately. Counters for prior work remain visible
in subsequent churn/cleanup samples and are not new work in those phases.

The table uses maximum **sampled relay RSS** within each phase, not a guaranteed
instantaneous peak or incremental memory per connection. The runtime may retain
heap pages; no forced GC was used. CPU figures below are relay-process CPU-time
deltas, not load-generator CPU or a cgroup CPU limit.

| Transport | Initial idle RSS MiB | Paced phase max RSS MiB | Slow-reader max RSS MiB | Slow-reader busy control failures | Idle control failures |
| --- | --- | --- | --- | --- | --- |
| TCP, 128 connections | 32.30 | 39.59 | 55.83 | 0 / 30 attempts | 0 / 30 |
| WSS, 128 connections | 36.42 | 44.03 | 55.52 | 0 / 30 | 0 / 29 |
| WebRTC, 121 connections | 72.00 | 108.75 | 262.00 | 7 / 12 | 0 / 11 |
| Mixed, 128 connections | 48.97 | 71.34 | 120.67 | 6 / 18 | 0 / 17 |

All paced phases opened 128 circuits over time (at most 64 concurrently),
verified roughly 116–118 MiB of echoes, and had no recorded open/I/O/control
failures. Relay CPU-time deltas were 4.91 s TCP, 5.56 s WSS, 21.60 s WebRTC and
11.20 s mixed over approximately twelve seconds each. A process can consume
more than one CPU-second per wall second across cores: this is not evidence
that the proposed two-core OCI limit supports the same traffic or latency.

TCP/WSS slow-reader runs delivered all 127 MiB of attempted source payload.
WebRTC recorded 56 deadline-limited writes and one other I/O failure; mixed
recorded 40 deadline-limited writes. Not all accepted bytes were delivered
before those phase samples. These are deliberately induced stalls, not a
claim that the pressure workload passed without errors or that their exact
upstream cause has been diagnosed. Busy WebRTC paths had control failures;
idle paths' sampled controls succeeded, not a universal fairness guarantee.

The separate WebRTC repeat again admitted 121 connections, recorded about
115.45 MiB maximum sampled RSS during paced work and 271.72 MiB during slow
readers, with 8/12 busy-control failures, 0/11 idle-control failures and 48
deadline-limited writes. It had no other counted I/O failures. Thus the
pressure/control effect recurred, but these two runs do not establish its
statistical distribution. Every final cleanup in the four-transport run and
repeat had zero relay connections, streams and accounted memory. Some relayed
streams outlived source deadlines until connection cleanup; do not claim
instant stream-only recovery from these results.

The configured local Docker context was checked read-only: it targets a local
Unix socket that is absent. No running engine was available; Linux VM memory,
CPU and cgroup capacity were unavailable, not zero. No VM, container, image
pull or OCI operation was started. Further Linux-enforced measurement needs
an explicitly available test environment; local Darwin results cannot replace it.

## Decision — revised Q337, accepted 2026-09-08

The current memory-accounting profile is demonstrably incompatible with 5,000
all-WebRTC connections. The measured idle RSS being smaller does not justify
under-accounting or simply raising the limiter above the container: buffered
traffic can exercise that reserved capacity. The user chose to keep current
limits and the 5,000-session goal unchanged, begin with lower demand and defer
capacity tuning until code is ready. This resolves the prototype's planning
decision without claiming target capacity or approving a larger budget.
Further sizing needs sustained/concurrent traffic, real account/control work,
relevant transport mixes and Linux ARM64 resource enforcement. Coordinate
Resource Manager, Go and container budgets; a container-only change does not
change libp2p admission. Any future budget or target change remains explicit.

Owner: [Prototype transport capacity and memory accounting](../../issues/17-prototype-transport-capacity-and-memory-accounting.md).
Throwaway capture branch: `codex/prototype-transport-capacity`. Do not commit
unrelated planning changes or absorb this harness into the production service.
