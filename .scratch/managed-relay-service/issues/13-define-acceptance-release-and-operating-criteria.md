# Define acceptance, release, and operating criteria

Type: grilling
Status: resolved
Blocked by: 08, 10, 11, 12, 14, 15, 16

## Question

What end-to-end compatibility, security, privacy, conservative quota, single-instance performance, restart recovery, migration, observability, multi-architecture CI, Helm validation, non-overlapping rollout, rollback, and operator-runbook criteria must be satisfied before the specification is implementation-ready and the resulting relay can be released through Argo CD?

## Comments

- Claimed on 2026-09-26 for the user's Wayfinder next-frontier request after
  every prerequisite resolved. Use grilling and domain-modeling; continue at
  Q349. This ticket defines acceptance and operating decisions, not production
  implementation, CI execution, environment provisioning or deployment.

- Q338–Q347 were accepted on 2026-09-26 in
  [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md).
  Carry its explicit scope profile, provider response/cache limits, final-server
  probe/pre-serving distinction, watcher timing, maintenance cancellation,
  backup/scratch bounds, durable pending deletion and fenced cleanup behavior
  into verification. Recovery-lag/storage thresholds require observable
  incidents and an operator stop/drain procedure, not automatic WAL deletion,
  quota reset or database kill. Decide actual alert delivery without assuming
  Prometheus exists. Q348's per-container ephemeral-storage follow-up was also
  accepted on 2026-09-26, resolving the limits prerequisite. Include node log
  rotation, actual storage accounting and pressure/restart behavior in deployment
  checks without changing cluster settings implicitly. This acceptance ticket
  is now unblocked; its claim is recorded above and numbering continues at Q349.

- Revised Q337 in [Prototype transport capacity and memory accounting](17-prototype-transport-capacity-and-memory-accounting.md)
  keeps current limits and the 5,000-session goal unchanged, starts with lower
  demand and defers capacity tuning until code is ready. Separate initial
  lower-load release validation from eventual target-capacity validation; do
  not make a proven 5,000-session profile a planning-completion or initial-scale
  requirement. Preserve the known WebRTC admission/accounting constraint and
  pressure effects in operating guidance. Before claiming the full target,
  validate representative transports, active circuits, real account/control
  work and recovery under enforced Linux ARM64 budgets, coordinating application
  limits with Go/container settings. This supersedes earlier timing language,
  not the scale goal or security, correctness and recovery gates.

- Accepted Q305–Q307 and the [recovery contract](../recovery-contract.md) add
  distinct-UID backup permissions across first boot/restart, private socket
  authentication without superuser bypass, bootstrap/backup readiness separation,
  cleanup generation/cutoff safety and verified initial/pre-migration/post-restore
  baselines. Exercise interrupted pruning and pending deletions while unrelated
  relay traffic continues. Test structural recovery before portal reopening and
  per-account review thereafter, not a portal-review startup deadlock. Numeric
  work/resource controls remain owned by the limits prerequisite.

- Accepted recovery Q297–Q304 add pgBackRest/OCI integration and derived-image
  checks, independent recovery-material access, scoped journal credentials,
  committed-sequence completeness and ambiguous-write tests, pending-deletion
  crash recovery, account review holds, reset-notice privacy and idempotent
  offline restore runs. Verify current administrator authority rather than
  reviving an old allowlist. Record backup degradation without false recovery
  guarantees or automatic zero-on-error quota behavior. Unverified resource
  or permission mechanics remain in the recovery/limits prerequisites.

- Recovery Q288–Q296 are accepted, with the user's revised Q289 allowing zero
  consumption for an unrecoverable current week, not a week-long service pause.
  Verify this is restore-only, preserves recoverable history and restrictions,
  does not revive deleted accounts or old credentials, and is reported as a
  recovery adjustment rather than exact traffic. Q293 sets a thirty-day restore
  cutoff/deletion-request deadline with monitored provider cleanup, not an
  exact physical-erasure guarantee. The recovery ticket still owns the remaining
  design choices; these accepted handoffs do not resolve that prerequisite.

