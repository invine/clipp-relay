# Prototype transport capacity and memory accounting

Type: prototype
Status: resolved
Blocked by: 01, 04, 11, 12

## Question

What do staged real TCP, WSS, WebRTC Direct and mixed-transport connections,
active circuits, control work and churn reveal about libp2p accounted memory,
relay resident memory and CPU, and what evidence-backed budget or target choices
must be returned to the user before finalizing the operational resource profile?

## Comments

- Created and claimed on 2026-09-08 following accepted Q336 in
  [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md).
  That ticket's partial accepted configuration supplies the experiment's
  baseline, not a circular completion dependency.
- Preserve the 5,000-session goal and no-fork policy. The 512-MiB Resource
  Manager/2-GiB container settings remain provisional; this experiment cannot
  approve more infrastructure or lower the target on the user's behalf.
- Use isolated loopback-only local processes, small staged loads and explicit
  stop/time limits. No production cluster, public load, Google credentials or
  real account data. Separate relay measurements from load-generator memory.
- Distinguish accounted reservations from actual RSS/Go/native memory. Record
  platform, pinned dependencies, workload, connection/circuit counts, rejection
  causes and cleanup state. No local Darwin result proves OCI Linux capacity.
- Read [the source profile](../research/operational-resource-scope-profile.md)
  for the WebRTC and yamux reservations. Its subordinate scope matrix is an
  experimental candidate, not accepted deployment configuration.
- The prototype skill's HTML state simulator cannot supply real transport
  memory evidence. Accepted Q336 takes precedence: use a clearly marked,
  trivial-to-run throwaway Go measurement harness and readable captured results.
  Do not replace real measurements with an HTML arithmetic simulation.
- Human review concluded with revised Q337 below. No production implementation
  or capture of unrelated dirty files is authorized by this prototype.

## Prototype evidence — first local pass

- Runnable asset: [transport-capacity prototype](../prototype/transport-capacity/README.md).
  Separate relay/generator processes; Go 1.27.1 Darwin ARM64, libp2p v0.49.0.
- Completed a two-client smoke check and staged 128-client TCP/WSS/WebRTC/mixed
  runs. WebRTC reached 121 then hit the system memory reservation limit; other
  workloads reached 128. All opened sixteen stock circuits and echoed data.
  Every cleanup sample returned connection/stream/accounted-memory counts to zero.
- Detailed samples, memory definitions, limitations and reproduction commands
  are in the asset. These are transport-only Go connections, not account-
  authenticated production Relay Sessions or a 5,000-session acceptance result.
- No budget/target change was made. The first pass did not supply a concrete
  sizing decision for the human: sustained/account-aware and OCI-enforced
  sizing remained unproven. The user is not responsible for reviewing code or
  certifying measurements. Do not resolve or claim production capacity based
  solely on the first local pass.
- On the user's 2026-09-08 Wayfinder continuation, extend the throwaway harness
  with bounded concurrent traffic and slow readers before presenting a specific
  trade-off. This is continued technical work, not a pause awaiting generic
  human approval. Keep the 512-MiB diagnostic memory baseline unchanged.

## Continued evidence — bounded traffic and slow readers

- Extended and ran the same local Go harness with paced traffic, renewed stock
  circuits, eight-second read pauses and concurrent controls. Up to 64 circuits
  touched 32 busy client connections; the remaining connections stayed idle.
  Each workload ran twelve seconds with a 128-MiB source-payload ceiling.
- TCP/WSS/mixed reached 128 connections; WebRTC again reached 121 and hit the
  system reservation bound. Paced workloads verified roughly 116–119 MiB of
  echoes per transport without counted open/I/O/control failures. This is not
  a full-scale or longevity test.
- Maximum sampled slow-reader RSS was approximately 56 MiB TCP, 56 MiB WSS,
  262 MiB WebRTC and 121 MiB mixed. A separate WebRTC repetition reached about
  272 MiB. Low idle RSS is not a safe sizing proxy under buffering pressure.
- Slow readers caused incomplete transfers/deadline failures and busy-path
  one-second control failures in WebRTC/mixed runs. Sampled idle-path controls
  succeeded. This does not prove production ten/twelve-second control deadlines
  fail, diagnose an upstream bug, or guarantee global fairness.
- All final cleanup samples returned connection/stream/accounted-memory counts
  to zero. RSS need not immediately return to baseline. Details, counters,
  actual CPU-time deltas and raw results are in the
  [prototype evidence](../prototype/transport-capacity/README.md#pressure-extension--2026-09-08).
- Local Linux/cgroup follow-up is not presently available through the configured
  Docker contexts: their local engine socket is absent. A factual read-only
  check did not start a VM or contact remote infrastructure. No production
  RAM/CPU figure or safe 5,000-session capacity is established by these runs.

## Decision round — Q337 (original proposal; not adopted)

**Q337 — Reopen the provisional resource envelope rather than weaken the
5,000-session goal?** Recommend keeping 5,000 concurrent sessions without a
hidden restriction on transport mix, and permitting a larger capacity profile
to replace the provisional 512-MiB Resource Manager / two-GiB relay container
baseline. The source-derived lower bound for 5,000 all-WebRTC connections is
about 20.5 GiB of Resource Manager ceiling before other reservations; that is
not a measured physical RAM requirement or a sufficient final configuration.
The final RAM/CPU numbers require a suitable Linux ARM64 workload and enforced
resource-budget validation. This question authorizes revisiting the planning
envelope, not a particular larger size, purchase, VM startup, cluster access
or deployment. Any environment setup requiring further authority is explicit.

The concrete human choice is whether resource growth is allowed to preserve
the transport-independent target, not whether the user approves the Go code,
believes the samples, or certifies a benchmark. If the existing two-GiB budget
is instead a hard constraint, return to the user with an explicit capacity/
transport-mix revision; never silently downgrade it. This is a new budget
direction decision, distinct from Q336's permission to collect local evidence.
Keep this ticket claimed until the required decision and follow-on sizing work
are genuinely handled; do not close it on a generic “prototype looks good”.

## Answer — revised Q337 (accepted 2026-09-08)

The user explicitly chose to keep both the current limits and the 5,000-session
goal unchanged, start with lower demand, and defer capacity tuning until code
is ready. This supersedes the proposal above to reopen the resource envelope
now and Q336's requirement to finish sizing before completing planning.

Retain the 512-MiB Resource Manager ceiling, 1,536-MiB GOMEMLIMIT and two-GiB
relay container limit. No higher budget, lower goal, transport restriction or
new subordinate scope default is approved. Later tuning must coordinate
application Resource Manager settings with Go and container resource budgets;
changing only the Kubernetes memory limit does not change admission accounting.

The prototype is resolved because the evidence and planning trade-off are now
settled, not because 5,000-session capacity was demonstrated. The observed
121-connection WebRTC accounting ceiling and slow-reader effects remain known
constraints. Initial deployment need not run at the eventual target; production
capacity claims still require representative, enforced Linux ARM64 validation
after implementation, owned by
[Define acceptance, release, and operating criteria](13-define-acceptance-release-and-operating-criteria.md).
Security, correctness and recovery checks are not waived.

[Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md)
can continue with the unchanged provisional baseline; its other unresolved
defaults still need decisions. The throwaway harness is captured separately on
`codex/prototype-transport-capacity` at `e95a6c5`, including source, raw results
and the verdict. Main, the normal staging index and unrelated working changes
were preserved. Do not promote the harness to production code.
