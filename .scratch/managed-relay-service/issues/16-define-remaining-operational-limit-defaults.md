# Define remaining operational Safety Limit defaults

Type: grilling
Status: resolved
Blocked by: 11, 12, 14, 15, 17

## Question

Given the internal module boundaries and deployment resource budget, what
initial configurable request-rate, authentication/handshake, control-payload,
discovery/Rendezvous/retry, reservation-lifetime, per-peer circuit, drain, and
Resource Manager memory/stream/file-descriptor limits should complement the
accepted provisional quota and circuit baseline?

## Comments

- Resumed on 2026-09-08 at the user's next-frontier request after the capacity
  prototype resolved. The existing claim continues. Q338–Q347 were proposed
  then and accepted on 2026-09-26 as recorded below. Keep every previously selected limit and
  the scale goal unchanged; these questions fill remaining operational fields.

- Claimed on the user's Wayfinder request after all deployment, quota,
  persistence and recovery prerequisites resolved. Work remains planning-only;
  do not edit production runtime/configuration or treat proposed numbers as
  accepted. Continue question numbering at Q308. Use grilling and domain-modeling
  with factual research where needed; preserve the accepted quota/security
  ceilings and the deferred IP-throttling boundary.

- The accepted [recovery contract](../recovery-contract.md) fixes the workload
  shape: PostgreSQL/local archiver, reload watcher and distinct-UID same-Pod
  backup scheduler within the existing database Pod budget; separate scheduled
  repository/journal cleanup. Recovery is now an explicit prerequisite for this
  ticket's final numeric configuration. Choose the per-container split,
  archive/backup concurrency and scratch caps, pending-deletion and append retry
  bounds, cleanup cadence/batches/deadlines, maintenance-state failure alerts,
  and storage/backup-lag warning/protective thresholds. Do not change the accepted
  daily-full/sixty-second WAL/seven-day PITR targets or thirty-day policy by
  choosing a cleanup schedule, and do not assume budgets prove capacity.

- Accepted recovery Q297/Q301/Q304 require bounded pgBackRest archive/backup
  work, observable/bounded pending-deletion work, and backup-lag/storage warning
  and protective-stop thresholds. Include backup work in the actual database
  workload budget rather than treating it as free background work. Placement
  and privileges are supplied by the recovery contract; choose numerical defaults
  here. A backup failure is not a quota-reset trigger.

- The resolved [deployment contract](../deployment-contract.md) requires an
  explicit database-container/reload-watcher resource split within the accepted
  database Pod budget, plus watcher polling/retry/expiry-warning settings.
  Derive these and the final probe/Job/drain settings here; do not make the
  watcher a hidden unbudgeted process or a bootstrap-readiness dependency.

- Accepted Q268 in
  [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md)
  establishes provisional request/limit envelopes: relay 1/2 CPUs and 1/2 GiB;
  bundled PostgreSQL 500m/1 CPU and 512 MiB/1 GiB; maintenance Jobs 100m/500m
  CPU and 128/256 MiB. Derive compatible connection, memory and work-concurrency
  limits rather than assuming upstream defaults fit. These envelopes are not
  benchmarks or evidence of 5,000-session capacity. Job limits do not bound
  database-side migration work. Drain, probe, Job-deadline and related numeric
  controls still require explicit values; Q266/Q271 do not supply them.

- This is the remaining scope from
  [Choose initial Quota Plan and Safety Limit defaults](12-choose-initial-quota-and-safety-limit-defaults.md).
  The user accepted the simulator's proposed numeric values provisionally and
  wants to tune them with production experience. The simulator did not propose
  values for these other operational controls, so their defaults must not be
  invented or treated as approved by that acceptance.
- Choose a practical initial configuration, not a claim of benchmark-derived
  safety. Production evidence can drive later administrator changes. Preserve
  the already settled credential security ceilings and wire contracts.
- Distinguish administrator-configurable controls from upstream compile-time
  constants and from limits supplied through the Kubernetes resource budget.
  The stock implementation and sizing caveats are recorded in
  [Initial-limit evidence](../research/initial-limit-evidence.md).
- The accepted weekly allowance, account session allowance, credit-block size,
  circuit byte/duration limits, and reservation ceilings remain authoritative
  in the baseline ticket. Do not re-open them merely to seek pre-production
  tuning data or treat scenario inputs as additional service limits.
- Include bounded login-continuation and temporary revocation-fence capacities
  from accepted Q239 in
  [Define internal Go modules and PostgreSQL persistence](14-define-go-modules-and-postgresql-persistence.md).
  Needed fences cannot be evicted while older flows remain valid; refuse new
  work instead. Also choose cleanup cadence/batch sizes and clock-skew controls
  handed off by that contract without changing accepted retention or expiry.

### First round — Q308–Q315 (accepted)

The user accepted Q308–Q315 and requested the next frontier. These are now
provisional selected defaults, not measured performance. Continue this ticket's
remaining resource, overload and operating settings; do not claim or resolve
the dependent acceptance ticket before those decisions are complete.

These are provisional engineering defaults to validate against the existing
ARM64 target, not measured capacity or changes to the accepted account quotas.
Server limits remain administrator-controlled/restart-configured under the
deployment contract; clients cannot negotiate higher limits. Detailed Resource
Manager scopes, pre-secure transport limits, per-operation rates, pools and
container budgets depend on this admission/renewal workload shape and follow
in the next round. Approval applies to this batch, not unproposed subordinate
scope limits or claims of simultaneous saturation capacity.

- **Q308 — Global admission:** Set the active Relay Session ceiling to 6,000,
  giving configuration headroom above the 5,000-session acceptance target.
  Keep five sessions per account and 6,000 reservations unchanged. Separately
  allow at most 256 secured-but-not-yet-Relay-Authenticated connections, which
  must complete initial Relay Authentication within ten seconds of becoming
  a secured libp2p connection. This is not a claim that kernel/proxy/transport
  handshakes before that point are covered; their work and Resource Manager
  scopes need separate coordinated bounds. Admission remains immediate,
  without an application wait queue. Preserve successful same-Peer-ID slot
  replacement and same-account reauthentication at capacity; failure leaves
  the old session intact. Configure lower-level headroom so it does not
  accidentally make those exchanges impossible at the logical session ceiling.
