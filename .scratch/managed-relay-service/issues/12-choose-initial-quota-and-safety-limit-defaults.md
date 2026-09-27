# Choose initial Quota Plan and Safety Limit defaults

Type: prototype
Status: resolved
Blocked by: 04, 06, 07

## Question

Given measured prototype behavior and the 1,000-account/5,000-session single-instance acceptance target, what numeric defaults and administrator-configurable values should govern weekly Charged Relay Traffic, concurrent Relay Sessions, local traffic-credit blocks, request rates, authentication timeouts, and stock reservation, circuit, Resource Manager, payload, and overload Safety Limits?

## Comments

- Claimed through Wayfinder. The first review artifact uses the logic-prototype
  shape: a standalone HTML quota and circuit-limit simulator, not a production
  relay implementation or a capacity benchmark.
- Draft primary asset: branch `codex/prototype-relay-limits`, commit `db69a81`,
  file `prototype/relay-limits.html`. The current worktree file is
  [Quota lab](/private/tmp/clipp-relay-limits.vQ1jAE/worktree/prototype/relay-limits.html).
  Open that file directly in a browser; no server, packages, or persistence are
  required. It has editable hypotheses, a visible account ledger, free-play
  actions, and guided endpoint-accounting, crash-credit, dependency-outage,
  large-frame, history-replay, shared-Ingress-IP, and circuit-duration scenarios.
- [Initial-limit evidence](../research/initial-limit-evidence.md) separates
  source facts and accepted probe observations from fixture values, modelling
  assumptions, proposed experimental ranges, and missing performance evidence.
  The existing real probe was inspected, not rerun for this draft.
- Important findings to review before approving values: stock 128 KiB
  per-direction circuits cannot fit Clipp's maximum 256 KiB body; a full-history
  snapshot spans multiple frames without a resume cursor, so finite circuit
  limits risk replaying the same prefix; and WSS through a terminating Ingress
  may share one observed proxy-IP reservation bucket. The history/proxy risks
  are source-based, not reproduced OCI measurements.
- The candidate settings are not decisions. The demo deliberately labels its
  credit-prefunding policy, serialized callback accounting, assumed history
  wire size, and simplified clock as modelling assumptions. It does not prove
  a production overshoot bound, large-history completion, PostgreSQL throughput,
  Resource Manager sizing, or the 5,000-session ARM64 acceptance target. Those
  facts cannot be inferred from arithmetic or the earlier small probe.
- Validation: both inline scripts passed a JavaScript syntax check; the
  committed prototype and planning edits passed whitespace checks. The in-app
  browser's URL policy rejected the local file, so rendered layout and browser
  interaction were not verified by the agent. Human review is still required.
- Pending: review the simulator and choose how to investigate large-history
  recovery before fixing circuit caps. Weekly allowances, credit blocks,
  reservation sublimits, timeouts, request rates, and Resource Manager/overload
  defaults remain unapproved. No resolution answer has been recorded.
- User verdict: accept the proposed values as provisional initial defaults and
  revisit them when production usage supplies more information. This supersedes
  the pending approval above; it does not turn the simulator into performance
  evidence or approve a production deployment.
- Scope split: the simulator proposed eight service/account settings. Other
  operational controls in the original question had no proposed numeric
  values; their selection continues in
  [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md).

## Answer

Adopt the simulator's starter settings as provisional v1 defaults. The user
accepts starting with these values rather than delaying their selection for
additional measurement. Revise them through administrator-controlled
configuration when production evidence justifies a change; clients do not set
their own quotas, and this decision introduces no automatic tuning mechanism.

| Setting | Initial value |
| --- | --- |
| Weekly traffic allowance (Quota Committed) per Relay Account | 1 GiB (1,073,741,824 bytes) |
| Concurrent Relay Sessions per Relay Account | 5 |
| Account-local traffic-credit block size | 64 KiB (65,536 bytes) |
| Circuit data Safety Limit | 2 MiB (2,097,152 bytes) in each direction |
| Circuit duration Safety Limit | 120 seconds |
| Global simultaneous reservation ceiling | 6,000 |
| Reservations per observed IP address | 6,000 |
| Reservations per recognized IPv6 ASN | 6,000 |

The first two values form the initial administrator-managed Quota Plan baseline;
existing plan and account-override semantics remain unchanged. The other
values are service configuration defaults, not client-negotiated account
allowances. The circuit budget is cumulative across the relayed connection,
not renewed per application message or stream. IP and ASN ceilings match the
global reservation ceiling, so they add no tighter reservation sublimit; this
avoids using the stock eight-per-IP cap against a shared Ingress proxy address.
It does not claim original-client-IP attribution or add IP throttling.

The weekly allowance's consumption measure is subsequently refined to Quota
Committed by accepted Q221 in
[Define internal Go modules and PostgreSQL persistence](14-define-go-modules-and-postgresql-persistence.md).
This changes the accounting definition, not the adopted 1 GiB allowance.

The 1,000-account/5,000-session figures remain acceptance targets, not measured
capacity or new hard admission limits. The simulator's 5,000-clients-per-proxy
and 100-MiB-history values are scenario assumptions, not service policy. Its
credit-prefunding, callback serialization, and simplified clock remain model
assumptions; accepting the block size does not settle PostgreSQL allocation
transactions or establish a maximum overshoot bound.

Keep the known risks explicit. A 2-MiB circuit has more byte headroom than a
single maximum-sized Clipp body, but neither that budget nor the two-minute
deadline guarantees completion of a multi-frame history snapshot. Repeated
history prefixes, shared-proxy address handling, stranded credit, asynchronous
quota cutoff, and the ARM64 capacity target remain subjects for release
validation and production observation. These values do not implement resumable
history or certify those risks as fixed. Existing security and release
requirements are not waived by provisional acceptance.

The primary prototype is branch `codex/prototype-relay-limits`, commit
`db69a81`, file `prototype/relay-limits.html`; its draft labels reflect the
pre-approval review state. This answer is authoritative for the subsequent
provisional acceptance. Source evidence and suggested measurement areas are in
[Initial-limit evidence](../research/initial-limit-evidence.md).

This closes selection of the proposed baseline, not every control named in the
original broad question. Unproposed timeouts, request/payload bounds,
reservation lifetime, per-peer circuit limits, and Resource Manager/overload
settings remain explicitly open in
[Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md).
Their configuration must be specified before the overall map is
implementation-ready, but the adopted baseline does not await more production
data. No production code or deployment was changed here.
