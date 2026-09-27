# Single-process Helm and Argo CD deployment contract

Status: accepted policy consolidation; planning specification, not a built or
deployed chart. Q242–Q279 were accepted, and the user requested documentation
and continuation to the next frontier.

Owner: [Define the Helm and Argo CD deployment contract](issues/11-define-helm-and-argo-cd-deployment-contract.md).
The question history remains authoritative, including Q242's database-mode
revision and Q275's explicit infrastructure-log exception. This asset supplies
the concrete rendering and operating relationships needed to implement it.

Accepted recovery amendments: Q297–Q307 in
[Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md)
select a pinned image derived from the official PostgreSQL 18 Debian image,
including pgBackRest and its runtime libraries at build time. References below
to the official image mean that upstream base/entrypoint, not an unmodified
image lacking the backup helper. Preserve initialization guards and hardening.
Recovery additionally requires separate backup/journal credential references,
operator-managed encrypted recovery material and an explicit offline recovery
Job. No serving relay gets bulk database-backup read/delete authority. The
[recovery contract](recovery-contract.md) now selects a distinct-non-root-UID
same-Pod backup scheduler with read-only PGDATA and a restricted SQL role over
an explicitly permissioned local Unix socket. That socket is the only database
TLS exception; network database connections retain verified TLS. Preserve
group-readable backup permissions across restart without root repair. Separate
cleanup workloads own expiry authority. Numeric allocations remain with the
limits ticket within the existing envelope, not a new resource allowance.

## Chart and configuration surface

Create `charts/clipp-relay` in this repository during implementation. Provide
`values.schema.json`, external/bundled example values and a pinned-revision
Argo CD Application example. Use one versioned non-secret application file;
reject unknown/missing/conflicting fields at render time where possible and at
startup. CLI arguments select operation/configuration location, not another
policy-precedence layer. No Helm `lookup`, embedded credentials or generated
persistent secrets. Account plans/overrides stay in PostgreSQL, not Helm.

The concrete values groups are:

| Group | Required meaning |
| --- | --- |
| `deployment` | Phase `stopped`, `migrating`, or `serving`; explicit maintenance run ID and desired schema revision when migrating; stable release/resource identity. |
| `image` | Relay image repository/digest and pull-Secret references; no floating `latest`. Serving and schema migration use the same intended release image. |
| `public` | Canonical HTTPS portal/discovery origin and distinct exact WSS hostname; TLS Secret or existing cert-manager issuer reference. |
| `listeners` | Portal HTTP, WebSocket, operations HTTP, raw TCP and WebRTC Direct UDP bind ports; internal ports are unprivileged and non-conflicting. |
| `transports` | Enabled transport set, public ports, optional explicit public-address overrides; disabled transports are omitted from discovery. |
| `database` | Mandatory `external`/`bundled` mode; database name; serving/migration credential references; verified TLS trust/name configuration; external endpoint or bundled settings, never conflicting fallback inputs. |
| `database.bundled` | Independently pinned PostgreSQL image; major-specific data layout; new-claim StorageClass/size or existing claim; explicit initialization permission; bootstrap credential and server certificate references. |
| `database.reloadWatcher` | Independently pinned watcher image, restricted reload-role credential, public certificate/trust references, explicit resource allocation and bounded retry/reload settings. |
| `recovery` | Database backup repository and journal identities/Secret references, helper/cleanup settings, explicit run/source/target references and baseline evidence; consume the accepted recovery contract, never auto-restore on startup. |
| `credentials` | Google, administrator allowlist and versioned current/retiring pepper references, including configurable Secret-key mappings. |
| `clients` | Exact registered public-client identities/callback policies from the accepted Clipp runtime contract; not arbitrary redirects or browser-supplied URLs. |
| `ingress` / `oci` | F5 ingress class and verified proxy source configuration; TCP/UDP direct-Pod NLB profile, subnet/NSG inputs and optional reserved IPs. |
| `networkPolicy` | Required enforcement prerequisites and explicit ingress/egress peer selectors/CIDRs for actual DNS, API, database, ingress and health/scrape paths. |
| `resources` / `scheduling` | Per-container resource budgets; ARM64 example, configurable selectors/tolerations, preferred rather than required relay/database node separation. |
| `operations` | Probe, drain, Job, cleanup and safety settings; numerical defaults delegated to the existing limits ticket, not guessed here. |

Implement one generated-name function for the StatefulSet, its expected Pod,
Services, Service-watch targets and maintenance verifier. `relay-0` is the
logical single Relay Network Slot; a release-prefixed physical Pod name must
be derived consistently everywhere. A release/name change is not a shortcut
around verifying that the old serving instance has stopped.