- **Q309 — Session lifetime and renewal:** Set maximum Relay Session lifetime
  to fifteen minutes, always capped by actual access-token expiry. Recommend
  renewal at a server-selected jittered 75–85 percent of the admitted lifetime,
  expressed through the existing renewAfterMillis field and bounded by actual
  remaining validity. The shared client serializes credential refresh and
  acquires a sufficiently fresh access token when needed to extend the session;
  reusing a token cannot extend beyond that token's expiry. No retry, clock
  adjustment or failed renewal extends the existing session deadline, and no
  credential security ceiling changes.
- **Q310 — Remaining stock relay resources:** Use thirty-minute reservations
  and sixteen simultaneous circuits touching each Peer ID. These are service
  Safety Limits, not additional account allowances, and a whole circuit counts
  at both endpoints. Preserve the accepted two-MiB-per-direction and 120-second
  circuit limits and 6,000 reservation caps. Reservation renewal does not renew
  Relay Authentication. Separate Resource Manager limits may reject earlier;
  these values do not promise simultaneous saturation at every ceiling.
- **Q311 — Independent operation deadlines:** Start with ten seconds for
  HTTPS discovery, twenty seconds per transport-address dial attempt, ten
  seconds for Relay Authentication, fifteen seconds for the client's reservation
  attempt, and twelve seconds for Rendezvous. Clipp-owned stream exchanges
  must bound combined read/validation/write work, not merely idle time; use the
  earlier initial-auth connection deadline where applicable. Client timeouts
  cancel/reset their attempt. Stock Circuit Relay v2's fixed 60-second control/
  STOP-handshake and 30-second STOP-connect constants remain upstream behavior;
  the fifteen-second client reservation timeout is not a claim to configure
  those server constants. No wire-protocol fork or timeout-triggered v1 fallback.
- **Q312 — Discovery and Rendezvous timing:** Publish discovery documents with
  a sixty-second TTL and permit at most five minutes of last-verified Kubernetes
  address-state staleness. Verify unchanged address state at least every sixty
  seconds using bounded successful API resynchronization, not only when an
  address-change event arrives. validUntil remains bounded by that staleness
  deadline; expiry does not close an existing authenticated connection. Use
  five-minute Rendezvous TTL and a managed-client refresh interval of sixty
  seconds with plus/minus twenty-percent jitter, scheduling earlier if required
  by the actual session/reservation/record expiry. Server leases still end at
  the earliest of all three bounds. Legacy clients' existing more frequent
  refreshes remain protocol-compatible, not a reason to reject v1 clients.
- **Q313 — Control payload ceilings:** Set Relay Authentication JSON frame
  bodies to 4 KiB, discovery JSON bodies to 16 KiB, raw signed peer-record
  envelopes to 16 KiB, Rendezvous v2 JSON frame bodies to 32 KiB, and complete
  unframed Rendezvous v1 JSON messages to 128 KiB. Apply request/response and
  encoded/decoded bounds before unbounded allocation; a length prefix is
  additional bounded framing, not permission to allocate its claimed length.
  The larger v1 budget accommodates its numeric byte array, while v2 uses
  unpadded base64url. Validate integer bytes before conversion, preserve accepted
  records unchanged, and reject oversized output/input rather than truncating
  signatures or silently publishing an incomplete address snapshot. These
  limits do not constrain encrypted forwarded Clip/History frames, whose
  separate existing application limits remain unchanged. No listing/batch lookup
  is introduced.
- **Q314 — Transient retries:** For known retryable temporary failures without
  a server hint, use per-configuration exponential backoff with ceilings of
  1, 2, 4, 8, 16, then 30 seconds; draw each delay uniformly from half to all
  of that ceiling. Reset after a complete successful ready cycle, not a partial
  dial success that could sustain a hot failure loop. A valid server retry hint
  is a minimum wait and can exceed thirty seconds; manual retry cannot bypass
  it. Default temporary overload hint is five seconds. This fast schedule does
  not apply to quota exhaustion, account-state/session-limit refusal or invalid
  credentials, whose slow/user-action handling remains intact and whose numeric
  polling defaults follow separately. Wake/resume checks authoritative expiry;
  no retry extends token/session/lease lifetime or creates another refresh race.
- **Q315 — Drain budget:** Allow thirty seconds for existing circuits to drain,
  then force-close what remains; set Kubernetes termination grace to forty-five
  seconds to leave a shutdown margin. Continue rejecting new work immediately
  on drain and withdrawing readiness/discovery. Upstream/OCI routing may cut
  connections earlier; this is a maximum best-effort opportunity, not guaranteed
  uninterrupted completion. Preserve complete old-Pod termination/fencing before
  the next ephemeral identity occupies the network slot.

### Factual boundaries for subsequent sizing

The [initial-limit evidence](../research/initial-limit-evidence.md) and existing
wire/lifecycle contracts distinguish stock constants from application settings.
Current Clipp uses twelve-second dial/engine-Rendezvous attempts and thirty-second
plus/minus twenty-percent Rendezvous refresh, but those implementation facts
were never approved as the managed-relay defaults. V1 Signed Peer Records are
numeric arrays (worst-case about four JSON bytes per raw byte), not v2 base64url.
Relevant sources: Clipp `packages/core/network/rendezvous.ts`, `engine.ts`, and
the resolved protocol/runtime tickets. No production source was edited.

The [Resource Manager investigation](../research/operational-admission-facts.md)
additionally found IP/subnet connection and
connection-rate controls independent of reservation IP/ASN limits. Existing
no-IP-throttling/shared-proxy policy requires explicitly disabling or making
these non-restrictive through supported configuration, not leaving an unnoticed
eight-connections-per-address bottleneck. This implements the settled boundary,
not a new per-IP policy. Do not claim global session, memory or reservation
configuration alone controls every transport or proves the acceptance target.

Next branches after this batch: explicit Resource Manager/connection-manager
and pre-auth transport budgets; HTTP/control rate/concurrency/body bounds;
client dial parallelism and slow retry; database pools/container splits; bounded
login/fence/cleanup/journal work; probes, clocks, storage and maintenance limits.
Ask independent ready values together, and retain each unknown as unapproved.

