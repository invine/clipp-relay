# Design the single-process account-governed Clipp relay v1

Label: wayfinder:map
Status: resolved

## Destination

Reach an implementation-ready decision set for a single-process Go Clipp relay v1: one Relay Instance, an in-process Relay Coordinator role, coordinated Electron, Android, and Chrome-extension integration, Google-backed Relay Accounts, conservative weekly quotas, user and administrator portals, and an ARM64-compatible Helm release consumable by Argo CD on the existing OCI Kubernetes cluster.

## Notes

- Planning completed on 2026-09-26 after acceptance of Q349–Q358; all decision tickets are resolved and no in-scope fog remains. On 2026-09-27 the user requested consolidation and confirmed the testing seams. Start subsequent implementation from the [consolidated specification](spec.md), marked ready-for-agent; the [original handoff index](implementation-handoff.md) remains as history. Production acceptance remains unexecuted.
- This map plans the work; it does not implement or deploy the production service.
- Maintain relay terminology in [CONTEXT.md](../../CONTEXT.md) and consume Clipp application terminology from [/Users/invine/src/js/clipp/CONTEXT.md](/Users/invine/src/js/clipp/CONTEXT.md).
- Grilling tickets use the `grilling` and `domain-modeling` skills. Research tickets use the `research` skill and primary sources. Prototype tickets use the `prototype` skill with live human review.
- A Relay Account is never a Device Identity or Device Network. Device Identity association exists only for a live Relay Session; the service has no durable device registry.
- Cross-account circuits are allowed. The v1 Relay Quota and topology baseline is recorded by [Choose the single-process v1 topology and quota boundary](issues/06-define-distributed-quota-and-coordination-semantics.md).
- The Weekly Quota Window resets Monday at 00:00 UTC without rollover. Accounts require administrator approval and receive administrator-managed Quota Plans and optional overrides.
- Google is the sole initial external provider. Only the Relay Coordinator role handles Google flows; Relay Authentication consumes a Clipp Relay credential.
- Clipp may hold multiple Relay Configurations and attempts every configured relay independently. An authenticated configuration uses one HTTPS Relay Discovery Endpoint to obtain the current Relay Instance's Peer ID and public addresses; an unauthenticated configuration uses explicit full relay multiaddrs.
- Unauthenticated Relay Configurations remain for development and separately operated persistent-identity relays.
- Production exposure must support WSS, raw TCP, and WebRTC Direct UDP while carrying Circuit Relay v2 and Clipp's exact Rendezvous behavior.
- The Relay Coordinator and Relay Instance roles run as internal Modules in one Go process and one Kubernetes Pod. PostgreSQL is the initial live application datastore; the deployment contract supports an external database or a separate chart-managed PostgreSQL workload under the user's revised Q242. The accepted recovery boundary additionally permits protected off-cluster backups and a minimal Recovery Journal; its detailed contract remains in [Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md).
- The current cluster has three ARM64 nodes plus Argo CD, NGINX Ingress, cert-manager, Longhorn, and Velero. Prometheus is absent; expose compatible metrics without requiring its installation.
- The scale goal remains 1,000 Relay Accounts and 5,000 concurrent Relay Sessions on one ARM64 Relay Instance. Revised Q337 keeps current limits unchanged and defers capacity tuning until code is ready; initial deployment need not operate at that scale. Known transport-accounting constraints remain explicit, and measured validation is required before claiming the goal is supported. Neither a larger budget nor a reduced target is approved.

## Decisions so far

<!-- Closed-ticket context pointers are appended here. -->

- [Research Go libp2p relay compatibility](issues/01-research-go-libp2p-relay-compatibility.md) — go-libp2p v0.49.0 covers Clipp's wire stack; exact account-aware circuit semantics require adaptation, while the simplified v1 must validate a small HOP authentication gate and conservative endpoint accounting.

- [Research cross-runtime Google authorization](issues/03-research-cross-runtime-google-authorization.md) — Use authorization code with S256 PKCE, opaque Relay credentials, and rotating login grants; the Relay Coordinator role owns Google interaction without creating durable Device Identity bindings.

- [Choose the single-process v1 topology and quota boundary](issues/06-define-distributed-quota-and-coordination-semantics.md) — Run the Relay Coordinator and one Relay Instance in one process; govern Relay Sessions and conservative weekly traffic-credit consumption while stock reservation and circuit controls remain Safety Limits.

