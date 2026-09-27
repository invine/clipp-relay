# Define internal Go modules and PostgreSQL persistence

Type: grilling
Status: resolved
Blocked by: 05, 06, 07, 08, 09

## Question

What internal Go Module interfaces, ownership boundaries, transaction units, PostgreSQL tables, constraints, indexes, cleanup jobs, migration rules, and privacy-enforcing schema checks should implement the settled single-process protocols, lifecycle, quotas, portal workflows, and account credentials without creating a Device Identity registry or leaking credential material?

## Comments

- 2026-09-05: Claimed the next frontier using Wayfinder, grilling,
  domain-modeling, and codebase-design. Main-tree inspection found planning
  documents only: production Go packages, SQL, and template tooling are not yet
  established. The separate in-process Coordinator and Instance roles,
  server-rendered portal, PostgreSQL-only durability, account-row serialization,
  atomic mutation/audit writes, and privacy/retention policies are already
  settled and are not reopened here. Q203's prohibition on migrating legacy
  Clipp relay settings does not prohibit PostgreSQL schema migrations.

### First round — Q215–Q220 (accepted)

User confirmed Q215–Q220 together. The recommendations below are settled;
explicitly deferred follow-ups remain open.

- **Q215 — Module ownership:** Recommend one Go dependency module and one
  application binary, with internal `account`, `quota`, and `relay` Modules.
  `account` owns account/credential/administrative workflows; `quota` owns weekly
  credit accounting; `relay` owns live connections, session admission, Rendezvous,
  and the libp2p integration. HTTP/Google and PostgreSQL Adapters translate at
  their respective Seams. These refine the settled Coordinator and Instance
  roles, not separate deployments. Interfaces expose complete operations such
  as rotating credentials, changing account policy, and allocating credit, not
  one generic CRUD repository per table. Cross-table atomicity stays inside
  the operation's Implementation rather than being assembled by handlers.
- **Q216 — PostgreSQL tooling:** Recommend explicit parameterized SQL using
  `pgx` v5 and its connection pool, with no ORM or SQL code generator initially.
  Keep SQL and transaction mechanics in the concrete PostgreSQL Adapter. Verify
  SQL behavior and concurrency against real PostgreSQL; do not introduce a
  production datastore abstraction for hypothetical alternatives. Exact
  dependency versions remain release inputs, not inferred from prototype pins.
- **Q217 — Portal tooling:** Implement the already-settled server-rendered
  portal with Go `net/http`, `html/template`, embedded CSS/assets, and small
  plain-JavaScript enhancements. No separate frontend application or Node
  runtime/build requirement for the initial portal. Preserve the accepted
  layouts and workflows; visual refinement remains deferred. This chooses
  tooling, not server rendering again.
- **Q218 — Schema evolution:** Recommend ordered, versioned SQL migrations
  invoked explicitly through a migration command, not automatically by the
  serving process. Normal startup checks schema compatibility and refuses an
  unsupported schema. Separate migration credentials from the serving
  credential, which cannot alter the schema. Prefer forward corrective
  migrations rather than automatic down-migrations. Helm/Argo CD invocation,
  compatibility windows, and recovery details remain dependent decisions.
- **Q219 — Account-local credit ownership:** Recommend one shared remaining
  credit balance per Relay Account per Weekly Quota Window, used by all that
  account's Relay Sessions. Fund it before the first session receives protected
  relay permissions; allow only one allocation in progress and no speculative
  second block. Use the accepted 64 KiB block or the smaller remaining weekly
  allowance. Retain unused live-process credit through ordinary disconnects so
  reconnecting does not allocate another block solely because it is a new
  session. Weekly rollover, graceful refunds, uncertain commits, displayed
  usage, and durable reconciliation still need explicit follow-up decisions;
  this is not an asynchronous traffic-overshoot guarantee.
- **Q220 — Live admission/revocation ordering:** Recommend a shared
  account-scoped in-process ordering mechanism for session admission and
  account invalidation, in addition to the already-settled PostgreSQL account
  row lock. An authentication validated before revocation cannot install a
  session afterward and escape the revocation. Commit durable changes and
  audit first, invalidate live permissions, then perform connection closure
  within the accepted ten-second deadline. Do not hold database transactions
  while waiting for Google or network closure. No message broker or distributed
  revocation mechanism in single-process v1. Exact failure handling and
  cross-account same-Peer-ID replacement remain follow-ups.