### Second round — Q316–Q323 (accepted)

The user accepted Q316–Q323 with “agree”. This batch uses accepted Q308–Q315
as its workload/admission baseline. Values below are provisional selected
defaults, not benchmark results. All applicable Resource Manager scopes
intersect; a ceiling is not a guarantee that every maximum can be saturated
simultaneously. The detailed subordinate scope matrix follows these top-level
choices before ticket resolution, rather than silently inheriting defaults.
The [budget consistency findings](../research/operational-budget-consistency.md)
check source support and arithmetic, not performance or policy acceptance.

- **Q316 — Admission rather than opportunistic eviction:** Disable ordinary
  connection-manager trimming for this relay and rely on explicit admission,
  expiry, quota/revocation, replacement and drain rules. Do not evict an
  otherwise valid idle authenticated session merely to admit a newcomer or
  invoke ForceTrim as routine overload handling. Make Resource Manager's
  independent IP/subnet concurrent/rate limits non-restrictive through supported
  configuration, preserving the existing no-IP-throttling policy. No additional
  Resource Manager allowlist capacity bypass. This does not promise survival
  of genuine process/kernel exhaustion or grant per-account fairness against
  an unauthenticated flood; overload protection still needs the bounds below.
- **Q317 — Physical connection headroom:** Set Resource Manager system
  connections to 8,192 combined/inbound and at most 512 outbound, with at most
  256 pre-peer transient connections and four simultaneous connections per
  Peer ID. The physical headroom above 6,000 active sessions supports the
  separately capped 256 post-security/pre-relay-auth connections, replacements
  and closing connections; no extra account sessions are authorized. Both
  authenticated and failed attempts count in the appropriate physical scopes.
  Outbound is a protective ceiling, not permission for new relay-initiated peer
  dials; inbound-only policy remains. Keep stock TCP upgrade behavior where
  no supported top-level override is available, set supported WebSocket
  negotiation timeout to ten seconds, and retain stock WebRTC Direct's fixed
  ten-second/128-in-flight setup behavior. Do not advertise test-only upstream
  knobs as configurable settings. Kernel/proxy/UDP work before these gates is
  separately covered by deployment/acceptance checks, not this connection count.
- **Q318 — Stream budgets:** Start with 32,768 total libp2p streams (inbound
  ceiling 32,768, outbound 16,384, still 32,768 combined), 24,576 combined relay
  service streams, 12,288 inbound HOP protocol streams and 12,000 outbound STOP
  protocol streams. Use 64 combined streams per Peer ID as the outer peer
  ceiling. A successfully active circuit uses one HOP plus one STOP; STOP
  therefore bounds settled active circuits to at most 12,000 before any tighter
  limit. This leaves arithmetic room for HOP reservation/control work, but
  pending CONNECT requests can occupy that room: it is not a reserved lane or
  flood-proof fairness promise. Preserve sixteen circuits per Peer ID and leave
  explicit system/protocol/service-peer headroom for Identify/Ping/Relay Auth/
  Rendezvous. There is no protocol fork or new application circuit registry.
- **Q319 — Memory and descriptors:** Set global Resource Manager accounted
  memory to 512 MiB, with explicit subordinate budgets derived next. Set Go's
  soft runtime memory target to 1,536 MiB within the existing 2-GiB container
  limit. These are overlapping controls, not 512+1,536 MiB of independent
  allocations, and neither measures all process/kernel/native memory. Cap
  Resource Manager-accounted connection descriptors at 8,192 and require a
  verified process soft descriptor limit of at least 16,384 to leave room for
  HTTP, database and other handles. Do not inherit host-memory-based autoscaling,
  silently lower the target on a host with insufficient descriptors, or claim
  the values prove 5,000-session capacity. Fail configuration/preflight clearly
  if the deployment cannot supply the accepted envelope.
- **Q320 — HTTP work and input bounds:** Permit at most 128 concurrent public
  portal/discovery HTTP requests, with no unbounded application queue. Keep
  private operational checks on a separate listener with their own small budget
  to be specified with probes; public saturation must not consume that budget.
  Use 16 KiB headers, 8 KiB request target and 16 KiB body for ordinary JSON/form
  account/auth/admin requests; no uploads or compressed request bodies. Start
  with a five-second header deadline, ten-second body-read deadline and
  fifteen-second ordinary handler/write deadline, always shortened by a route's
  tighter accepted bound. WebSocket/libp2p traffic is not a portal request after
  upgrade and must not inherit an ordinary HTTP write deadline. Respond to
  exhausted HTTP concurrency with temporary-unavailable behavior; keep discovery
  429 reserved for its already-set active-session-capacity semantics. Exact
  idle-connection/proxy limits and paginated output ceilings remain a dependent
  operating-profile choice rather than an unbounded default.
- **Q321 — Initial request rates:** Use token buckets with refill rate and
  maximum burst capacity as follows: public portal/discovery HTTP collectively
  200 requests/second, burst 400; after account authentication, ten HTTP requests/
  second/account, burst 20; Relay Authentication attempts globally 200/second,
  burst 400, plus one/second/secured connection, burst two; Rendezvous operations
  globally 500/second, burst 1,000, plus four/second/authenticated connection,
  burst eight. No IP-keyed buckets. Unknown credentials use only bounded global/
  connection gates, never an unbounded attacker-chosen account-key map. Use
  ephemeral bounded live state, not new durable usage or device identity data.
  Operation rates apply to control work, not forwarded bytes or Quota Committed.
  Initial denial closes its connection under the existing auth contract;
  renewal denial preserves the prior valid deadline. Use the accepted five-second
  temporary retry hint as a minimum, extended if needed for actual bucket
  availability. Discovery front-door/rate overload remains 503
  temporarily_unavailable, while its 429 still means active-session capacity;
  other HTTP rate denials may use 429 and Clipp streams their existing
  rate_limited code. Do not globally withdraw discovery because a Rendezvous
  bucket or unrelated lower-level scope is full. Provider exchange concurrency
  and handler/database work gates remain necessary beyond rates.