## Resources and routing

| Surface | Rendering and access |
| --- | --- |
| Relay | One-replica StatefulSet in `serving`, zero in maintenance; governing headless Service where required. No PVC, HPA or overlap. Fresh process key/certhash each start. |
| Portal/discovery | ClusterIP backend and exact-host F5 Ingress with HTTPS termination. Only intended public application routes; private operational routes are not registered on this listener. |
| WSS | Separate backend port/Service and exact-host F5 Ingress; public TLS, internal WebSocket. No portal/account routes on the WSS surface. |
| Raw TCP / WebRTC Direct | Two OCI NLB Services selecting the one intended Pod, direct Pod backends, no NodePorts, NSG-based rules and explicit instant-failover disablement. |
| Operations | Separate private HTTP listener for `/livez`, `/readyz`, `/metrics`; kubelet/provider checks and approved internal scraping only. No public Ingress/NLB frontend listener for this port. |
| Bundled database | Separate one-instance StatefulSet, internal Service and persistent claim. It remains running while the relay is stopped/migrating. No operator, HA failover or database sidecar in the relay Pod. |
| Database reload watcher | Non-root sidecar of the database Pod, no shared PID namespace, private key or privileged maintenance credential. It does not block PostgreSQL initialization/readiness on an as-yet-uncreated role. |
| Database backup scheduler | Separate non-root backup UID in the database Pod, read-only database files, same pinned pgBackRest build, restricted local-socket SQL access and bounded shared lock/scratch paths. Waits for bootstrap without blocking initial PostgreSQL readiness. |
| Recovery cleanup | Scheduled maintenance with separate repository/journal cleanup authority; journal maintenance may defer deletions but does not stop unrelated relay traffic. No automatic expiry authority for ordinary writers. |
| Maintenance | Ordinary run-specific Jobs; bootstrap precedes schema migration. No generic PreSync mutation or hidden workload scaling. |

Public HTTPS/WSS use exact hostnames, not wildcard requirements. The operator
owns DNS and issuers. Optional Certificate resources consume existing issuers;
database certificates require private trust and the exact internal connection
hostname/cluster domain, not the public portal certificate.

Preserve public network-slot resources across maintenance phases. Readiness
requires local listeners and a complete public-address snapshot, not database
availability or spare quota. Service status watching remains namespace-scoped,
read-only and bounded by the accepted staleness rules. Private `/readyz` also
backs the direct-Pod NLB JSON health-check configuration. Verify provisioning
order does not create a publication/backend-readiness cycle; public reachability
is tested externally, never inferred solely from Pod Ready.

SIGTERM starts application drain and makes readiness false. The application
rejects new work even if stale routing still forwards requests. Existing flows
have only a best-effort bounded completion opportunity: backend removal can
interrupt them earlier. Do not keep readiness falsely healthy, assume fail-open
behavior, or redirect a live connection to a new ephemeral relay identity.
Termination grace must exceed the configured drain deadline and shutdown margin.

## Credentials and runtime changes

| Credential group | Suggested default keys; mount scope |
| --- | --- |
| Google | `client-id`, `client-secret`; relay account/HTTP role only. |
| Administrator policy | `allowlist.json` containing an applied revision and authoritative-email entries; relay only. Empty grants nobody; observed malformed/unreadable policy disables admin operations. |
| Pepper keyring | `keyring.json` with current key version and versioned current/retiring material; relay only. Apply the existing record-lifetime and Retained Quota Usage protections. |
| Serving database role | `username`, `password`; relay; bootstrap only when provisioning that role. |
| Migration role | `username`, `password`; migration Job and authorized bootstrap, never relay serving. |
| Bootstrap administrator | `username`, `password`; explicit database initialization/bootstrap and authorized operator maintenance, never relay/migration/watcher. |
| Reload role | `username`, `password`; watcher and bootstrap provisioning only. |
| TLS and trust | Standard server `tls.crt`/`tls.key` plus explicitly referenced CA bundle. Clients/watcher receive public trust/certificate material only; the database alone receives its server private key. |

Key mappings remain configurable; the names above are a concrete example, not
permission to mount every key from a Secret. Use regular projected volumes for
live files, not subPath. The administrator allowlist is live-reloaded and checked
on every admin request, with a non-sensitive applied revision. Kubernetes
projection is eventual: urgent removal needs verified application or shutdown.

Other application configuration is restart-controlled. Use versioned, non-reused
Secret references and a checksum of non-secret rendered configuration. No
secret-valued annotations/checksums or automatic privileged API Secret reads.
Version changes must select the intended material before serving. Pepper rotation
retains required old versions; a missing live retention key cannot reset quota.

