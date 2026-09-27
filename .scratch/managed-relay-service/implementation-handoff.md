# Single-process Clipp relay v1 — implementation handoff

Historical reading index retained after the 2026-09-27 consolidation.
Start with the [consolidated specification](spec.md) for build-facing requirements.

Planning status: complete, 2026-09-26.
Production implementation and release acceptance: not completed by this map.

## Start here

The [Wayfinder map](map.md) records the destination, scope and resolved decision
index. All seventeen child tickets are resolved. This handoff implements the
accepted Q349 documentation decision: it is a reading index, not a second copy
of the specification or permission to build/deploy automatically.

Use accepted answers and explicitly accepted rounds in the linked tickets.
Historical proposals and research candidates are not decisions unless a later
accepted answer adopts them. Later explicit revisions govern superseded text;
report a genuine conflict instead of guessing a new policy. Scratch prototypes
are experimental evidence, not production foundations to copy unreviewed.

## Authoritative decision sources

| Area | Read |
| --- | --- |
| Domain vocabulary | [Relay glossary](../../CONTEXT.md) and [Clipp glossary](/Users/invine/src/js/clipp/CONTEXT.md) |
| Single-process topology and quota boundary | [Choose the single-process v1 topology and quota boundary](issues/06-define-distributed-quota-and-coordination-semantics.md) |
| Wire contracts and Relay Configurations | [Define single-process relay control protocols](issues/05-define-relay-control-protocols.md) |
| Ephemeral identity, discovery and drain | [Define single Relay Instance lifecycle and discovery](issues/07-define-relay-pool-lifecycle-and-assignment.md) |
| Accounts, Google credentials, revocation and retention | [Define account credential, lifecycle, and retention policy](issues/09-define-account-credential-lifecycle-and-retention.md) |
| Portal direction | [Prototype the account and administration portal](issues/08-prototype-account-and-administration-portal.md) |
| Shared-core and runtime changes | [Define Clipp runtime integration](issues/10-define-clipp-runtime-integration.md) |
| Go modules and persistence | [Persistence contract](persistence-contract.md), owned by [Define internal Go modules and PostgreSQL persistence](issues/14-define-go-modules-and-postgresql-persistence.md) |
| Helm, external/bundled PostgreSQL and Argo CD | [Deployment contract](deployment-contract.md), owned by [Define the Helm and Argo CD deployment contract](issues/11-define-helm-and-argo-cd-deployment-contract.md) |
| Backup, restore and deletion reconciliation | [Recovery contract](recovery-contract.md), owned by [Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md) |
| Initial configuration | [Choose initial Quota Plan and Safety Limit defaults](issues/12-choose-initial-quota-and-safety-limit-defaults.md) and [Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md) |
| Release gates and evidence ledger | [Define acceptance, release, and operating criteria](issues/13-define-acceptance-release-and-operating-criteria.md#production-acceptance-checklist) |

## Evidence and qualifications

- [Prototype Relay Authentication and conservative quota enforcement](issues/04-prototype-connection-scoped-relay-authentication.md)
  records the connection-scoped stock-protocol experiment, not acceptance of an
  as-yet-unbuilt production implementation.
- [Prototype transport capacity and memory accounting](issues/17-prototype-transport-capacity-and-memory-accounting.md)
  owns the measured transport caveats and revised Q337: keep current limits and
  the 5,000-session goal, defer capacity tuning until code is ready, and do not
  require initial deployment to operate at that scale.
- The operational-limits ticket explicitly adopts the subordinate Resource
  Manager scope tables through Q338. Other research recommendations must not
  silently become configuration defaults.
- Portal layout/style refinement and later capacity tuning are acknowledged
  follow-ups, not authorization to omit security, correctness or recovery gates.

## Transition to implementation

The next step is a separately requested build using these decisions and the
current instructions in each repository. Relay work belongs in clipp-relay;
shared client/runtime integration belongs in Clipp. Do not treat acceptance of
the planning map as permission to publish artifacts, provision infrastructure,
run faults against production or modify the OCI cluster.

Operator-specific inputs and evidence remain necessary before cutover: the
selected environment and credentials, verified backups/journal, actual network
and storage behavior, monitoring/notification destination and human promotion.
These are the accepted deployment/release prerequisites, not remaining planning
fog. The production acceptance ledger starts at **not run** throughout.