- **Q322 — Client relay-setup parallelism:** Attempt at most four Relay
  Configurations concurrently, with at most two supported address-dial attempts
  racing within one configuration. Close losing attempts when one effective
  connection wins; every retained physical connection still needs its own
  authentication. Waiting in backoff does not occupy an active setup slot, so
  every configured relay progresses independently without list-order fallback.
  One refresh operation per configuration remains mandatory; no duplicate
  refresh-token use. This limits managed relay setup work, not the number of
  saved configurations or unrelated direct-peer networking.
- **Q323 — Slow retries after account quota/session refusal:** For explicitly
  identified account quota or session-limit failures without a longer server
  hint, retry after five minutes with plus/minus twenty-percent jitter. Preserve
  valid credentials; do not open a browser or turn an ambiguous stock resource
  status into an unsupported quota diagnosis. Honor longer hints, including
  manual retry; use the accepted fast temporary schedule only for appropriately
  classified transient failures. Invalid/revoked renewable credentials require
  explicit user sign-in; Pending/Suspended/Denied or recovery-review restrictions
  are not reasons to repeatedly launch authorization. The weekly reset does
  not override account state or permit a request before a valid retry hint.

Next independent branches include the full Resource Manager scope matrix,
HTTP idle/pagination and provider-work limits, database pools/container split,
login/fence capacities, cleanup/clock/probe settings, and recovery work/storage
thresholds. Source-derived arithmetic and configuration support are not a
benchmark; all remaining values stay unapproved until presented and accepted.

### Third round — Q324–Q331 (accepted)

The user accepted Q324–Q331 with “agree”. These are provisional selected
engineering defaults, not measured capacity. Continue using the existing
domain distinction between Safety Limits and Relay Quota; no new quota,
identity registry or retention period is introduced.

- **Q324 — Portal connections:** Bound accepted portal/discovery backend HTTP
  connections to 512 and idle keep-alive to sixty seconds. This includes
  connections not yet carrying a complete request; the existing 128 concurrent
  request gate and header/body deadlines still apply, including across any
  multiplexed requests. Do not create an unbounded application wait queue.
  These are application backend connections, not a per-IP/browser allowance or
  a claimed bound on the shared Ingress controller's front-end sockets. Keep
  WSS/libp2p connections outside this portal budget and ordinary HTTP idle/write
  rules after upgrade; their accepted physical and handshake budgets apply.
- **Q325 — Portal result bounds:** Paginate existing account/plan/audit lists
  using stable cursor ordering, fifty rows by default and at most one hundred
  per page. Cap encoded dynamic account/admin response bodies at one MiB;
  bound query work and serialization before allocation, never fetch the full
  list to paginate in memory. Return a continuation for a smaller complete
  page when needed, never silently truncate fields or records. An individually
  unrepresentable record is an explicit controlled error. This does not add
  an audit UI/export surface that was not already specified, limit total
  retained history, or raise the separate sixteen-KiB discovery ceiling.
  Static application assets are not paginated account data.
- **Q326 — Google work:** Allow eight concurrent server-side Google validation/
  exchange workflows with an eight-second combined provider-I/O deadline,
  always shortened by the remaining fifteen-second parent HTTP deadline.
  Acquire admission before consuming the one-use local flow where possible;
  reject saturation temporarily without an unbounded provider queue. No
  database locks span provider I/O. Cache/deduplicate verification-key fetches
  and bound their work under this provider budget; no fallback to unverified
  claims when keys cannot be obtained. An ambiguous one-use code exchange is
  not blindly retried and may require a fresh explicit login. Google failure
  does not invalidate already-issued local credentials or relay readiness.
- **Q327 — Unfinished login capacity and safe overflow:** Permit 1,024 active
  process-local login continuations, with a sixteen-KiB retained payload cap
  per continuation, and 4,096 temporary External Identity revocation fences.
  The existing ten-minute flow lifetime remains. Refuse new login starts when
  continuation capacity is full; do not evict a valid flow just for a newcomer.
  For the separate exceptional case where a security mutation cannot install
  a required fence, invalidate all still-unfinished login flows atomically
  under the same issuance/revocation serialization (including callbacks already
  exchanging with Google), then reclaim fences no surviving flow needs. This
  lets the security mutation proceed without dropping protection. New flows
  start only after that transition. Existing durable Portal Sessions/Login
  Grants and live Relay Sessions of unrelated accounts remain unaffected.
  This is an explicit accepted refinement of Q239's fail-closed capacity rule,
  not permission to evict needed fences or pretend the mutation succeeded.
  Physical expired-row cleanup remains independent of authoritative validity.
- **Q328 — Database pool and deadlines:** Use one runtime pool of at most eight
  connections, including ordinary application cleanup, and at most 64 admitted
  runtime database work units combined (active or waiting). Refuse excess work
  temporarily according to the operation's existing contract; no unbounded
  goroutine/pool/account-lock wait queue. Acquire this work admission before
  waiting on logical account/identity guards. Allow at most 500 milliseconds
  for pool acquisition and three seconds for the whole ordinary database unit,
  including logical guards, acquisition, statements and commit, shortened by
  its enclosing operation. Set runtime-role statement timeout to two seconds,
  lock timeout to 250 milliseconds and idle-in-transaction timeout to five
  seconds. Do not impose those runtime SQL timeouts on backup, bootstrap or
  migration roles. Explicit rollback/release and operation-specific ambiguous
  commit recovery remain mandatory; cancellation does not prove rollback and
  retries do not reset the deadline or grant extra credit. This is concurrency
  protection, not a throughput promise. Pool lifetime/idle/connect settings,
  total PostgreSQL client allowance and PostgreSQL memory knobs follow from
  this pool and the proposed container split; the research note's candidates
  are not implicitly accepted here.
- **Q329 — Bundled database container split:** Keep the aggregate requests of
  500m CPU/512 MiB and limits of one CPU/1 GiB. Allocate PostgreSQL plus its
  local archive helper requests 400m/384 MiB and limits 750m/768 MiB; the
  distinct-UID backup scheduler requests 90m/96 MiB and limits 225m/192 MiB;
  the reload watcher requests 10m/32 MiB and limits 25m/64 MiB. Each container
  has its own limit, so unused sibling capacity does not increase that limit.
  These small provisional budgets require testing simultaneous database and
  backup work; failures require explicit retuning, not hidden extra resources.
  Separate maintenance Jobs retain their already accepted budget. Detailed
  PostgreSQL memory, helper concurrency and scratch settings remain subsequent
  dependent choices rather than an approval of every research candidate.