All network PostgreSQL clients use full chain/hostname verification and SCRAM
roles. The accepted same-Pod backup socket uses its explicit restricted local
authentication rule instead; it is not a plaintext-network fallback.
Server authentication rules reject plaintext network connections. Password
rotation is planned maintenance: stop clients, authenticate the maintenance
actor, change the targeted role, verify new credentials with a fresh connection,
select the replacement Secret and restart. Partial failure stays stopped for
reconciliation; compromise response also terminates old sessions.

Bootstrap grants the reload role only database connection and execution of
`pg_reload_conf()`, with no inherited table, schema-creation, role-management or
superuser privileges. Enforce explicit grants/default-privilege hygiene; do not
assume the absence of direct grants eliminates privileges inherited via PUBLIC.
The watcher waits in a non-provisioned state until bootstrap creates its role;
its success is a separate pre-serving check, not a circular database-ready gate.

For same-CA leaf renewal, observe complete certificate publication, request
reload, and verify the expected certificate on a fresh TLS handshake. Role
authentication and reload do not prove the new leaf is actually served. Report
failures/expiry risk without private material. If expiry prevents authenticated
reload, an operator uses the local out-of-band reload path; never disable TLS
verification. CA replacement remains staged maintenance with coordinated trust.

## Database persistence and resource envelope

Bundled PostgreSQL uses a tested Debian-based 18.x image/digest, mounting the
volume at `/var/lib/postgresql` with `PGDATA=/var/lib/postgresql/18/docker`.
External 17/18 are the initial verification matrix. Server major and application
schema revision are different compatibility checks. Minor changes are deliberate;
major upgrades require a separate upgrade/recovery procedure.

Use a chart-created 10 GiB claim or a referenced existing claim, never both.
The production example requires operator-managed expandable Longhorn storage
with Retain reclaim behavior. Explicitly protect a chart-created PVC from both
Argo prune and application deletion; existing claims are referenced, not adopted.
Do not rely solely on StatefulSet retention. Direct namespace/PVC deletion still
needs its own operator protections. Reclaim retention, replicas and snapshots
are not backups, and logical claim size is not total physical consumption.

Before invoking the official entrypoint, an executable guard checks expected
data-directory/major markers when initialization is disabled. Empty or missing
expected data fails closed. Explicit first-install initialization is allowed only
with relay serving stopped and must be cleared before first serving. Clearing
it may deliberately restart the database during initial setup. No bootstrap
retry recreates lost production data or overwrites conflicting roles/passwords.

| Workload | CPU request / limit | Memory request / limit |
| --- | --- | --- |
| Relay | 1 / 2 cores | 1 / 2 GiB |
| Bundled database Pod budget | 500m / 1 core | 512 MiB / 1 GiB |
| Each maintenance Job | 100m / 500m | 128 / 256 MiB |

The database budget includes the watcher, backup scheduler and database-side
archiving work: split it explicitly when the numerical limits ticket defines
the settings. Kubernetes
container allocations must sum within the stated envelope, not add a hidden
unbounded watcher. Bound database connections and per-query work accordingly;
Job memory does not cap database-side migration work. These are provisional
engineering budgets and do not prove the unchanged 5,000-session target.

Run non-root, read-only root filesystems, no privilege escalation, dropped
capabilities and runtime-default seccomp; bound necessary temporary storage.
Validate volume ownership/permissions with the actual CSI driver. No root
permission-fixing fallback, host networking/mounts or automatic force attachment.
No default blocking PDB; singleton downtime during maintenance is accepted.

## Network and diagnostic controls

Require an enforcing network-policy engine; VCN-native networking alone is
insufficient. Default-deny ingress/egress, with explicit required allowances.
Verify source-NAT and host-network behavior for ingress and health checks.
Serving needs DNS, configured PostgreSQL/API endpoints and public HTTPS;
maintenance needs only its relevant database/API/DNS paths plus explicitly
required storage HTTPS for backup/recovery/cleanup. Allow replies to
accepted inbound transport flows without granting arbitrary new peer dials.
Disable unused AutoNAT/dial-back, UPnP, automatic relay discovery and relay-side
hole punching; client-side direct connectivity is unaffected. Test actual
WebRTC/TCP/WSS behavior before claiming the restrictive policy is compatible.
Public TCP/443 egress is not a Google-domain firewall: application HTTP clients
must validate intended endpoints and redirects independently.