- The resolved [deployment contract](../deployment-contract.md) now makes the
  operator runbook scope concrete: first install, same-schema replacement,
  schema maintenance, credential/CA rotation, degraded database/API/publication,
  resource exhaustion, failed Jobs, uninstall and uncertain node/volume state.
  Cover the watcher/bootstrap readiness cycle, executable empty-data guard,
  generated-name consistency and target-specific Job completion in verification.
  Recovery policy comes from the existing recovery prerequisite, not a new
  unticketed operator-runbook decision.

- Accepted Q274–Q279 in
  [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md)
  add inbound-only relay/egress verification, certificate-reload-role isolation,
  missing-data startup guards, target-specific maintenance checks and preflight.
  Apply Q275's explicit restricted infrastructure-diagnostic exception (maximum
  seven days, including exports) while keeping application logs/audit strict;
  synthetic credential leakage remains a deployment defect, not an allowed
  diagnostic field. Do not claim arbitrary upstream error text is sanitized.

- Accepted Q265–Q273 in
  [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md)
  require direct-Pod OCI exposure, verified ingress-policy enforcement,
  best-effort drain checks across TCP/UDP/WSS, retained expandable database
  storage, bundled PostgreSQL 18 and external PostgreSQL 17/18 verification,
  inspectable maintenance Jobs, and deliberate pruning/rollback. Test the
  provisional Q268 resource envelope against the unchanged acceptance target;
  do not treat user approval of budgets as capacity evidence. Verify actual
  cluster allocatable capacity, network-policy engine and storage behavior
  before production deployment. No release verification was performed by
  accepting these planning decisions.

- The user's revised Q242 in
  [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md)
  requires both external and chart-managed PostgreSQL. Include both modes in
  Helm, TLS, credential-isolation, migration, persistence, and recovery release
  checks; bundled database lifecycle details remain with that claimed ticket.

- [Choose initial Quota Plan and Safety Limit defaults](12-choose-initial-quota-and-safety-limit-defaults.md)
  records a user-accepted provisional baseline to revise with production
  experience. Treat its values as initial configuration, not a load-test result.
  Do not infer that the 5,000-session ARM64 target or existing release checks
  have been waived.
- Carry the source-based large-history cutoff/replay risk and shared-Ingress-IP
  reservation behavior into explicit compatibility and operating criteria.
  Define aggregate, privacy-preserving observations for revisiting circuit
  cutoffs, capacity rejections, credit allocation/stranding, and resource use.
  No recurring monitoring job or production rollout is authorized by this
  planning decision.
- [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md)
  now supplies the accepted provisional numeric profile. Its resolution is not
  evidence that an implementation or deployment satisfies those bounds.

### First round — Q349–Q358 (accepted 2026-09-26)

The user accepted Q349–Q358 with “agree” on 2026-09-26. This settles acceptance
policy, not the outcomes of any future production test or deployment.

Read-only repository inventory on 2026-09-26 found Clipp's root `npm run check`
(type checks plus Jest), separate lint/format checks, Android release checks and
separate Chromium extension verification scripts. It found no tracked CI
pipeline in either repository and no production Go module, image build or Helm
chart in clipp-relay; the scratch modules are prototypes. Electron has build/dev
scripts but no observed installer/OS packaging matrix. These are repository
facts, not proof of passing tests or absence of external infrastructure.
See [Clipp scripts](/Users/invine/src/js/clipp/package.json),
[Android scripts](/Users/invine/src/js/clipp/apps/android/package.json) and
[extension scripts](/Users/invine/src/js/clipp/apps/extension/package.json).
Q350 describes the accepted managed-client target, not a claim that today's
client defaults already implement it. In particular, shared browser runtime
transport availability and actual relay reservations must be tested, not
inferred from imports or a successful shared-core test.