- **Q330 — Application database cleanup:** Run one in-process cleanup worker
  at startup and once per minute, with at most thirty seconds of total work
  per pass and transactions deleting at most 250 eligible rows each. Use the
  ordinary short database deadlines, indexed eligibility scans and existing
  lock order; resume unfinished batches fairly on later passes. No overlapping
  worker or unbounded cascade. Warn when eligible cleanup backlog age exceeds
  one hour, escalate at twelve hours, and record a breach if a required
  twenty-four-hour purge deadline is missed. These are incident thresholds,
  not extensions of retention or guarantees during outages. Completed account
  deletion starts its purge clock; pending Recovery Journal deletion remains
  a separate fenced workflow, never bypassed by cleanup. Security expiry and
  access denial apply immediately even when physical deletion lags. Backup/
  journal repository cleanup uses its separately bounded maintenance process,
  not this SQL worker.
- **Q331 — Relay health-check budget:** Use the existing private /readyz startup
  probe every five seconds, two-second timeout, 120 consecutive failures
  (approximately ten minutes before startup failure). Once started, readiness
  checks every five seconds with two-second timeout and one failure/success;
  liveness checks /livez every ten seconds, two-second timeout and three failures.
  Startup uses readiness as already agreed, so persistent initial address
  publication failure can eventually trigger restart; after startup it only
  withdraws readiness, not liveness. PostgreSQL/capacity do not become readiness
  or liveness dependencies. Bound the private listener to 32 connections and
  sixteen active requests, with at most two concurrent metrics scrapes,
  one-second health-handler and five-second metrics deadlines, two-second
  header deadline and thirty-second idle timeout. Collect only bounded local
  state for health/metrics, not synchronous database/provider calls. The private
  surface remains unexposed publicly; rate limiting metrics must not consume
  every health-handler slot. Database-specific probes, maintenance Job limits,
  clocks and watcher settings still require their own compatible defaults.