Q275 supersedes Q264's absolute infrastructure-log ban. Application stdout and
audit remain privacy-limited under the account contract. Infrastructure ingress/
database diagnostics are operator-only, may contain IPs/identifiers/error details,
and expire within seven days including exported copies. Disable request-query/
body, credential-header and SQL statement/parameter logging. Protect credential-
carrying route errors at their source and test with synthetic credentials.
Unexpected credential leakage is a defect, not an approved diagnostic field.

The shared F5 controller requires verified operator-owned scoped logging/template
configuration where ordinary annotations cannot supply it. Do not invent an
annotation or enable arbitrary snippets. Pre-host diagnostics fall within the
accepted restricted infrastructure exception; arbitrary PostgreSQL error text
is not asserted to be sanitized. The chart does not install a dedicated ingress
controller or a custom zero-sensitive-log supervisor. Log ownership/retention
verification is a production prerequisite, not an application-code promise.

## Maintenance state machine and Argo CD

| Desired phase | Relay replicas | Database | Allowed work / exit gate |
| --- | --- | --- | --- |
| `stopped` | 0 | Available if bundled; initialize only with explicit first-install permission | Preserve network resources; await ordinary graceful Pod termination. Unknown node/process termination requires fencing, not forced-delete inference. |
| `migrating` | 0 | Available, with verified TLS | On first install run explicit bootstrap, then migrations. Check actual stopped state and old Pod absence; require current run-specific success against the intended target. |
| `serving` | 1 | Available | Initialization permission must be false; exact schema and supported server version must match; verify role/watcher/configuration prerequisites before admitting work. |

Use normal Jobs, not migration PreSync hooks. Generate each Job name from an
explicit run identifier and bounded target/configuration identity; changed
inputs require a new run, not mutation of an immutable completed Job template.
Run-specific success must match image, database target and schema. Bootstrap
and migrations must not race: use staged rendering or ordered waves plus an
independent prerequisite check. Bootstrap failure never advances migration.
The verifier uses namespaced read-only access to the exact workload/Pod targets;
API denial/unavailability is failure, not evidence of absence.

Jobs have `restartPolicy: Never`, no automatic failed-Pod retry, bounded active
deadlines, and retained failure status/redacted logs pending operator review.
Explicit new runs may supersede old Jobs; prune those last after review. Do not
use automatic TTL cleanup that removes failure evidence before inspection.
Schema migration serialization/checksums remain PostgreSQL-authoritative.

Argo's example uses a pinned Git revision, the chart path, chosen example values,
existing destination namespace/project, manual full sync and `PruneLast=true`.
No automated self-heal, Force or Replace. Confirm pruning/application deletion
for public slot resources/database workloads; never delete retained PVCs or
operator-owned Secrets/issuers/infrastructure. Resource-selective sync is not a
supported maintenance procedure. Schema-compatible relay upgrades use ordinary
non-overlapping replacement; schema changes require the explicit three phases.

On failure, keep the stopped/migrating desired state. Never run automatic down
migrations or revive an incompatible image. Before uninstall or changing a
release name/database mode, verify old instance shutdown and deliberate target
handoff; a new name or empty database is not a reset shortcut.

## Verification and downstream handoff

Preflight is non-mutating and never prints Secret contents. Distinguish static
render validation, checks requiring existing namespace/Secrets/issuers/database,
and tests requiring provisioned Services/certificates/transports. Check actual
policy enforcement, RBAC, storage, TLS, roles and allocatable resource headroom.
Helm rendering alone cannot prove these. Missing prerequisites fail with an
actionable operator task, not automatic infrastructure mutation or relaxed TLS.

Implementation must verify both database modes, all transports, restart/drain,
publication startup ordering, default-deny networking, synthetic OAuth/log
leakage, watcher bootstrap/reload/expiry recovery, wrong/empty PVC rejection,
role isolation, fresh/failed/retried maintenance runs, and pruning/data retention.
No implementation or executed deployment tests are claimed by this document.

- [Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md)
  owns backup frequency/retention, recovery targets, deletion/credential/quota
  anti-resurrection and database-mode cutover recovery.
- [Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md)
  owns final numerical probe/drain/Job/watcher/connection/memory/rate/cleanup
  settings and the concrete per-container split within the accepted budgets.
- [Define acceptance, release, and operating criteria](issues/13-define-acceptance-release-and-operating-criteria.md)
  owns evidence, release gates and the operator runbooks for the now-settled
  deployment phases, degraded dependencies, maintenance and recovery.

Those named downstream decisions are resolved as of 2026-09-26. Their accepted
defaults and release gates are consolidated in the [build specification](spec.md).
This does not claim the chart has been implemented, tuned or production-validated.