These questions select acceptance policy, not new feature behavior or capacity
limits. Existing decisions remain authoritative; an unexpected implementation
conflict must be reported rather than silently waived. No test, infrastructure
setup, release publication or production deployment is authorized by this round.

- **Q349 — Separate planning completion from release readiness:** Close this
  map once the decisions and acceptance checklist are agreed and internally
  consistent. Produce a concise implementation-handoff index that points to
  authoritative contracts and tickets rather than duplicating their decisions.
  Production code and executed evidence come in a separately authorized build.
  Mark each future acceptance item passed, failed or not run; prototypes and
  approved limits cannot stand in for production acceptance. Initial lower-load
  release and eventual 5,000-session validation are distinct, following Q337.
- **Q350 — Real-client compatibility gate:** Before the first release claiming
  managed-relay support, exercise Electron, Android and Chrome extension clients
  end to end. Require all runtime-supported transports: Electron TCP/WSS/WebRTC
  Direct; Android and extension WSS/WebRTC Direct. Force each transport so a
  working fallback does not conceal a broken path; force actual relay traffic
  so a direct connection cannot produce a false pass. Cover same-runtime and
  cross-runtime peer pairs, both circuit directions, cross-account peers, exact
  Rendezvous v1/v2 compatibility, multiple independent Relay Configurations and
  the separately configured unauthenticated-relay mode. Verify ordinary clip
  sync and history transfer, including the known stock circuit byte/duration
  cutoff and reconnect/replay behavior. Large-history failure must be reported
  accurately without silent loss, duplication or uncontrolled retry loops; do
  not promise unlimited transfer or silently weaken the accepted cutoffs.
  Record exact client/runtime/OS versions and unsupported combinations; do not
  claim all desktop operating systems from one machine's test.
- **Q351 — Authentication, account and privacy gate:** Release-blocking tests
  must cover connection-specific Relay Authentication before HOP/Rendezvous,
  replacement/renewal races, token/PKCE/state/callback validation, Google key
  rotation/failure, administrator authorization, account states, Sign out
  everywhere, deletion/re-registration, and credential-storage boundaries.
  Use deterministic provider doubles for hostile/error cases and real Google
  test-account flows through each runtime before initial serving and after
  material authorization changes. Synthetic secret markers must not appear in
  application logs, metrics, URLs exposed beyond their intended callback or
  retained unauthorized storage. Exercise infrastructure diagnostics under
  Q275's restricted exception, never interpreting it as permission for normal
  credential leakage. No release with a known authentication bypass, secret
  disclosure, unauthorized administration or deletion/credential resurrection.
- **Q352 — Quota and persistence correctness gate:** Require invariant tests
  under concurrent admission, replacement, quota allocation and security
  mutations, plus fault injection around durable commit/local installation and
  ambiguous retries. Prove conservative endpoint charging, no ordinary refunds,
  no double credit installation, correct same-account double charging, Monday
  UTC reset, quota-preserving re-registration and bounded shutdown/revocation.
  Test database loss, clock disagreement, process crashes and restart without
  broadening readiness dependencies or ordinary zero-on-error recovery. Exercise
  real PostgreSQL, not only mocked transactions, on supported 17/18 versions
  and both deployment modes. Recovery tests separately prove the accepted
  restore-only zeroing rule, review holds, current administrator authority,
  deletion journal completeness and credential invalidation. A known integrity
  or isolation failure blocks release even if ordinary happy paths pass.
