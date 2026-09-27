# Define the Helm and Argo CD deployment contract

Type: grilling
Status: resolved
Blocked by: 02, 06, 07, 09, 14

## Question

What exact Helm values, single-process Kubernetes workload and non-overlapping rollout policy, WSS/raw-TCP/WebRTC-Direct exposure, DNS/TLS, external or chart-managed PostgreSQL and existing-Secret contracts, migration sequencing, probes, metrics exposure, ARM64/multi-architecture image, draining, Argo CD sync behavior, and example `Application` should constitute the production deployment handoff?

## Comments

Claimed with the Wayfinder, grilling, and domain-modeling skills after every
listed prerequisite resolved. This is a deployment decision ticket, not
authorization to implement a chart, alter the cluster, or provision resources.
Continue question numbering after accepted Q241.

### Settled inputs

- One Go process and one-replica StatefulSet; ephemeral Peer ID/certificate,
  no relay PVC, no horizontal scaling, and non-overlapping replacement with
  accepted planned downtime. See
  [Define single Relay Instance lifecycle and discovery](07-define-relay-pool-lifecycle-and-assignment.md).
- PostgreSQL with distinct migration/serving credentials, exact schema
  compatibility and no serving process during schema migration. See
  [Define internal Go modules and PostgreSQL persistence](14-define-go-modules-and-postgresql-persistence.md)
  and its [accepted contract](../persistence-contract.md). The user's Q242
  revision adds a chart-managed PostgreSQL option alongside external PostgreSQL;
  it does not change persistence semantics or put PostgreSQL inside the relay
  process/Pod.
- All three transports, a single client-configured discovery endpoint, existing
  Secret-backed Google/admin/pepper material, and optional Prometheus-compatible
  metrics without requiring Prometheus installation remain settled.
- The [OCI networking research](../research/oci-relay-instance-networking.md)
  records an August 30 cluster snapshot, not a fresh cluster inspection. Its
  F5 NGINX and separate TCP/UDP OCI NLB findings remain useful; replicated
  coordinators, per-ordinal scaling, wildcard requirements, registration, and
  old capacity/readiness coupling are not v1 policy.

### First round — Q242 revised; Q243–Q249 accepted

The user requested both external and chart-managed PostgreSQL for Q242 and
accepted Q243–Q249 together. Q242 below incorporates that explicit scope change;
details of the chart-managed database are not silently settled by it.

- **Q242 — Chart ownership and handoff:** Put the application chart at
  `charts/clipp-relay` in this repository, with documented values, validation,
  and an example Argo CD Application targeting a pinned Git revision. The chart
  owns application workloads, Services/Ingress, narrowly scoped RBAC and
  configuration; it supports either an external PostgreSQL database or a
  PostgreSQL deployment managed as part of the chart. It consumes an existing
  namespace, credential Secrets, ingress controller and issuer. It may request its relay
  OCI NLBs through Services and optionally request a Certificate, but it does
  not provision OCI networking/IAM, external database infrastructure, DNS,
  cluster controllers, or secret-management infrastructure. The bundled
  database's workload, storage, bootstrap, TLS, update and deletion contracts
  require follow-up decisions. Keep the two TCP/UDP NLB Service
  approach from the networking research as the baseline; mixed-protocol
  consolidation is not assumed proven.
- **Q243 — Exact DNS names instead of wildcard setup:** For single-process v1,
  use two exact configurable hostnames: one for portal/Google callback/discovery
  and one for WSS, routed through the existing F5 NGINX ingress. Clients still
  configure only the discovery endpoint and learn the WSS address from it.
  No wildcard DNS/certificate is required. Support an existing TLS Secret or
  optional cert-manager Certificate referencing an existing HTTP-01-capable
  issuer for these names. DNS remains operator-owned. This replaces the
  future-pool research's wildcard requirement; raw TCP and WebRTC Direct use
  their discovered NLB IP addresses, not extra client configuration.
- **Q244 — Existing Secrets and least-privilege mounts:** Values contain
  Secret names/key references, never secret values. Mount runtime credentials
  as read-only files; mount the separate PostgreSQL migration credential only
  in migration Jobs, never the serving Pod. No generated persistent peppers,
  Helm secret lookups, plaintext example credentials, or dependency on a
  particular external-secret controller. Keep image pull and ingress TLS
  Secrets in their respective Kubernetes roles rather than copying them into
  application configuration. Exact key layout and live reload/restart behavior
  follow this ownership choice; it does not yet decide reload semantics.
- **Q245 — PostgreSQL connection security:** Require TLS with certificate
  chain and server-name verification in the production profile, with an
  operator-provided CA bundle when needed. Use separate least-privilege serving
  and migration database roles as already agreed; missing/invalid TLS material
  fails closed. Do not silently fall back to plaintext or skip verification.
  Any local-development relaxation must be explicit and excluded from the
  production example.