HTTP timeout semantics are documented by [Go's HTTP server API](https://pkg.go.dev/net/http#Server);
connection admission is an additional bound, not supplied by IdleTimeout alone.
Probe behavior follows [Kubernetes probe documentation](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/).
The chosen numeric values above are recommendations, not values mandated by
those sources. No runtime, chart or cluster configuration was changed.

The [database budget findings](../research/operational-database-budget-facts.md)
check pool/timeout semantics and container arithmetic. Only Q328–Q329's
explicit values were accepted from that note in this round; additional
PostgreSQL tuning candidates in that note remain unapproved. The complete
Resource Manager matrix, subordinate database/HTTP-provider settings, clocks,
database probes, certificate watcher, maintenance and recovery work/storage
bounds remain on this claimed ticket, not silently delegated to implementation.

### Fourth round — Q332–Q336 (accepted)

The user accepted Q332–Q336 with “agree” on 2026-09-08. These remain
provisional selected defaults, not performance evidence. Q336 authorizes the
focused prototype, not a resource-budget increase or a lower acceptance target.
The resulting [Prototype transport capacity and memory accounting](17-prototype-transport-capacity-and-memory-accounting.md)
is resolved under revised Q337: keep limits and goals unchanged and defer
capacity tuning until code is ready. This ticket remains claimed for its other
unresolved defaults, no longer blocked on further capacity experiments.

- **Q332 — Total PostgreSQL connection allowance:** Configure bundled PostgreSQL
  for 32 connections, including three superuser-reserved emergency slots and
  no additional reserved slots. Budget eight runtime clients including ordinary
  cleanup, one separate repository/journal maintenance client when needed,
  one reload watcher, up to two backup-control clients, one database probe,
  one bootstrap-or-migration client, and three operator/emergency clients:
  seventeen planned slots with fifteen unassigned. Serialize each single-client
  category and bound actual backup-control connections; a SQL role name alone
  does not enforce concurrency. No serving role gets emergency-slot privileges.
  Probe/startup implementation and the pinned backup command must fit this
  ledger; exceeding it requires explicit retuning, not hidden connections.
  External PostgreSQL must provide the corresponding application/maintenance
  allowance without the chart changing the operator's global server settings.
- **Q333 — Small-memory PostgreSQL profile:** Use shared_buffers=128MB,
  work_mem=2MB, hash_mem_multiplier=2, maintenance_work_mem=32MB and
  autovacuum_work_mem=16MB. Keep autovacuum enabled with at most two active
  workers; disable parallel query and parallel maintenance workers. Apply a
  64MB temporary-file limit per runtime backend, not a claimed whole-volume
  limit. PostgreSQL's MB units here are binary; these allowances overlap its
  existing 768-MiB container limit and do not increase it. Query plans can
  allocate work memory multiple times, so these knobs are not an RSS proof.
  Preserve durability and TLS; no disabling vacuum or synchronous commit to
  make the provisional budget appear sufficient. Maintenance/restore roles
  need bounded, explicit profiles under their own operation deadlines, not
  unbounded exceptions. Test vacuum progress, temporary-file growth and
  concurrent backup/foreground load before accepting capacity claims.
- **Q334 — Runtime pool lifecycle:** Retain the accepted eight-connection
  maximum with a zero minimum, five-minute idle limit and thirty-minute
  connection lifetime plus uniformly selected zero-to-five-minute jitter.
  Check pool health every sixty seconds. Bound a new connection attempt to
  three seconds, always shortened by its owning context; this does not extend
  the 500-ms foreground acquisition or three-second whole-operation deadline.
  Background connection creation/health work stays within the same pool's
  bound, not an extra pool. Retire borrowed connections safely on release;
  never kill an in-use transaction merely because its pool lifetime elapsed.
  Explicit startup connectivity/schema checks remain required; creating a
  pool object is not evidence of a working database connection.
- **Q335 — Clock-health thresholds:** Sample relay/database clock agreement
  at startup and every sixty seconds with bounded work through the runtime
  pool. Use request/response bracketing, not an unqualified midpoint, to account
  for measurement uncertainty. Warn when the measured offset interval cannot
  be contained within plus/minus one second. Fail time-sensitive issuance/
  admission and new quota allocation closed when the interval cannot be
  contained within plus/minus five seconds, when a usable bound cannot be
  established within one second of
  round-trip uncertainty, or when no valid sample remains within five minutes.
  An operation's authoritative after-lock timestamp can refresh the check;
  separate probes do not replace per-operation database time. Clock regression
  and unsafe week transitions remain fail-closed even below warning thresholds.
  Recover after three valid samples at the ordinary cadence, without extending
  old deadlines, restoring discarded credit or resetting Quota Committed.
  Existing sessions retain only their previously confirmed conservative local
  credit/deadlines; a clock incident does not automatically restart the Pod or
  alter the database-independent readiness rule. These thresholds are not
  expiry grace. Relative agreement cannot detect both hosts sharing the same
  wrong UTC time, so infrastructure time synchronization remains a prerequisite.
- **Q336 — Resolve the transport-capacity contradiction before final sizing:**
  Keep the 5,000-concurrent-session acceptance goal and the no-fork transport/
  Circuit Relay boundary, but add a focused transport-capacity prototype before
  finalizing the Resource Manager profile. The accepted 512-MiB accounted-memory
  and two-GiB container settings remain provisional; neither a larger deployment
  budget nor a reduced target is approved by this question. Compare TCP, WSS,
  WebRTC Direct and representative mixed workloads, measuring idle sessions,
  active circuits, churn and concurrent control work. Capture accounted memory
  separately from RSS/Go/native memory and CPU, and verify priority/scope limits.
  Start with small staged loads in an isolated development harness; do not
  assume the local machine proves OCI ARM64 capacity, generate public traffic,
  mutate the cluster or provision larger infrastructure. Report evidence and
  explicit budget/target choices to the user; do not silently increase limits,
  bypass honest memory accounting, force a different transport on clients or
  declare 5,000 sessions achieved. On approval, create a separate prototype
  decision ticket and make completion of the operational-limits ticket depend
  on its evidence and the user's resulting sizing decision. Independent
  operational decisions can continue while the network profile is unresolved.

#### Newly verified sizing conflict behind Q336

Pinned go-libp2p v0.49.0 WebRTC Direct reserves 2,631,680 bytes per incoming
connection (ten maximum 257-KiB receive messages), at medium Resource Manager
priority. With priority 152, the applicable memory threshold is 153/256 of the
scope limit. Thus the 512-MiB system memory setting admits at most **121** such
reservations even with no other memory use; tighter scopes/other work can reject
earlier. For 5,000 such connections, the reservations alone total 13,158,400,000
bytes and require a system memory ceiling of at least 22,016,669,282 bytes
(about 20.5 GiB) at that priority, before other reservations. These are source
arithmetic bounds, not measured practical capacity or eagerly resident memory.
The Pion receive-buffer cap and the Resource Manager reservation are separate
operations; increasing the latter does not prove a small container remains safe.
No supported production buffer-size option was found in this pinned transport.
This materially qualifies the earlier top-level budget consistency check,
which checked intersections/arithmetic but not each transport's reservations.

Verified against the local pinned module source, with primary source pointers:
[receive-message size](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/stream.go#L43),
[connection buffer size](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/transport.go#L78),
[incoming memory reservation](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/transport/webrtc/listener.go#L222-L224),
[priority](https://github.com/libp2p/go-libp2p/blob/v0.49.0/core/network/rcmgr.go#L143),
and [memory threshold](https://github.com/libp2p/go-libp2p/blob/v0.49.0/p2p/host/resource-manager/scope.go#L105-L138).
Retain this conflict as an explicit capacity caveat. Revised Q337 defers its
configuration tuning until code is ready; it does not require resolving the
5,000-session sizing conflict to finish this planning ticket. Remaining
maintenance, provider, storage and subordinate scope settings are still
unresolved, not part of Q332–Q337's approval.

### Handoff after revised Q337 — 2026-09-08

The user chose unchanged limits and goals with lower initial demand. Continue
remaining operational decisions using the accepted provisional baseline;
do not enlarge budgets, reduce the target or accept the research scope matrix
implicitly. Next question numbering starts at Q338. Capacity tuning and
representative enforced validation belong after implementation, before any
claim to support the full target, rather than blocking implementation readiness.

The [database budget findings](../research/operational-database-budget-facts.md)
provide source support for Q332–Q334, not numeric acceptance. Clock conversion
uses [Go monotonic-time semantics](https://pkg.go.dev/time#hdr-Monotonic_Clocks)
and PostgreSQL's [actual current-time function](https://www.postgresql.org/docs/18/functions-datetime.html#FUNCTIONS-DATETIME-CURRENT),
not transaction-start time. Q335's thresholds are engineering recommendations.

### Fifth round — Q338–Q347 (accepted 2026-09-26)

The user accepted Q338–Q347 with “agree with q338-347” on 2026-09-26.
These independent choices use the accepted topology, workload and budgets.
Numbers are initial engineering defaults, not vendor requirements or measured
capacity. No production files, chart values or cluster resources are changed.
Acceptance applies to this round, including the scope tables explicitly adopted
by Q338, not unrelated research candidates. Revised Q337 still defers capacity
tuning until code is ready without changing the scale goal or existing budgets.

- **Q338 — Complete the subordinate Resource Manager profile:** Adopt the
  three complete scope tables in
  [Explicit Resource Manager profile](../research/operational-resource-scope-profile.md#complete-candidate-scope-matrix-conditional-on-accepting-the-intersections-above)
  as the initial subordinate configuration, preserving all accepted aggregate
  ceilings. In particular: 32-MiB peer, 8-MiB connection and 1-MiB stream memory
  ceilings; explicit Identify/Ping/Auth/Rendezvous/relay service and protocol
  bounds; unknown and allowlisted scopes blocked. Use explicit fixed settings,
  not host autoscaling or hidden built-in overrides. The tables' zero means
  block, not inherit. Account for custom buffers and assert the enabled protocol
  inventory. This adopts the tables, not the research note's superseded demand
  for immediate target sizing; revised Q337 governs timing. Intersecting limits
  may reject earlier than any advertised connection/stream count and do not
  reserve a guaranteed control lane.
- **Q339 — Bound Google responses and verification-key caching:** Retain eight
  provider workflows and the combined eight-second provider budget. Limit
  provider response headers to 16 KiB and each decoded response body to 256 KiB;
  bound reads before parsing, including decompression. Retain at most 32 keys
  in one provider key set, with a 256-byte key-ID bound and no attacker-keyed
  growing negative cache. Respect provider cache directives with a local
  six-hour maximum freshness; do not extend a shorter provider lifetime or use
  expired keys on retrieval failure. A still-fresh matching cached key remains
  usable after a failed refresh; failure never extends freshness. Deduplicate
  fetches. For an unknown key ID,
  allow one bounded refresh, subject to a shared sixty-second refresh cooldown
  (initial empty-cache fetch excepted), not one fetch per arbitrary key ID.
  A cooldown may temporarily reject a newly rotated key; retry via an explicit
  fresh login where the one-use exchange requires it. Invalid/oversized data
  or unavailable verification fails login closed without invalidating local
  credentials already issued. Only configured provider endpoints may be fetched;
  token-controlled URLs and arbitrary redirects are not authority.
- **Q340 — Bundled PostgreSQL health checks:** Check final-server acceptance
  at an explicit single loopback TCP address, not the readiness-dependent
  Service or initialization socket. Keep verified-TLS client parameters with
  the configured certificate hostname and trust; do not add a socket exception.
  Use a two-second inner timeout, three-second Kubernetes timeout, five-second
  interval, two readiness failures/one success, and 360 startup failures
  (approximately thirty minutes). Serialize within the one-probe allowance.
  Server acceptance is not authenticated role/schema/certificate-rollout
  validation: fresh authenticated pre-serving checks remain separate, and the
  probe must not depend on the reload role being provisioned. Omit a database
  I/O-dependent liveness probe; process exit still permits ordinary restart.
  Explicit offline restore uses a separately configured startup allowance
  within its run deadline instead of repeatedly restarting slow recovery.
  Verify exact probe TLS/failure-log behavior against the pinned image.
- **Q341 — Certificate watcher cadence:** Poll complete published certificate
  generations every thirty seconds, one operation at a time; ten seconds total
  for reload plus fresh-handshake verification. Retry after 5, 15, 30 and then
  60 seconds, with plus/minus twenty-percent jitter at the capped interval.
  Recheck the served certificate every five minutes even without a detected
  change. Warn after five minutes without convergence and at seven days before
  certificate expiry; escalate at twenty-four hours remaining or immediately
  on expiry. Wait quietly for initial role provisioning. Do not weaken TLS,
  restart PostgreSQL on renewal failure, or treat Secret polling as a delivery
  guarantee. Short-lived certificates need explicitly adjusted warnings.
- **Q342 — Offline maintenance deadlines:** Default bootstrap Jobs to ten
  minutes, schema-migration Jobs to thirty minutes, and an explicit offline
  restore/reconciliation run to three hours. Keep no automatic failed-Job retry,
  retained failure evidence and the stopped/fenced preconditions. Bootstrap and
  migration SQL use five-minute statement, five-second lock and sixty-second
  idle-in-transaction limits, shortened by the remaining whole-run deadline;
  retain 32-MB maintenance memory, disable parallel maintenance and cap temporary
  files at 256 MB per such backend. Backup/restore SQL has its own operation
  profile, not the ordinary runtime's two-second timeout. Timeout never proves
  rollback or worker termination. A reviewed new run can explicitly override
  these deadlines for a known operation without increasing CPU/memory budgets.
  The three-hour restore cap is not proof of the four-hour RTO, which includes
  incident response and reopening work.
- **Q343 — Backup and archive work:** One active full backup and at most one
  coalesced pending request; serialize manual/check backup-control commands
  through the same scheduler. Use pgBackRest process-max=1, buffer-size=1MiB,
  synchronous archive-push, db-timeout=30m, protocol-timeout=31m,
  io-timeout=60s and archive-timeout=120s. Bound the whole scheduled backup to
  two hours; retry a failed backup after fifteen minutes with plus/minus
  twenty-percent jitter rather than accumulating missed daily runs. A local
  archive attempt has a two-minute watchdog, must return failure on uncertainty,
  and must terminate/reap its own children before another attempt overlaps.
  Test cancellation and resumability; a killed client is not proof server work
  stopped. Retain separate cleanup authority, no automatic backup expiry and
  no queue-driven WAL dropping. The daily-full/sixty-second-WAL schedule and
  existing CPU/memory/SQL-connection budgets do not change.
- **Q344 — Temporary disk bounds:** Use disk-backed scratch limits of 256 MiB
  for backup work, 64 MiB for the archive helper, 8 MiB for the shared lock
  directory, 8 MiB for the watcher and 256 MiB per maintenance Job. Stream
  backups/restores to the intended repository/data volume, never stage the
  full database in scratch. Do not change persistent volume or memory budgets.
  Scratch exhaustion fails the operation visibly without discarding required
  WAL or expanding privileges. These volume limits neither reserve node space
  nor synchronously cap every write. Per-container ephemeral-storage allowances
  must subsequently include logs/writable layers as well as these paths.
- **Q345 — Pending deletion and Recovery Journal work:** At most 1,024 durable
  pending deletions and one serialized append/reconciliation worker. Refuse a
  new deletion before starting its irreversible/fenced workflow if capacity is
  unavailable; an already accepted deletion is never evicted, expired or
  reported complete without committed journal proof. Repeated requests find
  the same operation. Use a twenty-second worker-attempt deadline, five-second
  per-object-request deadline, and retries after 1, 2, 4, 8, 16, 30, then 60
  seconds with half-to-full jitter; manual requests do not bypass backoff.
  Fairly revisit other due operations after a failed attempt. Keep database
  units under their existing short deadlines, with no locks during storage
  I/O. Bound encoded events to 4 KiB and head/checkpoint documents to 16 KiB;
  stream bounded pages during reconciliation rather than loading the journal.
  Warn at fifteen minutes pending, escalate at one hour and at 80% pending
  capacity. Existing permissions remain fenced, while unrelated accounts and
  account-wide security revocation remain independent of journal availability.
- **Q346 — Repository and journal cleanup:** Run one non-overlapping cleanup
  pass each hour, with a thirty-minute whole-pass deadline and custom listing/
  deletion pages of at most 250 objects. Backup expiry remains tool-driven and
  chain-aware, not raw object-count deletion; paginate all required evidence
  before advancing a cutoff. Preserve resumable progress, frozen deletion
  targets, generation checks and separate cleanup credentials. Warn when a
  scheduled successful pass is overdue by one hour, escalate at six hours overdue,
  and explicitly report retention deadline breaches. A timeout does not release
  a journal maintenance fence until worker termination and safe reconciliation
  are proven. No new deletion commits through an uncertain generation; unrelated
  relay traffic continues. Existing retention periods do not change.
- **Q347 — Recovery and storage warning thresholds:** Sample available recovery
  and disk-pressure observations every sixty seconds with bounded work. Warn
  when the latest proven recoverable state is more than five minutes behind;
  mark the fifteen-minute RPO as breached when exceeded or not provable. Track
  archive backlog/progress and baseline validity, not only the last backup's
  success timestamp. Warn if no successful daily full backup exists within
  twenty-six hours and escalate after forty-eight hours. For the bundled data
  filesystem, warn at 80% used, escalate at 90%, and require operator-controlled
  protective maintenance at 95% (or earlier if growth predicts exhaustion).
  This means the existing stop/drain procedure, not automatic PostgreSQL kill,
  WAL deletion, PVC expansion or quota reset. Continued archival/recovery work
  remains available. These observations are not a hard disk-exhaustion guarantee;
  late operator response can still exhaust storage. External databases require
  equivalent provider/operator signals; missing data is unknown, not healthy.
  Show state in private metrics and transition logs without requiring an
  installed Prometheus; actual alert delivery is an operating-criteria decision.

Evidence: [resource scopes](../research/operational-resource-scope-profile.md),
[maintenance mechanics](../research/operational-maintenance-facts.md),
[pgBackRest configuration](https://pgbackrest.org/configuration.html),
[Kubernetes temporary volumes](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir),
[PostgreSQL probe semantics](https://www.postgresql.org/docs/18/app-pg-isready.html),
[Google key caching](https://developers.google.com/identity/openid-connect/openid-connect#validatinganidtoken),
and [OIDC signing-key rotation](https://openid.net/specs/openid-connect-core-1_0.html#RotateSigKeys).
Provider cache lifetimes and probe acceptance must not be misrepresented as
fixed Google TTLs or authenticated serving checks. This round was accepted;
Q344's explicit ephemeral-storage follow-up is settled by accepted Q348 below.
No new domain term was introduced, so the glossary is unchanged.

### Sixth round — Q348 (accepted 2026-09-26)

The user accepted Q348 with “agree” on 2026-09-26. The following values are
selected provisional defaults, not a deployment change or capacity proof.

**Q348 — Complete the temporary-storage budget:** In addition to the accepted
disk-backed scratch-volume bounds, set these initial per-container Kubernetes
ephemeral-storage requests/limits. These cover writable layers and container
logs; the Pod-level allowance must also accommodate its shared disk-backed
emptyDir volumes. They are not extra scratch allowances or memory limits.

| Container | Request | Limit |
| --- | --- | --- |
| Relay | 64 MiB | 128 MiB |
| PostgreSQL plus local archive helper | 128 MiB | 256 MiB |
| Backup scheduler | 256 MiB | 512 MiB |
| Certificate reload watcher | 16 MiB | 32 MiB |
| Each maintenance Job's single worker | 256 MiB | 512 MiB |

The bundled database Pod totals 400-MiB request and 800-MiB limit. Its accepted
scratch caps total 336 MiB (backup 256 + archive 64 + shared locks 8 + watcher 8),
leaving nominal aggregate headroom for logs/writable layers, not proof of safe
peak use. PostgreSQL data, retained WAL on its PVC and off-cluster repositories
are outside ephemeral storage and retain their existing budgets/policies.
Keep read-only root filesystems and only the accepted writable scratch paths;
do not introduce a new disk-backed relay queue or log files. Logs remain stdout/
stderr under the accepted privacy/retention rules. These scheduling/eviction
settings do not configure node log rotation or guarantee synchronous disk caps.
Verify node log rotation, supported storage accounting and pressure behavior
in deployment acceptance rather than silently changing cluster settings.

Exhaustion must be reported and recovered under the existing operation/restart
rules, never by deleting required WAL, resetting quota or expanding limits
automatically. Keep CPU, memory, persistent-volume sizes and the 5,000-session
goal unchanged. Values are provisional engineering defaults, not measurements.

This settles the identified remaining default decision. Actual alert
delivery, deployment checks and evidence for release remain with
[Define acceptance, release, and operating criteria](13-define-acceptance-release-and-operating-criteria.md),
not additional implicit approvals in this round.

## Answer

Resolved on 2026-09-26 following the user's acceptance of Q348. The accepted
rounds above define the initial operational Safety Limit profile: admission,
lifetimes, retries, control payloads/rates, explicit Resource Manager scopes,
HTTP/provider/database work, probes and certificate reload, maintenance and
backup work, pending deletions, cleanup, storage warnings and temporary-storage
budgets. Q338 explicitly incorporates the linked scope tables; no other
unaccepted research candidate becomes a default by implication.

All identified default choices in this ticket are settled. Revised Q337 remains
authoritative: keep the existing CPU/memory budgets and the 5,000-session goal,
start with lower demand and tune capacity after code is ready. Configuration
ceilings are not simultaneous-capacity guarantees; known transport-accounting
constraints remain visible. No protocol fork, IP throttling, new domain term,
production implementation, cluster change or deployment was authorized here.

[Define acceptance, release, and operating criteria](13-define-acceptance-release-and-operating-criteria.md)
is now unblocked. It owns release evidence, operator alert delivery, node log
rotation/storage-accounting verification, failure/cancellation/recovery tests
and the distinction between initial lower-load release and eventual target
capacity. The next question number is Q349. No further in-scope fog or new
decision ticket is identified by this resolution.