Tooling references checked for this round:
[pgx documentation](https://pkg.go.dev/github.com/jackc/pgx/v5) and
[Go HTML template documentation](https://pkg.go.dev/html/template).

### Second round — Q221–Q228 (accepted)

2026-09-06: User accepted Q221–Q224 and Q226–Q228 and the rest of Q225,
but objected to the word carryovers. The user subsequently clarified that the
retained usage must remain and only the term is misleading. The existing
deletion/re-registration anti-reset behavior is preserved; no extra account or
device history retention is introduced. The user then approved **Retained
Quota Usage** as the replacement canonical term, completing Q225.

- **Q221 — Committed quota versus measured traffic:** Recommend a deliberately
  conservative, durable `Quota Committed` total: debit a block when allocated,
  keep its unused balance available to the account in the running process, and
  do not refund allocations on disconnect, graceful shutdown, or crash. After
  restart, unused old credit is unavailable and remains committed. The profile
  doughnut should show `Quota committed`, with an explanation that it includes
  unused reserved credit; do not label that total as measured transferred bytes.
  For example, allocating 64 KiB and observing 10 KiB of endpoint traffic shows
  64 KiB committed, while 54 KiB remains usable until consumed or the process
  ends. Keep reporter-observed endpoint bytes distinct in operational metrics;
  no exact durable per-account transferred-byte history is promised. This is an
  accepted refinement of the earlier Charged Relay Traffic/profile wording.
  The glossary and profile/topology references now point to this decision;
  deleted-account quota retention is preserved by the Q225 clarification below.
- **Q222 — Live weekly rollover:** At Monday 00:00 UTC discard all old-window
  local credit; never spend it in the new window. Existing otherwise-valid
  sessions may continue if new-window credit can be confirmed, without a
  mandatory weekly sign-in or connection reset. If PostgreSQL cannot fund the
  new window, close the affected sessions instead of spending old credit.
  Classify reporter observations by the window when processed, not an inferred
  timestamp for when opaque bytes originally crossed the wire. Clock authority
  and exact transition synchronization remain implementation-contract follow-ups.
- **Q223 — Allocation commit ambiguity:** Use a server-generated idempotency
  identifier for each credit-allocation operation. If the database reply is
  lost, recover/retry that same operation without either debiting twice or
  installing the returned credit twice. Unconfirmed credit is never spendable.
  If funding cannot be confirmed and usable local credit is exhausted, close
  affected sessions with a temporary failure rather than granting free credit
  or falsely declaring quota exhaustion. Keep recovery metadata bounded, not
  an ever-growing traffic-allocation history. This is internal quota recovery;
  it does not weaken the separately settled strict refresh-token reuse rule.
- **Q224 — Quota Plan changes:** Recommend immutable allowance values once a
  Quota Plan has been assigned. A changed allowance creates a replacement plan;
  existing accounts stay on their assigned plan until explicitly reassigned.
  An old plan may be archived to prevent new assignments without breaking
  current assignments. Account overrides remain available. This avoids a
  single plan edit silently changing every assigned account. The effects of
  explicit reassignment/override reductions and historical allowance display
  still need decisions.
- **Q225 — Durable record shape and privacy:** Use explicit typed records for
  accounts, plans, account-week quota totals, Retained Quota Usage, authorization
  transactions/codes, Login Grants, refresh generations, Relay Access Tokens,
  Portal Sessions, and audit events. Keep the one External Identity and optional
  quota overrides on the account record, not in a device-like association table.
  No generic JSON metadata bags in this v1 schema. Encode uniqueness, references,
  and value validity as database constraints where possible, backed by schema
  tests for the no-Device-Identity/no-raw-credential policy. Audit and Retained Quota Usage
  records must remain appropriately independent of erased account records.
  Exact columns, indexes, deletion detachment, and constraints follow this
  schema-shape decision; arbitrary SQL text fields alone cannot enforce privacy.
  The original proposal called the retained-usage records Quota Carryovers;
  the accepted name is now Retained Quota Usage, with the same behavior.
- **Q226 — Retention worker:** Run bounded, retryable cleanup inside the one
  application process, with startup/recovery catch-up, rather than introducing
  a separate scheduler or queue. Check expiry at authorization/read time so a
  late deletion job never extends credential validity. Keep every accepted
  retention deadline; expose backlog/deadline failures when a database outage
  prevents deletion, and catch up on recovery instead of silently resetting
  the retention clock. Cadence and batch-size numbers belong to the remaining
  operational Safety Limit defaults.
- **Q227 — Revocation commit ambiguity:** If suspension, deletion, or
  Account-wide Revocation may have committed but confirmation is lost, close
  that account's live sessions conservatively and block fresh admission until
  database state is reconciled. The portal reports an uncertain/temporary
  failure, not confirmed success. A later read establishes committed state;
  previously closed connections are never resurrected. Do not infer that a
  failed database response means the transaction rolled back.
- **Q228 — Late reporter attribution:** The stock reporter supplies Peer ID,
  protocol, and byte count, not a connection ID. Recommend retaining v1's
  report-time authoritative live-session association: a late report from a
  replaced connection may charge the new account for the same Peer ID. If no
  live association remains, record unattributed bytes only in aggregate metrics,
  without reconstructing an account from a retained device history. Account
  switching still requires valid credentials and still closes the old session.
  Accept this attribution limitation explicitly; session-generation fencing
  protects registry cleanup but cannot disambiguate these reporter callbacks.
  Neither its tail size nor the asynchronous cutoff has a proved maximum.

Facts checked for the second round: the accepted probe at
`d95c6d0:prototype/relay-authentication/harness/go/main.go:247–266,389–396`
looks up the current session association inside each Peer-ID-only callback.
Its quota/session-limit setters are demonstration behavior, not decisions about
production quota reductions. The portal prototype changes only plan fixtures,
and the quota simulator resets its whole model for a new week; neither settles
plan propagation or live weekly rollover. These observations introduce no
production changes; the user's subsequent acceptance is recorded above.

### Q225 terminology clarification (resolved)

2026-09-06: User explicitly confirmed that usage must not be erased; the
objection is to the term Quota Carryover, not the existing retention policy.
Deleting and re-registering must therefore not reset current-window committed
quota. Unused allowance still never rolls into a new week. Existing deletion
and history-retention scopes are unchanged.

The user approved **Retained Quota Usage** as the replacement canonical term: the
privacy-minimal record of a deleted Relay Account's Quota Committed for the
current Weekly Quota Window, preserving consumed allowance on re-registration
within that window and expiring when the window ends. This is distinct from
the ordinary account-linked quota history and does not preserve the deleted
account or Device Identities. The glossary and current decision references now
use the accepted name. Historical prototype assets are unchanged.

### Third round — Q229–Q238 (accepted)

User accepted Q229–Q238 together. The policies below are settled. Consolidate
the concrete schema, Module Interface, transaction, cleanup, and migration
contract for final design review; do not treat newly derived details as agreed
or close this ticket before shared-understanding confirmation.

- **Q229 — Applying quota changes:** An explicit plan reassignment or account
  override takes effect for the current week without resetting Quota Committed.
  If the new traffic allowance is below the committed total, invalidate unused
  local credit and close all account Relay Sessions. Equality does not discard
  already-funded credit: it can still be spent, but no further allocation is
  available. If the session allowance is lowered below the current session
  count, close all account sessions and allow reconnection up to the new limit,
  rather than selecting individual survivors. These closures use the existing
  ten-second account-wide closure deadline and do not revoke Login Grants or
  Portal Sessions. Permit nonnegative integer allowances; zero means no
  allowance, never unlimited. A later increase does not refund committed or
  discarded credit. Apply policy changes under the agreed account ordering
  mechanism after the database mutation and audit commit.
- **Q230 — Historical statistics:** Show absolute Quota Committed bytes for
  each historical week, not percentages recalculated against the account's
  current plan. The current week's doughnut may show committed/current allowance,
  including an explicit exceeded or zero-allowance state. Preserve the accepted
  current-plus-twelve-completed-windows retention. No historical allowance
  snapshots or time-weighted plan calculations are needed for this v1 display.
  Durable quota/plan reads should be one consistent database snapshot; live
  session counts are explicitly a current in-memory sample, not a historical
  or transactionally simultaneous traffic measurement.
- **Q231 — Clock authority:** Use PostgreSQL UTC time for durable expiry and
  quota-window decisions, sampling wall time after required locks have been
  acquired. Derive conservative Go monotonic deadlines for local session and
  funded-credit validity so response transit time cannot extend them. An
  observed clock regression must not extend an existing deadline or revive
  expired credit; clock inconsistencies fail closed for affected admission or
  allocation and are observable. Correct infrastructure UTC time is an
  operational prerequisite, not guaranteed by choosing a PostgreSQL function.
  Detailed skew thresholds remain with operational defaults. Do not use
  transaction-start `now()` to classify a mutation that waited across a week
  boundary.
- **Q232 — Immediate deletion with bounded physical purge:** In the deletion
  transaction, invalidate account credentials, store the current-week Quota
  Committed as Retained Quota Usage, append the audit event, and detach the raw
  External Identity and presentation metadata from the old account. That
  releases the identity for immediate registration while the remaining old
  account-owned records are unavailable to normal operations and purged in
  bounded batches within the existing 24-hour deadline. A private deletion
  marker is cleanup state, not a fifth public account state. Serialize deletion
  and registration for the same External Identity; import retained consumption
  into the new account atomically and exactly once. Retained Quota Usage keeps
  only its already-agreed digest, week, and committed amount, never the old or
  new account ID. No previous account audit/history links attach to the new
  account. Account invalidation, local credit cutoff, and physical connection
  closure still follow the accepted live-ordering and ambiguous-commit rules.
- **Q233 — Bounded allocation-recovery record:** Keep the account-week total
  plus one most-recent allocation receipt containing its operation identifier
  and granted amount. Since the account has at most one allocation in progress,
  do not start another until that receipt's outcome has been reconciled. A
  duplicate identifier returns the same outcome; the live allocator applies it
  at most once. Do not keep a row per block for the whole traffic history.
  A fresh process generates new operation identifiers and never reinstalls
  unused credit from an old process. A receipt confirms a debit, not continuing
  account authorization: recheck account validity, week, and local invalidation
  state before installing recovered credit. Schema representation, pruning,
  and cancellation/generation fencing will be in the consolidated contract.
- **Q234 — Credential invalidation generation:** Give each account a
  credential-invalidation generation separate from its administrator-edit
  revision. Account-wide revocation and blocking transitions advance it;
  existing credentials and account-bound authorization flows from older
  generations fail immediately. Cleanup may physically remove them later.
  Outstanding pre-revocation authorization codes/flows must not mint replacement
  credentials after revocation; completing authorization requires a fresh Google
  flow. Ordinary plan/override changes do not advance this generation. This
  is an account credential mechanism, not a durable Relay Session, Login Grant
  association on sessions, Device Identity registry, or process identity.
  Handling flows whose account was not yet known when they began must be
  specified in the concrete authorization transaction contract.
- **Q235 — Session replacement and stale cleanup:** Serialize competing
  authentications for the same Peer ID, use consistently ordered account guards
  when the old and new accounts differ, and perform a short atomic registry
  swap only after authentication, quota, and capacity checks succeed. Keep
  every session/timer/lease ownership generation in memory. Close the captured
  old connection after the swap; a delayed close callback, expiry timer, or
  Rendezvous cleanup may remove only the generation it owns, never its
  replacement. Failed admission preserves the old session. Do not hold the
  global registry mutex during database or network waits. This implements
  already-agreed swap guarantees; it does not change Q228's accepted Peer-ID-only
  reporter attribution limitation.
- **Q236 — SQL concurrency baseline:** Use short Read Committed transactions
  with explicit row locks, rechecking authoritative account policy and relevant
  credentials after locking. Use a consistent lock order for multi-account
  operations and serialize External Identity creation/deletion paths, where an
  account row may not yet exist, with uniqueness constraints as a final guard.
  Do not depend on an unlocked preliminary account lookup for permission. Retry
  only known-aborted retryable transactions with bounded attempts; recover
  ambiguous commits by the relevant operation's established rules, not a blind
  generic retry. Exact lock acquisition order and creation-lock mechanism belong
  in the consolidated transaction contract; no global Serializable default or
  permanent identity-lock table is proposed.
- **Q237 — Migration compatibility:** Each application build declares one
  exact expected schema revision. Serving starts only with that revision and
  never runs concurrently with schema-changing migration. Require drain/stop,
  an explicit migration command, then compatible application startup. Serialize
  migration runners, use ordered checksummed migrations, and commit each
  supported migration and its version marker together. Restrict the initial
  migration set to transaction-compatible SQL; unexpected versions, changed
  checksums, or incomplete migrations fail closed. Rolling back an image after
  a schema change is permitted only if that image supports the resulting
  revision; otherwise use a forward fix or the separately defined recovery
  procedure. Helm/Argo orchestration and backup/restore remain with their
  existing dependent tickets.
- **Q238 — Auditing plan administration:** Plan creation and archival can
  happen without targeting one account. Extend the audit schema's allowed
  targets with an optional internal Quota Plan ID and explicit plan event
  types. Keep fresh administrator authentication, structured reasons, atomic
  mutation/audit commit, restricted reads, and existing retention. Account
  assignment/override events keep their account target and may include the
  relevant plan ID. This explicitly extends the earlier account-only target
  field list; it does not permit free-form metadata, identity claims, credentials,
  or per-device data.

Primary-source facts checked for this round:
[PostgreSQL time functions](https://www.postgresql.org/docs/current/functions-datetime.html#FUNCTIONS-DATETIME-CURRENT)
distinguish transaction-start timestamps from `clock_timestamp()`;
[Read Committed](https://www.postgresql.org/docs/current/transaction-iso.html#XACT-READ-COMMITTED)
and [explicit locking](https://www.postgresql.org/docs/current/explicit-locking.html)
require care with lock waits, missing rows, and lock order. Normal and concurrent
index builds have different transactional restrictions under
[CREATE INDEX](https://www.postgresql.org/docs/current/sql-createindex.html#SQL-CREATEINDEX-CONCURRENTLY).
These are source facts supporting proposals, not a new PostgreSQL version pin
or evidence that the intended concurrency behavior has been implemented/tested.

### Consolidated contract draft

[Single-process persistence and Module contract](../persistence-contract.md)
now collects the proposed operation Interfaces, schema/constraints/indexes,
lock order, transaction/recovery units, cleanup, migrations, and verification
handoffs. It distinguishes accepted Q215–Q241 policies from the concrete design
still awaiting final review. It is not production code or test evidence.

The authorization fact audit found that the final Clipp callback must echo the
outer client `state`, whereas Google returns a different relay-generated state.
A database hash cannot reconstruct the outer value. The existing research
mentions the outer field but does not settle recoverable storage. Similarly,
account generation alone cannot reject pre-revocation flows whose account was
unknown at initiation. These are genuine gaps, not reasons to revisit the
already accepted credential lifecycle.

### Fourth round — Q239–Q241 (accepted)

User accepted Q239–Q241 together. These policies are settled and incorporated
into the consolidated contract; the ticket still awaits final shared-
understanding confirmation of the complete design.

- **Q239 — Process-bound unfinished Google sign-ins:** Keep the recoverable
  outer client state only in a bounded, short-lived in-memory continuation,
  alongside the authoritative database authorization transaction containing
  digests. Both are required to complete login; restarting the relay cancels
  unfinished sign-ins, not existing Portal Sessions or Login Grants. Bind each
  attempt to the initiating browser using a separate opaque Secure, HttpOnly,
  SameSite=Lax, host-only cookie and its stored digest. Claim an attempt once
  before exchanging the Google code; failure/ambiguity may require starting
  again. Do not place redirect URLs or identity claims in cookies.

  To enforce Q234 before the account is known, keep temporary per-External-
  Identity revocation fences in that same process. A flow records its start
  sequence; revocation/deletion fences the identity with a later sequence, and
  completion checks the fence after Google identifies the user and under the
  identity/account ordering guards. Fence keys are keyed digests using a
  process-random key, with no account ID or Device Identity. Retain each fence
  until every older flow has expired (at most the ten-minute flow lifetime).
  This also prevents an old flow signing back in after deletion/re-registration.
  Restart loses both flows and fences, so cannot reopen an old flow. Do not
  evict safety fences while affected flows remain valid; refuse new work if
  bounded capacity cannot preserve the invariant. Numeric bounds belong to the
  operational-limits ticket. This explicitly permits temporary recoverable
  client state in RAM, not plaintext state in PostgreSQL or a durable deleted-
  identity registry. Apply conservative fencing on uncertain revocation commits.
- **Q240 — Reconstructable per-session CSRF token:** Derive a purpose-separated
  CSRF token from the presented opaque Portal Session credential and its
  configured server pepper; store only its digest with the session. The server
  can regenerate it for HTML forms after restart without storing plaintext or
  rotating the token whenever another tab opens. Compare submitted tokens to
  the stored digest and require the already-agreed Origin check. No CSRF token
  in a URL or a second JavaScript-readable cookie. Replacing the Portal Session
  credential replaces its CSRF token. This is a per-session synchronizer token,
  not additional account authority or a substitute for session validation.
- **Q241 — Missing retention key must not reset usage:** Reject a configured
  pepper removal while it protects unexpired Retained Quota Usage. If the
  service starts with such a key already missing, fail new-account registration
  temporarily closed until the key is restored or the affected quota window
  ends. Do not interpret an unmatchable digest as no prior consumption. Other
  operations remain subject to their normal credential/key checks; there is no
  raw-identity or unkeyed fallback. This refines Q180's early-removal behavior:
  invalidating credentials is safe, silently resetting retained quota is not.

### Final review ready

All policy questions Q215–Q241 are accepted, including the Q225 terminology
clarification. The consolidated contract covers the Module Interfaces, durable
record shapes and constraints, ordering, transaction ambiguity, quota and
deletion races, login continuation security, cleanup, and migration rules.
No further policy question is open within this ticket. Numeric operational
limits, Helm/Argo orchestration, backup/restore, and release evidence remain
with their existing dependent tickets; no new frontier is claimed.

The user subsequently confirmed the consolidated contract as the shared
understanding. The resolution below supersedes the preceding review-pending
notes.

## Answer

Resolved after the user's explicit final confirmation of Q215–Q241 and the
[Single-process persistence and Module contract](../persistence-contract.md).
That linked asset is the accepted detailed contract for this ticket.

Use one Go dependency module and binary with Account, Quota, and Relay Modules;
keep complete operations and their ordering/error guarantees behind their
Interfaces. Use pgx and explicit PostgreSQL SQL, typed privacy-limited records,
short locked transactions, and no durable Device Identity or Relay Session
registry. The contract specifies schema constraints/indexes, credential and
administrative transaction units, local admission ordering, bounded allocation
recovery, quota changes, and deletion/re-registration without a usage reset.

Quota Committed remains the durable consumption measure; Retained Quota Usage
preserves only deleted-account consumption for the current window. Credential
generations and temporary login fences prevent older flows restoring revoked
authority. Unfinished Google sign-ins are process-bound; Portal Sessions and
Login Grants remain durable. CSRF tokens are derived per session, and missing
retention keys cannot silently reset quota. Cleanup preserves accepted deadlines;
schema changes require explicit stop-migrate-start with exact revision checks.

No additional policy question remains in this ticket. Deployment orchestration
continues in [Define the Helm and Argo CD deployment contract](11-define-helm-and-argo-cd-deployment-contract.md);
restore safety in [Define PostgreSQL backup, restore, and deletion recovery](15-define-postgresql-backup-restore-and-deletion-recovery.md);
numeric operating bounds in [Define remaining operational Safety Limit defaults](16-define-remaining-operational-limit-defaults.md);
and verification/release evidence in [Define acceptance, release, and operating criteria](13-define-acceptance-release-and-operating-criteria.md).
No production implementation, executed verification suite, or deployment is
claimed. No further frontier is claimed by this resolution.