- **Q246 — Private operational surface:** Serve `/livez`, `/readyz`, and
  `/metrics` on a separate operational listener, omitted from public Ingress
  and NLB listener ports. Allow the required kubelet/provider health checks and
  optional internal metrics scraping; publish no public metrics/debug route.
  Do not install Prometheus or require ServiceMonitor/PodMonitor CRDs. Exact
  networking restrictions must account for OCI health checks and the cluster's
  NetworkPolicy enforcement, not merely rely on absence of an Ingress rule.
- **Q247 — Hardened stateless Pod:** Run non-root with a read-only root
  filesystem, no privilege escalation, dropped Linux capabilities and the
  runtime-default seccomp profile. No host networking, host ports, privileged
  containers or host mounts. Use bounded ephemeral writable storage only where
  needed, no relay PVC. Kubernetes API access is limited to the Service watcher
  and the separately scoped migration verification needs; no cluster-wide or
  Secret-reading RBAC. Resource values and ingress/egress rules are the next
  design layer, not unspecified permissions hidden by these defaults.
- **Q248 — Reproducible image and architecture:** Publish a Go-only runtime
  image containing the embedded portal and explicit migration command, with
  `linux/arm64` and `linux/amd64` variants. Pin production image references by
  immutable digest; the example schedules ARM64, while scheduling remains
  configurable. The migration Job and intended serving release use the same
  image digest. Do not use `latest`, build at Pod startup, or require Node.js
  in the deployed image. Registry provisioning remains outside the chart.
- **Q249 — Explicit offline migration phases:** Default the example Argo CD
  Application to manual full syncs. For schema changes use explicit Git-managed
  `stopped` → `migrating` → `serving` phases: stop/drain the old Pod while
  preserving network resources; migrate with replicas still zero; start the
  matching release only after successful migration. A migration must verify
  old Pod absence, not just Argo health at zero replicas. It must not scale
  the workload behind Git's back. On failure remain stopped for operator
  diagnosis; never automatically restart an incompatible image or run a down
  migration. Ordinary schema-compatible upgrades keep the accepted ordered
  replacement. A node with uncertain process termination requires isolation/
  fencing before replacement; forced deletion is not proof of termination.
  Detailed phase rendering, Job lifecycle/retry, pruning, and initial-install
  behavior follow acceptance of this operator workflow.

### Facts and remaining design tree