- [Prototype Relay Authentication and conservative quota enforcement](issues/04-prototype-connection-scoped-relay-authentication.md) — The accepted live harness validates exact-connection authentication, stock Circuit Relay v2 handoff, exact Rendezvous gating, conservative endpoint accounting, and account-wide session termination across all required transports.

- [Define single-process relay control protocols](issues/05-define-relay-control-protocols.md) — Use independent authenticated or explicit Relay Configurations, strict HTTPS discovery and connection-scoped Relay Authentication, stock Circuit Relay v2 statuses, and versioned exact Rendezvous with fail-closed compatibility rules.

- [Define single Relay Instance lifecycle and discovery](issues/07-define-relay-pool-lifecycle-and-assignment.md) — Use one ephemeral process identity in a stable non-overlapping network slot, publish complete bounded-staleness addresses atomically, enforce session admission at authentication, and drain existing circuits before restart.

- [Define account credential, lifecycle, and retention policy](issues/09-define-account-credential-lifecycle-and-retention.md) — Use Google issuer-and-sub account identity, explicit administrator-governed states, short opaque credentials with rotating Login Grants, account-wide-only revocation, quota-preserving self-deletion, and privacy-limited retention and audit.

- [Prototype the account and administration portal](issues/08-prototype-account-and-administration-portal.md) — Use one light portal language, an Active profile with doughnut quota consumption and tabular account-wide capacity, and a dense but visually aligned administrator workspace; final layout and styling remain deferred.

- [Define Clipp runtime integration](issues/10-define-clipp-runtime-integration.md) — Use one shared-core relay controller with runtime-specific browser authorization and credential storage, explicit initially empty configurations, independent readiness and recovery, and portal-based account management.

- [Choose initial Quota Plan and Safety Limit defaults](issues/12-choose-initial-quota-and-safety-limit-defaults.md) — Adopt the simulator's proposed quota, credit, circuit, and reservation values provisionally; tune with production evidence while keeping unproposed operational limits and compatibility risks explicit.

- [Define internal Go modules and PostgreSQL persistence](issues/14-define-go-modules-and-postgresql-persistence.md) — Use deep Account/Quota/Relay Modules, privacy-limited PostgreSQL records, ordered atomic operations and bounded quota recovery, process-bound unfinished sign-ins, and explicit offline schema migrations.

- [Define the Helm and Argo CD deployment contract](issues/11-define-helm-and-argo-cd-deployment-contract.md) — Support external or chart-managed PostgreSQL, hardened singleton networking, verified TLS and explicit credential lifecycles, with manual stop/migrate/serve releases, retained storage and restricted infrastructure diagnostics.

- [Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md) — Use pgBackRest or equivalent managed PITR, a minimal deletion journal and explicit validated restores; preserve restrictions, reset unrecoverable weekly consumption to zero, and enforce bounded recovery retention with tested operating gates.

- [Prototype transport capacity and memory accounting](issues/17-prototype-transport-capacity-and-memory-accounting.md) — Local transport and pressure experiments expose accounting and buffering constraints; keep current limits and the 5,000-session goal unchanged, start below that scale, and defer capacity tuning until code is ready.

- [Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md) — Adopt explicit provisional admission, resource, provider/database, maintenance, recovery and temporary-storage bounds; preserve existing capacity goals and defer tuning until implementation is ready.

- [Define acceptance, release, and operating criteria](issues/13-define-acceptance-release-and-operating-criteria.md) — Separate planning completion from executed release evidence; require real-client, security, recovery, modest-load and operator checks before human promotion, with full-capacity tuning deferred.

## Not yet specified

- No additional unticketed decision is currently identified.

## Out of scope

- Production implementation or deployment during this planning map.
- Multiple Relay Instances, independent Relay Coordinator deployment, Relay Pool lifecycle, and horizontal data-plane scaling in v1. [Research OCI relay-instance networking](issues/02-research-oci-relay-instance-networking.md) remains a future topology reference.
- Exact payload-only Relayed Traffic accounting, same-account endpoint deduplication, per-account reservation or circuit quotas, distributed quota leases, and targeted circuit termination in v1.
- IP-address throttling in the initial release.
- Identity providers other than Google, cross-provider account linking, billing, and paid subscriptions.
- Durable Device Identity enrollment, ownership, or device administration.
- Content inspection or moderation.
- A separate native/mobile administration application.
- OCI tenancy, cluster, external database, OCIR repository, and Argo CD credential provisioning. Optional chart-managed PostgreSQL is in scope under the revised deployment contract.