- **Q353 — Automated checks and release artifacts:** Every change must pass
  applicable formatting, static/type checks, unit/contract tests and integration
  checks; include Go race detection for concurrency-sensitive code and the
  Clipp repository's required shared/runtime checks. Release candidates also
  need end-to-end and fault/operation suites, Helm lint/render/schema checks
  for both database modes and deployment phases, and executed Linux ARM64 and
  AMD64 image smoke/integration checks (cross-compilation alone is insufficient).
  Record source revisions, dependency locks, immutable image digests, chart
  version, schema revision, supported client/protocol versions, configuration
  and evidence in a release manifest. Include dependency/image vulnerability
  scan results; reachable serious findings need remediation before release,
  while false-positive or demonstrably irrelevant findings require recorded
  rationale, owner and review date rather than an unexplained green override.
  Missing credentials/devices/runners yield not-run, not pass; no publishing
  permission is inferred from a successful build.
- **Q354 — Real deployment and recovery gate:** Before production cutover,
  test the candidate in an isolated, operator-authorized environment using the
  actual OCI routing, TLS, NetworkPolicy, storage and Argo CD behavior relevant
  to the intended install. Preserve static/environment/live preflight stages.
  Verify first installation, non-overlapping replacement with a new Peer ID,
  stop/migrate/serve, wrong or missing PVC/data rejection, failed maintenance,
  leaf renewal and CA/credential rotation, intentional rollback or stopped
  recovery, storage pressure and uninstall retention. Test all public transports
  from outside the cluster and prove private surfaces stay private. Require a
  timed isolated restore and actual off-cluster backup/WAL/journal evidence,
  including permissions, interrupted cleanup and deletion followed by new
  registration. Keep accepted RPO/RTO and first-serving baseline gates; a chart
  render or backup command exit alone cannot satisfy them. Each advertised
  database mode needs evidence, with actual operator prerequisites additionally
  checked for the selected target. No production fault injection is implied.
- **Q355 — Modest initial endurance and overload evidence:** Once code exists,
  begin with a four-hour Linux ARM64 run at twenty concurrent authenticated
  Relay Sessions, using multiple accounts within their existing session/quota
  allowances and a recorded mix of TCP, WSS and WebRTC Direct. Repeatedly renew
  sessions/reservations, open circuits, exchange bounded traffic and include
  ordinary portal/database/backup work. Check no unexpected crash/OOM, stuck
  renewal, correctness failure, accepted-deadline violation or unreleased live
  resource accumulation over repeated workload/cleanup cycles. Distinguish Go
  heap retention from leaked live objects. Separately force each overload gate
  with isolated test profiles/workloads and demonstrate bounded rejection and
  recovery, slow-reader/control behavior and retry compliance. Record test-only
  overrides and do not pretend they validate a larger production envelope.
  This is a small release smoke/endurance workload, not a new production session
  cap, throughput/latency SLA or replacement for the 5,000-session goal. No need
  to run it, change limits or certify full capacity to finish this map.
- **Q356 — Useful observability without identity tracking:** Provide a bounded
  operational view of session/connection/stream counts, admission refusals by
  safe reason/transport, auth and database/provider failures/latency, restarts,
  memory/CPU/storage pressure, backup/recovery freshness, pending deletions,
  cleanup lag and certificate status. Metrics labels must be bounded enums,
  never account/peer IDs, email, IP, tokens, arbitrary URLs or error strings.
  Keep allowed account-specific audit separate from operational metrics and
  existing account statistics; do not create a device registry or relayed-content
  inspection. Emit redacted structured operational transitions to stdout and
  serve compatible metrics privately; no Prometheus installation required.
  Verify overload does not produce an unbounded diagnostic queue or leak secrets.
- **Q357 — Actual alert delivery is an operator prerequisite:** Local logs and
  metrics alone are sufficient for development, not for claiming unattended
  production monitoring. Before public serving, require an operator-owned monitor
  and a tested notification destination outside the relay process, able to
  detect both critical conditions and loss of observations/relay availability.
  Use existing infrastructure or an external checker; do not build an email,
  webhook-delivery or paging subsystem into the relay and do not require
  Prometheus. Test delivery of an already-detected critical condition within
  five minutes and route warnings in the selected operator system. Existing
  warning/escalation thresholds stay unchanged. Provider selection, credentials
  and provisioning are operator inputs, not a reason to block implementation
  planning; production cutover waits for evidence. No recurring automation or
  external notification is created by accepting this policy.