The factual sequencing audit found that Argo PreSync executes before applying
the desired manifests, so a PreSync migration does not itself stop an existing
relay. Argo's StatefulSet health assessment is not a process-termination fence;
zero desired replicas plus Healthy must not stand in for old Pod absence.
Sources: [Argo resource hooks](https://argo-cd.readthedocs.io/en/stable/user-guide/resource_hooks/),
[sync phases and waves](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-waves/),
and [Argo CD v3.5.0 StatefulSet health implementation](https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.0/gitops-engine/pkg/health/health_statefulset.go).
After forced deletion, API-level Pod absence is insufficient: the old process
may still run on an unreachable node. See
[force deleting StatefulSet Pods](https://kubernetes.io/docs/tasks/run-application/force-delete-stateful-set-pod/).
No cluster changes or deployment verification were performed.

Other primary references for this round:
[F5 Ingress annotations](https://docs.nginx.com/nginx-ingress-controller/configuration/ingress-resources/advanced-configuration-with-annotations/),
[cert-manager Ingress integration](https://cert-manager.io/docs/usage/ingress/),
[OCI NLB Services](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingnetworkloadbalancers.htm),
and [Kubernetes Secrets](https://kubernetes.io/docs/concepts/configuration/secret/).

Next layers after these choices: exact values/Secret keys and reload behavior;
public routes, trusted proxy and access-log privacy; NLB annotations, backend
health/drain behavior and NSG/NetworkPolicy prerequisites; resource budget and
probe/drain configuration; phase/Job/prune/rollback contracts and failure gates;
render checks and the example Application. Numeric Safety Limits remain with
their existing dependent ticket. Ask ready independent decisions together;
do not silently settle any remaining layer or mark this ticket resolved.

### Second round — Q250–Q256 accepted

The user first accepted Q250–Q254 and Q256 and asked about best practice for
internal databases, writing "q225". This was interpreted as Q255 (bundled
database TLS), matching the question's subject. After the explanation below,
the user explicitly agreed to Q255 as well. The earlier Q225 Retained Quota
Usage terminology decision is unchanged.

The user's Q242 revision expands chart ownership to optional PostgreSQL, not
the relay's process topology. Q243–Q249 remain accepted for both deployment
modes. The no-PVC rule applies to the relay Pod, not to PostgreSQL data.

- **Q250 — Explicit database mode:** Require `database.mode` to select
  `external` or `bundled`; do not silently infer or fall back between them.
  Provide complete example values for each mode. Both modes use the same
  application schema, authorization, quota, verified TLS, and separate serving/
  migration roles. Reject conflicting mode-specific connection settings.
- **Q251 — Simple bundled database:** Implement the optional database as one
  chart-owned PostgreSQL StatefulSet with persistent storage and an internal
  Service, using a pinned official PostgreSQL image. Do not require an operator,
  replication topology, automatic failover, or a third-party database subchart
  in v1. Accept database downtime during its restart; use external PostgreSQL
  when the operator needs separately managed HA. This is a distinct database
  Pod, never a sidecar inside the single-process relay. Exact supported major,
  digest, data-directory layout, and resource values require the next design
  layer and verification rather than being inferred from this proposal.
- **Q252 — Persistent data ownership:** Support either a new database PVC
  using an explicitly configured StorageClass/size, with an OCI example for
  Longhorn, or an explicitly supplied existing claim. Retain data through
  normal chart removal, bundled-mode disablement and mode changes; do not
  automatically delete a PVC or reinitialize an existing database. Document
  separate explicit destructive cleanup. Protect both Argo pruning and
  application deletion where relevant, not just StatefulSet scale-down.
  This is not protection against a direct PVC/namespace deletion or a backup
  guarantee. Verify permissions and reattachment with the selected CSI driver.
- **Q253 — Mode changes do not move data:** Switching external/bundled is an
  explicit maintenance operation with a validated data-transfer/restore and
  cutover procedure, not an ordinary values toggle that starts with an empty
  account/quota database. Preserve the source and verify the destination
  before resuming relay service. Never merge databases or silently import
  accounts. Detailed backup/restore and deletion-reconciliation policy remains
  in [Define PostgreSQL backup, restore, and deletion recovery](15-define-postgresql-backup-restore-and-deletion-recovery.md).
- **Q254 — Independent database lifecycle:** The bundled database remains
  running during the relay's accepted `stopped` and `migrating` phases; those
  phases stop only relay serving. Pin the database image independently of the
  relay image. Do not restart or change its major version just because the
  relay release changes. Database maintenance/upgrades are explicit operations;
  a PostgreSQL major-image change is never treated as an application SQL
  migration or an automatic upgrade of an existing data directory.
- **Q255 — Bundled database TLS:** Consume a server TLS Secret and trusted CA
  valid for the internal database Service DNS name, or optionally request that
  certificate from an existing suitable private cert-manager issuer. Do not
  reuse the public portal certificate or disable verification for in-cluster
  traffic. Configure PostgreSQL's network authentication to require TLS as
  well as enabling it. Certificate authority provisioning stays operator-owned;
  rotation/reload mechanics follow the Secret contract.
- **Q256 — Explicit privileged bootstrap:** Use a dedicated, one-shot bundled
  database bootstrap Job to create/check the relay database and its separate
  migration and serving roles. Supply an existing database-administrator
  credential Secret only to database initialization/bootstrap contexts, never
  to the relay or ordinary schema migration command. This narrowly extends
  Q244: the bootstrap Job may also read the role credential Secrets needed to
  provision those roles. Application serving is never a PostgreSQL superuser.
  Bootstrap must be resumable without overwriting existing data or silently
  resetting passwords; conflicting existing state fails for operator review.
  Changing a Secret alone is not a database password rotation. Define a
  deliberate authenticated rotation procedure in the subsequent Secret
  lifecycle discussion, not an automatic destructive initialization fallback.

Primary-source factual audit for these proposals:

- The [official PostgreSQL image documentation](https://raw.githubusercontent.com/docker-library/docs/master/postgres/README.md)
  documents initialization only on an empty data directory, superuser semantics
  for `POSTGRES_USER`, non-root requirements, and major-version-dependent volume
  paths. Existing data is not reconfigured by changing initialization variables.
- [PostgreSQL server TLS](https://www.postgresql.org/docs/current/ssl-tcp.html)
  and [authentication rules](https://www.postgresql.org/docs/current/auth-pg-hba-conf.html)
  distinguish TLS enablement from actually requiring encrypted connections.
- [StatefulSet PVC retention](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#persistentvolumeclaim-retention),
  [PV reclaim policy](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#reclaiming)
  and [Argo sync options](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/)
  cover different deletion paths; none is a backup policy.
- [PostgreSQL upgrades](https://www.postgresql.org/docs/current/upgrading.html)
  require a major-version upgrade procedure distinct from application migrations.

The mode and lifecycle choices above are accepted, including Q255. Detailed
bootstrap rendering, PVC protections, server TLS mounts, image/major selection,
and credential rotation remain to be specified. Acceptance of the security
and certificate-ownership policy does not silently settle those mechanics.

### Q255 clarification — internal database practice (accepted)

Use private database exposure plus server-authenticated TLS with full
certificate-chain/hostname verification and SCRAM password authentication for
separate least-privilege roles. Encryption-only `require` is not equivalent to
`verify-full`. PostgreSQL network authentication must reject plaintext; enabling
TLS alone is insufficient. Do not require client certificates/mTLS in v1
merely because the database is internal.

The database certificate covers the exact internal Service DNS name used by
clients, including the configured cluster domain; no public database domain or
public CA is required. Prefer an existing operator-managed private issuer with
cert-manager renewing the leaf certificate, retaining the existing TLS Secret
option. Automatic leaf renewal is not automatic CA lifecycle management:
trust distribution, CA expiry/rotation and PostgreSQL certificate reload still
need explicit ownership. Do not silently expand the relay chart into a CA
provisioner. The user confirmed this policy with "ok, agree". The deployment
ticket remains claimed pending its remaining design layers and final review.

Sources: [PostgreSQL verification modes](https://www.postgresql.org/docs/current/libpq-ssl.html),
[PostgreSQL authentication rules](https://www.postgresql.org/docs/current/auth-pg-hba-conf.html),
[cert-manager Certificates](https://cert-manager.io/docs/usage/certificate/),
and [CA issuer lifecycle cautions](https://cert-manager.io/docs/configuration/ca/).

### Third round — Q257–Q264 (accepted)

The user accepted Q257–Q264 together with "Yes". These configuration and
credential-lifecycle policies are settled alongside Q242–Q256, including the
database-mode revision and Q255 clarification. This is decision acceptance,
not implementation or authorization to change cluster resources or credentials.
Continue this claimed deployment ticket rather than taking a blocked child.

- **Q257 — One validated configuration contract:** Render one versioned,
  non-secret application configuration file from Helm values, with explicit
  Secret-file references. Reject unknown fields, missing required inputs and
  conflicting production settings during rendering where possible and again
  at startup. Avoid overlapping env/flag/file configuration precedence; CLI
  selects the operation and configuration location, not an alternative policy
  source. Publish complete external/bundled examples and a values schema.
  Keep operator settings separate from administrator-managed account plans
  and overrides in PostgreSQL; redeployment never resets those account values.
- **Q258 — Separate credential groups:** Define independently referenced
  Secret groups for Google credentials, administrator allowlist, pepper
  keyring, serving database credentials, migration credentials, privileged
  bootstrap credentials and TLS/trust material. Mount only required keys into
  each workload, using regular Secret/projected volumes rather than subPath
  for live-updated files. Support configurable Secret names/key mappings;
  one workload must not receive another role's secrets merely for convenience.
  Concrete documented keys/files follow this grouping; no Kubernetes
  Secret-reading API permission or chart-generated passwords is introduced.
- **Q259 — Live administrator allowlist:** Reload the allowlist from the
  mounted file without restarting or dropping relay sessions; every admin
  request checks the current successfully observed list. An unreadable or
  malformed observed configuration disables administrator operations until
  corrected instead of retaining old administrator permissions. An empty list
  grants no administrator access. Kubernetes projection is eventually
  consistent, so do not promise immediate revocation measured from the Secret
  edit: provide an observable non-sensitive applied revision. An urgent removal
  requires verified application of that revision or stopping relay serving.
  This controls administrator authority, not the account's relay quota/state.
- **Q260 — Controlled restart for other application settings:** Treat listener,
  public URL/client-registration, Google credential, database connection/trust,
  pepper-keyring and ordinary operational configuration as startup settings.
  Apply changes by the accepted non-overlapping relay restart, not ad hoc live
  mutation. Use versioned, non-reused Secret references for static material and
  a rendered non-secret configuration checksum to make the desired rollout
  explicit. Verify the intended configuration/Secret version before serving;
  editing a mounted static file is not a successful configuration update.
  Keep current/retiring peppers across rotation and enforce Q241's retained-
  usage protection. This does not alter the live allowlist or server-certificate
  renewal policies; account plan edits remain live database operations.
- **Q261 — Deliberate database password rotation:** Use a planned relay outage:
  stop serving and maintenance clients, authenticate with the privileged
  maintenance credential, change the targeted database role password, verify
  the replacement with a new TLS connection, publish/select the replacement
  credential Secret version, and restart the relevant clients. Do not add
  duplicate rotating database roles or a hidden two-password overlap mechanism
  for v1. On partial failure remain stopped and reconcile actual database/Secret
  state; never reset database data or blindly revert credentials. Compromise
  response also terminates old authenticated connections rather than assuming
  a password change revokes them. External database administrators may execute
  the equivalent procedure outside this chart; bootstrap is not a password-
  reconciliation loop.
- **Q262 — Certificate renewal versus CA rotation:** Automate application of
  renewed bundled PostgreSQL server certificates under the same CA: observe a
  complete certificate/key update, trigger PostgreSQL configuration reload,
  and verify the certificate served on a fresh connection. Do not restart the
  database for ordinary leaf renewal. Expose reload failures/expiry risk without
  logging key material; a failed reload must not be reported as successful
  renewal merely because the Secret changed. Private CA replacement remains
  planned maintenance with staged old/new trust, client restart as needed,
  certificate verification, and removal of old trust only after all users have
  moved. The application chart does not generate or rotate the private CA.
  The exact bounded reload mechanism must fit Q247's hardened Pod contract.
- **Q263 — Configured public origin and trusted proxy:** Build OAuth callbacks,
  absolute portal links and origin checks from the configured canonical HTTPS
  origin, never arbitrary Host/Forwarded headers. Accept proxy metadata only
  from explicitly trusted ingress paths; untrusted forwarded headers cannot
  change security decisions or public URLs. Keep the WSS hostname's public
  surface separate from portal/account routes. Default deployments have no
  extra reverse-proxy chain; additional proxies require explicit trust settings.
  This is not source-IP quota enforcement or a change to Clipp's exact
  registered callback matching.
- **Q264 — Privacy applies to infrastructure logs too:** Require ingress and
  database logging configuration to avoid raw OAuth query values, tokens,
  cookies, request bodies, SQL parameter values and account/device identifiers.
  Disable ordinary access logging for relay routes unless an approved redacted
  format is available; retain safe aggregate metrics and redacted operational
  outcomes. The chart must not silently enable controller-wide snippets or
  alter other applications' logging to achieve this. Where the existing ingress
  cannot meet the requirement through release-scoped configuration, document
  the specific operator prerequisite and treat it as a deployment gate.
  Database/server/provider diagnostic logging must be checked too, rather than
  claiming application redaction alone proves no credential leakage.

  The absolute infrastructure-log restriction above is superseded in part by
  accepted Q275 below. Application logs/audit remain strict; restricted
  operator infrastructure diagnostics have the explicit seven-day exception.

Factual basis for this round:

- [Kubernetes Secret projections](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod)
  update eventually and do not automatically update subPath mounts.
- [PostgreSQL TLS file reload](https://www.postgresql.org/docs/current/ssl-tcp.html#SSL-SERVER-FILES)
  requires startup/configuration reload; invalid reloads retain the previous
  TLS configuration and log an error rather than installing the bad files.
- [Database role password storage](https://www.postgresql.org/docs/current/catalog-pg-authid.html)
  and [ALTER ROLE](https://www.postgresql.org/docs/current/sql-alterrole.html)
  do not provide two simultaneous passwords for one role. Authentication is
  performed during [connection startup](https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-START-UP);
  the need to separately terminate established connections is an inference
  from that lifecycle, not a promise that password changes revoke connections.
- The installed controller is F5 NGINX, not community ingress-nginx. Check its
  [logging controls](https://docs.nginx.com/nginx-ingress-controller/logging-and-monitoring/logging/)
  and release-scoped support before choosing exact annotations; do not copy a
  similarly named controller's access-log annotation and assume it works.

Remaining deployment layers include the concrete configuration/Secret key
schema and Job/reload realization of accepted policies; OCI exposure, provider
health/drain behavior, proxy/log configuration and network-policy enforcement;
database image/major and PVC settings; resource/probe budgets; exact phase,
prune, bootstrap/migration retry and first-install behavior; and example
Application/render validation. No final Answer or additional ticket claim.

### Fourth round — Q265–Q273 (accepted)

The user accepted Q265–Q273 together with "agree". These infrastructure,
resource-budget, storage, version-support and maintenance policies are settled
alongside the preceding batches. Continue the same claimed deployment ticket;
acceptance does not authorize cluster changes or claim verified performance.

- **Q265 — OCI direct-Pod networking profile:** Use the two already-agreed
  TCP/UDP OCI NLB Services with direct Pod backends for the documented VCN-native
  cluster profile. Disable NodePort allocation, use the provider's direct-Pod
  JSON HTTP health-check configuration targeting the private `/readyz` port,
  and explicitly disable instant failover. Supply required subnet/NSG inputs
  and use NSG-based rules rather than having the chart provision infrastructure.
  Do not silently fall back to NodePort, host networking, or the regular OCI
  Load Balancer. Validate actual cluster/controller compatibility before release;
  the earlier inspection is a dated snapshot, not continuing proof.
- **Q266 — Best-effort network drain:** Keep SIGTERM-driven application drain
  and immediate unready/new-admission rejection. Preserve Services and ingress
  routing resources across stopped/migrating phases, but do not promise that
  admitted TCP/UDP/WSS connections survive provider backend removal for the
  entire application drain deadline. Early network interruption is an accepted
  possibility; clients reconnect/rediscover under their existing contracts.
  Do not keep readiness falsely healthy, add a second relay, or assume NLB
  fail-open/failover is a session-transfer mechanism. Test real TCP/UDP/WSS
  behavior and keep admission rejection effective even if stale routing sends
  traffic to a draining process. Exact deadline/margin values stay with the
  operational Safety Limit defaults ticket.
- **Q267 — Enforced ingress isolation prerequisite:** Render default-deny
  ingress policies with explicit allowances: public traffic only to relay data
  ports; portal/WSS backends only through the ingress path; private operations
  only for verified kubelet/provider health checks and approved internal
  scraping; bundled PostgreSQL only from serving and authorized maintenance
  workloads. Require an actual enforcement engine and verify NAT/host-network
  source behavior rather than assuming selectors work. The chart does not
  install Calico or another cluster-wide network controller. Missing enforcement
  blocks production acceptance. This decides ingress isolation only; egress
  rules require the transport outbound-dial audit and are not silently treated
  as restricted by this decision. Policies do not replace session revocation.
- **Q268 — Provisional compute envelope:** Start the configurable OCI example
  with relay requests of 1 CPU/1 GiB and limits of 2 CPUs/2 GiB; bundled
  PostgreSQL requests of 500m CPU/512 MiB and limits of 1 CPU/1 GiB; and each
  bootstrap/migration Job requests of 100m CPU/128 MiB and limits of 500m
  CPU/256 MiB. These are initial engineering budgets, not benchmarks or a
  guarantee of 5,000 sessions. Check cluster allocatable capacity and contention
  before deployment. Tune PostgreSQL connection/memory settings and libp2p
  resource bounds to these envelopes in the dependent limits ticket. Job
  limits constrain the client process, not work executed inside PostgreSQL.
- **Q269 — Initial database storage:** Start the example with a configurable
  10 GiB database claim. Require a suitable operator-managed Longhorn
  StorageClass with supported expansion and Retain reclaim behavior for the
  production profile, retaining the explicit-existing-claim option. Do not
  modify the cluster's shared default StorageClass or automatically shrink,
  replace, or delete the claim. Account for Longhorn replicas/snapshots beyond
  the logical 10 GiB and monitor free space. Expansion and manual recovery do
  not change the separately pending backup/recovery contract.
- **Q270 — PostgreSQL version baseline:** Use a tested Debian-based official
  PostgreSQL 18.x image for bundled mode, pinned by digest at release time,
  with its explicit PostgreSQL-18 volume/PGDATA layout. Initially test/support
  external PostgreSQL 17 and 18; reject unsupported majors with an actionable
  compatibility error rather than claiming every PostgreSQL version works.
  Minor image updates require intentional version changes and verification;
  neither floating tags nor a chart update silently performs a major upgrade.
  The relay application schema revision remains separate from the PostgreSQL
  server major version.
- **Q271 — Explicit, inspectable maintenance Jobs:** Render ordinary named
  Jobs only in the appropriate maintenance phase, with an explicit operator-
  selected run identifier. In bundled first-install mode, bootstrap must
  complete before application migrations; external mode checks the supplied
  database/roles without using bundled-admin credentials. Give each Job a
  bounded deadline and no automatic Kubernetes retry after failure. An operator
  diagnoses/reconciles before selecting a fresh run identifier. Preserve failed
  Job status and redacted logs for inspection. Avoid a generic PreSync hook,
  hidden StatefulSet scaling, and rerunning bootstrap on every relay restart.
  Migration success and serving startup both verify the intended database and
  schema; a previously successful Job against another target is not proof.
  Concrete run-name rendering/pruning and deadline values follow the final
  contract and operational-limits handoff.
- **Q272 — Pruning, uninstall, and rollback safety:** Use full manual syncs
  with pruning last; require explicit confirmation before deleting/pruning
  public network-slot resources or database workloads. Retain database PVCs
  under the already-agreed data-ownership rules; do not place credentials or
  external infrastructure under chart deletion ownership. For removal, first
  stop/drain and verify termination, then remove routing/workload resources
  deliberately. A failed release never automatically reverts the database or
  restarts an incompatible image. Resource-selective sync is not a supported
  maintenance workflow. No Force/Replace sync options in the production example.
- **Q273 — Singleton scheduling and disruption:** Do not enable autoscaling
  or render a PodDisruptionBudget that promises unavailable singleton
  redundancy. Omit blocking PDBs by default so operator node maintenance can
  evict the relay/database with accepted downtime. Prefer placing the relay
  and bundled database on different eligible nodes, but do not require it if
  that would leave either unschedulable. Keep node selectors/tolerations
  configurable and the ARM64 example explicit. Never automatically force-
  delete an uncertain StatefulSet Pod or force-attach its data volume to
  recover availability; node fencing and storage recovery remain operator acts.

Facts checked without cluster access:

- [OKE direct-Pod load-balancer configuration](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm)
  requires VCN-native networking, a supported Kubernetes version, disabled
  NodePort allocation and the dedicated health-check annotation. The
  [NLB provisioning guidance](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingnetworkloadbalancers.htm)
  documents instant failover; fail-open is a distinct facility and its chart-
  controlled setting/default has not been established by this audit.
- [OKE network-policy enforcement](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengsettingupcalico.htm)
  requires a policy engine in addition to VCN-native networking.
  [Kubernetes NetworkPolicies](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
  distinguish ingress/egress isolation, implicitly allow replies, and have
  source-NAT/host-network caveats. Standard policies do not provide FQDN peers;
  permitting all outbound TCP/443 must not be described as Google-only access.
- [PostgreSQL version policy](https://www.postgresql.org/support/versioning/)
  lists supported 17/18 majors; the
  [official image manifest](https://raw.githubusercontent.com/docker-library/official-images/master/library/postgres)
  includes ARM64/AMD64 for the Debian-based 18 image. Choose the tested minor
  and digest at implementation/release, not from an assumed floating tag.
- [PVC expansion](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#expanding-persistent-volumes-claims)
  needs supporting storage configuration and does not support shrinking below
  current capacity. [Longhorn StorageClass configuration](https://longhorn.io/docs/1.12.1/references/storage-class-parameters/)
  and [space consumption](https://longhorn.io/kb/space-consumption-guideline/)
  require separate consideration of reclaim behavior, replicas and snapshots.
- [PostgreSQL resource settings](https://www.postgresql.org/docs/current/runtime-config-resource.html)
  can multiply memory use across operations/connections; the proposed resource
  budgets are not evidence that default database settings fit safely.

After this round: consolidate concrete values/Secret schema, port and route
mapping, PVC/Job rendering, first-install and phase transitions, and remaining
egress/log/certificate-reload integration gaps. Preserve dedicated follow-up
tickets for numerical Safety Limits, backup/recovery and release evidence.
Do not mark this ticket resolved before the final shared-understanding review.

### Fifth round — Q274–Q279 (accepted)

The user explicitly accepted Q275's relaxation of Q264 and agreed with the
rest of Q274–Q279. These policies are settled alongside Q242–Q273. The
infrastructure diagnostic exception does not weaken application log/audit
rules or authorize routine credential logging.

- **Q274 — Constrained outbound networking:** Operate this public relay as an
  inbound libp2p server: reject new outbound peer dials, keep relay STOP streams
  on existing authenticated connections, and disable unused AutoNAT probing/
  dial-back, automatic relay discovery, UPnP/NAT mapping and relay-side hole-
  punching services. This does not disable client-side direct connectivity or
  change Circuit Relay v2. Use default-deny egress with explicit DNS, database,
  Kubernetes API and public HTTPS allowances; permitted inbound flows retain
  their response traffic. Public HTTPS permission is not a Google-only network
  allowlist: application HTTP clients must separately restrict intended HTTPS
  endpoints and redirect behavior. Prove TCP/WSS/WebRTC Direct operation under
  the actual network policy/NAT before release; do not silently broaden egress
  if a transport or dependency unexpectedly initiates traffic. Database and
  maintenance workloads receive only their documented outbound allowances.
- **Q275 — Explicit infrastructure diagnostic exception:** Revise Q264's
  blanket no-sensitive-text promise for all infrastructure logs. Keep strict
  application log/audit rules unchanged, but permit restricted operator-only
  ingress/database diagnostics that may contain client IPs, database identifiers
  and error details, with a maximum seven-day retention including exported
  copies. Disable request-query/body, credential/header and SQL statement/
  parameter logging; suppress credential-carrying route error output at source
  where needed. This is risk reduction, not a claim that arbitrary upstream
  error strings are provably free of sensitive values. Verify with synthetic
  credentials before deployment and treat discovered credential leakage as a
  defect, not an approved routine diagnostic field.

  Keep standard Ingress on the existing F5 controller. Any required fixed,
  resource-scoped operator template/logging configuration remains an explicit
  prerequisite, never a chart-enabled snippet facility or silent shared change.
  This accepted choice avoids requiring an isolated ingress controller and a
  custom PostgreSQL log-allowlisting supervisor solely to make an absolute
  zero-sensitive-infrastructure-log guarantee. The stricter alternative was
  not selected; no isolated controller or custom log supervisor is authorized
  by this decision.
- **Q276 — Least-privilege certificate reload watcher:** Add one bundled-
  database watcher with a dedicated database role limited to connection and
  `EXECUTE` on `pg_reload_conf()`, with no application-table, role-management
  or superuser rights. It watches public leaf-certificate/CA material, requests
  reload and verifies a fresh TLS handshake against the expected certificate.
  It does not need the database server private key, root, shared PID namespace,
  Kubernetes write permission or the bootstrap credential. This explicitly
  adds a reload-role Secret to Q258 and its provisioning to Q256. Pin the
  watcher version independently from ordinary relay upgrades so its presence
  does not undermine Q254's independent database lifecycle. Reload permission
  applies to all reloadable PostgreSQL settings, not TLS alone; the role cannot
  modify those files. Expired-certificate recovery uses an operator-controlled
  local reload path, never disabled TLS verification. Include watcher resource
  needs in the database Pod budget rather than claiming the Q268 totals include
  an unbudgeted extra process.
- **Q277 — Explicit first initialization, never silent recreation:** Initial
  bundled PostgreSQL data-directory creation requires an explicit first-install
  initialization setting usable only while relay serving is stopped. Clear it
  before first serving; later restarts/migrations reject a missing or empty
  expected data directory rather than initializing a replacement database.
  Existing-claim reuse verifies the configured PostgreSQL major/data layout
  without wiping it. Bootstrap creates/checks application roles/database but
  does not interpret lost data as a fresh install. An empty/wrong PVC after
  previous use is a recovery incident requiring operator action and the
  separately defined restore rules.
- **Q278 — Maintenance completion is target-specific:** Tie each bootstrap/
  migration run to the selected immutable image, configuration/database target,
  expected schema and explicit run ID. The migration Job independently checks
  the stopped-phase precondition and old relay Pod absence, failing on denied
  or unavailable verification rather than assuming safety. No cluster write
  permissions are needed for that check. Bootstrap and migration completion
  must match the current intended run; do not accept an old completed Job or
  a cached Argo Healthy state as success for changed inputs. Verify current
  database compatibility again at serving startup. Maintenance failures stay
  in the stopped/migrating desired state until explicit correction.
- **Q279 — Preflight and first-install handoff:** Supply a non-mutating
  preflight checklist/command with the eventual chart: validate required values,
  referenced keys without printing their contents, issuer/certificate readiness,
  database access/version/roles, namespace-scoped API permissions, network-policy
  enforcement, storage class and resource headroom. Clearly distinguish checks
  possible before resource creation from post-provisioning reachability and
  privacy tests. Missing prerequisites yield actionable failures, not automatic
  controller installation, credential generation, DNS changes, or relaxed TLS.
  First install follows the same explicit stopped/bootstrap/migrate/serve
  safety rules, with live external transport and synthetic-login/log tests
  required before real production use. Do not claim Helm rendering alone
  verifies external infrastructure.

Facts supporting this round:

- Local go-libp2p v0.49.0 source sets `network.WithNoDial` before opening the
  relay STOP stream (`p2p/protocol/circuitv2/relay/relay.go:362`). Its WebRTC
  listener configures ICE Lite (`p2p/transport/webrtc/listener.go:208`). These
  support the proposed inbound-only profile but are not an executed CNI/NLB
  compatibility test. The accepted prototype is the `d95c6d0` git asset;
  its settings are a reference, not the production configuration.
- The exact [F5 v5.5.4 annotation parser](https://raw.githubusercontent.com/nginx/kubernetes-ingress/v5.5.4/internal/configs/annotations.go)
  has no general per-Ingress access/error-log switch. The
  [controller ConfigMap controls](https://docs.nginx.com/nginx-ingress-controller/configuration/global-configuration/configmap-resource/)
  affect the shared controller. [Operator-owned custom templates](https://docs.nginx.com/nginx-ingress-controller/configuration/ingress-resources/custom-annotations/)
  can supply fixed scoped directives without enabling arbitrary snippets, but
  [pre-host virtual-server selection](https://nginx.org/en/docs/http/server_names.html#virtual_server_selection)
  means host-scoped suppression alone cannot cover every earlier diagnostic.
  Merely switching to VirtualServer does not supply a typed logging API.
- [PostgreSQL logging settings](https://www.postgresql.org/docs/current/runtime-config-logging.html)
  suppress selected statement/parameter/context fields, but arbitrary main
  error messages can still contain input values; TERSE is not a sanitizer.
  A downstream collector does not prevent prior raw output to stdout/files.
- [PostgreSQL administrative functions](https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-SIGNAL)
  permit specifically granting configuration reload to a non-superuser.
  Reload can retain the previous TLS configuration on error, so fresh served-
  certificate verification remains necessary.

The user explicitly accepted this batch, including Q275, and requested
documentation, ticket update and continuation. The consistency audit found
no further deployment policy choice; the resolution below records that
confirmation and the consolidated contract.

## Answer

Resolved on the user's acceptance of Q242–Q279 and explicit instruction to
document the decisions and continue. The detailed handoff is
[Single-process Helm and Argo CD deployment contract](../deployment-contract.md).

Use a single hardened relay StatefulSet with non-overlapping replacement,
two exact HTTPS/WSS hostnames through existing F5 ingress, separate direct-Pod
TCP/UDP OCI NLB Services, enforced network isolation and private operational
endpoints. Support external PostgreSQL 17/18 or a separate chart-managed
PostgreSQL 18 workload with retained expandable storage, verified private TLS,
separate database roles and a least-privilege certificate reload watcher.

Configuration and static credential versions are explicit; administrator
allowlist changes are observed live, while ordinary application changes use
controlled restarts. Preserve accepted quota and credential semantics. No
missing database is silently recreated. Schema maintenance is Git-managed
stop → bootstrap if necessary → migrate → serve, using inspectable target-
specific Jobs, real shutdown verification, manual full syncs and safe pruning.
Infrastructure diagnostics have Q275's restricted seven-day exception;
application logs/audit remain strict and credential leakage remains a defect.

The consolidated contract makes mechanical constraints explicit: watcher
authentication cannot block the database readiness needed for bootstrap;
initialization must be guarded before the official entrypoint; generated names
are shared by routing and absence checks; Job success cannot be reused for
changed inputs; PVC retention is independent of Job/workload deletion. These
are realizations of accepted choices, not new policies answered for the user.

No production chart, database, cluster change or deployment test is claimed.
Numeric operational settings, recovery/anti-resurrection and release/runbook
evidence remain in their existing dependent tickets. The user's request to
continue authorizes claiming the next unblocked decision, not resolving it
without its own live discussion.