- **Q358 — Release ownership, runbooks and exceptions:** Require explicit human
  promotion of a recorded candidate through the accepted manual Argo CD phases;
  successful CI never itself authorizes production mutation. Supply tested
  runbooks for install/upgrade, schema maintenance, credential/CA rotation,
  degraded dependencies, resource exhaustion, failed Jobs, suspected compromise,
  backup/restore/journal repair, account review and uninstall. Each names the
  expected signal, safe action, forbidden shortcuts and verification before
  reopening. Known security, privacy, quota-integrity, unsupported-schema or
  unsafe-recovery defects block promotion. Cosmetic layout refinements and
  future high-capacity tuning may remain documented follow-up work; anything
  else omitted or failing needs an explicit decision, not silent acceptance.
  Reuse unchanged evidence only with recorded applicability to the candidate;
  rerun checks affected by protocol, dependency, image, schema, chart, auth or
  backup changes. Keep the accepted quarterly/material-change restore drills.

The round is accepted. No changed branch introduces a further planning decision.
Exact deployment inputs, monitor/provider selection and execution evidence stay
with the explicitly defined operator/release prerequisites. No glossary change
is needed.

## Answer

Resolved on 2026-09-26. Q349–Q358 establish the release gates for real-client
compatibility, security/privacy, conservative quota and persistence correctness,
automated checks and immutable artifacts, actual deployment/recovery evidence,
modest initial endurance, bounded observability, operator-owned alert delivery
and explicit human promotion with tested runbooks.

The accepted contracts and this checklist complete the implementation-ready
decision set. There is no remaining open decision ticket or unticketed in-scope
fog. The [original implementation handoff](../implementation-handoff.md) indexes
the authoritative sources. On 2026-09-27 the user separately requested the
[consolidated build specification](../spec.md); it preserves accepted requirements
without turning scratch prototypes into production code or passed acceptance.

Q337 remains unchanged: retain existing resource budgets and the 5,000-session
goal, begin at lower load and tune after implementation. Q355's twenty-session,
four-hour run is a future initial acceptance workload, not a lowered goal or
new production cap. First-serving security/recovery/monitoring gates remain
mandatory even though target-capacity validation is deferred.

No production implementation, executed release verification, environment setup,
publication, automation or deployment is claimed or authorized by closing this
planning map. A subsequent build requires a separate user request.

### Production acceptance checklist

This is the initial evidence ledger as of planning completion, not a duplicate
of the requirements above. Record candidate-specific evidence and exact scope
when updating a row to **passed** or **failed**; missing or unexecuted checks
remain **not run**. Expand rows into concrete cases during implementation.
Prior prototype results do not mark these production gates passed.

| Evidence area | Authoritative criteria | Current status |
| --- | --- | --- |
| Runtime/transport and cross-runtime relay interoperability | Q350 | Not run |
| Account/authentication/admin/privacy and real Google flows | Q351 | Not run |
| Quota, concurrency, crash boundaries and real PostgreSQL | Q352 | Not run |
| Automated checks, both image architectures and release manifest | Q353 | Not run |
| Helm/Argo/OCI lifecycle, TLS, networking and storage | Q354 and deployment contract | Not run |
| Isolated restore, off-cluster baseline and recovery integrity | Q354 and recovery contract | Not run |
| Initial twenty-session/four-hour endurance and overload recovery | Q355 | Not run |
| Private bounded metrics and redacted stdout diagnostics | Q356 | Not run |
| External operator monitoring and notification delivery | Q357 | Not run |
| Tested runbooks and explicit candidate promotion | Q358 | Not run |
| Eventual 5,000-session capacity claim | Q337; separate from initial release | Not run; deferred until implementation |
