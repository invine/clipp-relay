# Single-process persistence and Module contract

Status: accepted after explicit final shared-understanding confirmation;
planning contract, not an implementation.

Owner: [Define internal Go modules and PostgreSQL persistence](issues/14-define-go-modules-and-postgresql-persistence.md).
Q215–Q241 and the concrete design below are accepted. The owning ticket records
the resolution and links this asset as its detailed contract.

## Scope and Interfaces

One Go dependency module and binary; three internal Modules, concrete PostgreSQL
storage using pgx and parameterized SQL, and HTTP/Google/libp2p Adapters. These
operation names describe Interfaces, not an already compiled Go API.

| Module | Complete operations and ownership |
| --- | --- |
| Account | Begin/complete authorization; exchange code; rotate refresh credential; check access; read profile/admin views; change account policy; create/archive plan; revoke account credentials; delete account. Owns durable identity, credentials, administrative authorization, and atomic mutation/audit rules. |
| Quota | Fund an account-week credit balance, reconcile an allocation, observe endpoint consumption, invalidate credit, and handle week rollover. Owns the one in-flight allocation and local credit generation; does not issue SQL per copied buffer. |
| Relay | Authenticate a particular connection, replace a peer's authoritative session, invalidate an account's sessions, maintain reservation/Rendezvous ownership, expose aggregate capacity, and drain. Owns all live Peer IDs, connections, timers, and lease generations. |

Interfaces include authorization, ordering, expiry, commit ambiguity, and local
invalidation semantics—not just method signatures. HTTP and stream handlers
cannot compose table CRUD calls into their own transactions. PostgreSQL
transaction handles stay inside the concrete persistence Implementation.
Cross-Module commands use shared account-ordering machinery and narrow
invalidation/admission Seams; no hypothetical interchangeable-database Interface.

Return typed invalid-credential, blocked-account, conflict, quota-exhausted,
capacity, and temporary/uncertain outcomes. Adapters map them to the already
settled portal and wire behavior; raw SQL errors never become public responses.

## Durable schema

Internal IDs are random UUIDs; counts/revisions are nonnegative `bigint` values;
timestamps are finite UTC `timestamptz`; weekly keys identify Monday UTC.
Enumerations and bounded fields use explicit checks. Protected values use
purpose-separated HMAC-SHA-256 digests plus a key version. Constraints enforce
digest lengths, required field combinations, uniqueness, and valid lifecycle
relationships. No generic JSON metadata or request/response blobs.

| Table | Columns and principal constraints |
| --- | --- |
| `accounts` | ID; issuer/subject; latest allowed email/verified/hosted-domain claims and validation time; created/last successful Google portal-login times; public status; plan ID; nullable traffic/session overrides; administrator revision; credential generation; private deletion-request time. Unique live issuer/subject. Active requires a plan. Completed-deletion rows detach identity/presentation/assignment fields and are excluded from all account-facing reads; in-progress deletion retains identity only until journal-proven finalization and fences re-registration. Recovery state follows the accepted extension below. |
| `quota_plans` | ID, name, weekly byte allowance, session limit, revision, creation/archive times, and first-assignment marker. Nonnegative limits. Once assigned, allowances are immutable; archived plans cannot receive new assignments. Existing assignments remain valid. |
| `weekly_quota_usage` | Primary key account ID/window; committed bytes; allocation sequence; one latest operation ID and granted amount. A quota reduction may leave committed bytes above the new allowance: do not constrain committed bytes to the current plan. |
| `retained_quota_usage` | Primary key key-version/identity-digest/window; committed bytes. No old/new account ID, identity claims, plan, state, or device information. Exists only for the current window. |
| `authorization_transactions` | ID; portal/client flow kind; registered client and exact validated redirect for client flows; client-state digest and S256 challenge where applicable; independent Google-state/nonce digests; browser-binding digest; optional resolved account/generation; creation/expiry and one-use claim/completion state. Recoverable outer client state exists only in the required process-local continuation under Q239. No Google tokens or unagreed Google-hop PKCE verifier. |
| `authorization_codes` | Unique code digest/key version; account/generation; registered client, exact redirect, S256 challenge; issued/expiry/consumed times. One successful redemption. |
| `login_grants` | ID, account/generation, registered public client type, current refresh generation, creation/last-use times, inactivity/absolute deadlines, termination status/time. No installed-device identity or user-managed individual revocation surface. |
| `refresh_token_generations` | Grant ID/generation, unique digest/key version, issued/consumed times. Unique grant/generation. Consumed hashes remain while the grant is active to detect replay. |
| `relay_access_tokens` | Unique digest/key version, account/grant references, issued/expiry times. Generation and grant validity are checked through their owning records. Audience and permitted operations are fixed service policy, not client-defined claims. |
| `portal_sessions` | Unique digest/key version, account/generation, Google authentication time, creation/last-use and idle/absolute deadlines; per-session CSRF digest under Q240. No redirect URL or identity claims in the session cookie. |
| `audit_events` | ID, time, enumerated event/outcome/reason, optional target account ID, optional plan ID, acting administrator email and public client type when relevant. No account foreign key that would delete retained audit events. No link to retained usage or a replacement account. |
| `schema_migrations` | Ordered revision, immutable checksum, application time; written atomically with each migration. |

Index live identity uniqueness, credential digests, grant/generation uniqueness,
account-owned child lookups, status/admin pagination, expiry/cleanup scans,
account-week lookup and expired-window scans, and bounded audit reads/retention.
Index columns used to find dependent rows before deleting a parent; PostgreSQL
does not automatically create every useful referencing-column index.
Avoid indexes or auxiliary lookup tables for unneeded email/device searches.

There are no durable Relay Session, connection, Peer ID, reservation, Rendezvous,
Device Identity membership, per-buffer traffic, or per-allocation history tables.
Database schema and SQL review tests check this allowlist as well as credential
storage and audit-field restrictions. Schema checks complement—not replace—
parameterized queries, bounded input validation, and redacted logging.

## Ordering and transaction units

Use short Read Committed transactions with explicit locks and authoritative
revalidation after lock acquisition. An earlier lookup may resolve a row ID,
but cannot authorize an operation. PostgreSQL wall time sampled after waits
determines durable expiry and week; transaction-start time is insufficient.
Convert validated remaining lifetimes to conservative monotonic deadlines.
Clock regressions never extend an existing deadline or revive discarded credit.

Lock order for the relevant subset of locks:

1. In-process External Identity guard for registration, login completion,
   revocation/blocking and deletion, or per-Peer-ID guard for connection
   authentication. These paths do not nest those guards.
2. All involved account guards in sorted account-ID order, including an acting
   administrator's account when its session must be validated.
3. PostgreSQL account row locks in the same order, followed by relevant plan,
   credential, and account-week rows in consistent key order.
4. Brief live registry update, with no database/network wait under its mutex.

An operation never acquires an earlier guard while holding a later one. Identity
guards exist only while needed, not as a permanent identity registry; the
single-serving-process invariant makes this sufficient for absent-account
serialization, backed by database uniqueness. Plan-only administration locks
the acting account then the plan. Helpers must not hide reverse lock acquisition.

| Operation | Atomicity and recovery contract |
| --- | --- |
| Complete Google authorization | Require browser binding and the process-local continuation; atomically claim the flow before exchanging the Google code. Validate the provider response outside database locks; serialize canonical identity, check its revocation fence, resolve/create account, then recheck state and generation before issuance. A claimed flow with uncertain completion may require fresh login. |
| Redeem code / refresh | Account lock plus one-use credential transition and new credential digests in one transaction; enforce account state, generation, client binding, deadlines, and grant cap. Strict refresh replay remains unchanged; no generic retry after an ambiguous commit. |
| Allocate credit | Under the account guard, validate account/week and expected allocation sequence; allocate at most 64 KiB or the remaining allowance and atomically increment committed usage with the receipt. Retry the same operation/sequence only until reconciled. An older sequence cannot become a new debit after a later allocation. Commit before installing local credit; apply once and recheck local generation/account/week. |
| Cancel or restart allocator | Reconcile an in-flight operation before permitting another. Invalidated workers cannot install late receipts. Fresh processes use fresh operation IDs and never restore old unspent credit, even if the last receipt is present. A committed but unusable allocation is not refunded. |
| Change plan/override | Check fresh admin authority and optimistic revision; commit policy plus audit, then apply Q229's local-credit/session consequences before acknowledging completion. Below committed traffic closes all; equality preserves already-funded remainder. Session cap below live count closes all rather than selecting survivors. |
| Revoke / block / delete | Serialize against admission and issuance; advance credential generation and commit audit with mutation, then invalidate live permissions and close within the accepted ten seconds. Unknown commit conservatively closes and quarantines admission until reconciled; never report false success or resurrect closed sessions. |
| Delete / re-register identity | Freeze competing identity/account operations; deletion stores current committed usage, invalidates credentials, detaches identity and marks old data for purge atomically. Registration creates a fresh Pending account and imports/deletes retained usage in one transaction, exactly once. Lookup uses configured key versions; conflicting duplicate logical records fail closed rather than double-import. No old audit/history links transfer. |
| Replace a peer session | Under peer and sorted account guards, validate authorization, quota and capacity; atomically install a new live generation. Close only the captured old connection afterward. Stale timer/close/Rendezvous callbacks compare ownership generation before removal. Failed admission leaves the previous session intact. |
| Read profile | Read durable plan/override/week totals in one consistent statement snapshot, or one explicit read-only consistent snapshot when multiple statements are necessary. Sample live counts separately. Historical rows show absolute committed bytes, not reconstructed historical allowance percentages. |

Accepted recovery extension: before the deletion-finalization transaction above,
persist the in-progress operation and fence admission, then prove committed
Recovery Journal intent outside database locks. Final identity detachment,
current-week retained usage and deletion audit remain atomic. A failed/ambiguous
journal write is incomplete deletion, not success; restart resumes the same
operation. Re-registration remains fenced until detachment completes. The
[recovery contract](recovery-contract.md) owns this ordering, journal replay and
the additional durable operation/run markers, Recovery Review Holds and
current-week reset-notice state. They do not introduce a device registry or a
normal-operation quota refund.

The bandwidth reporter still attributes a callback to the current authoritative
Peer-ID association. Old-connection tail may charge a replacement account;
without an association it is aggregate unattributed traffic. Generation fencing
does not solve that accepted limitation or establish a numerical overshoot bound.

## Unfinished login and browser protection

Under accepted Q239, the database transaction and a live in-memory continuation
are both necessary to complete a Google sign-in. Keep the recoverable outer
Clipp state only in that bounded continuation, never in PostgreSQL or logs.
Use a separate opaque Secure, HttpOnly, SameSite=Lax, host-only browser-binding
cookie with its digest in the transaction. Restart cancels unfinished flows;
it does not invalidate otherwise-valid durable Portal Sessions or Login Grants.

Flows capture a process-local start sequence. Revocation/blocking/deletion
records a later per-External-Identity fence under the identity/account guards;
completion checks it after Google resolves the identity. Fence keys use a
process-random HMAC key, never an account ID or Device Identity. Keep them until
all older flows expire, at most the ten-minute flow lifetime. Uncertain
revocation commits fence conservatively. Restart drops both continuations and
fences, so an old flow cannot bypass a lost fence. Bounded capacity must not
evict a needed fence; refuse new work rather than lose the invariant.

Accepted Q327 in [Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md)
refines capacity overflow: if a security mutation cannot install a needed
fence, atomically invalidate all unfinished login flows (including callbacks
in provider exchange) before reclaiming their now-unneeded fences and allowing
new flows. This preserves the security mutation without revoking unrelated
established sessions. A full continuation store still refuses new login starts;
it does not evict existing flows merely to admit a newcomer.

Under accepted Q240, derive a purpose-separated CSRF token from the presented
Portal Session credential and its configured pepper and persist only the
token's digest. Regenerate it for forms, compare submitted tokens to the stored
digest, and require the accepted Origin/session checks. The token stays stable
across tabs and restart; replacement of the session credential replaces it.
Do not put it in URLs or a JavaScript-readable cookie.

## Cleanup, migrations, and verification

Cleanup runs bounded indexed batches at startup and periodically, rechecking
eligibility under the same ordering rules as foreground operations. No unbounded
account cascade is required: delete expired/deleting child rows in batches, then
the hidden account row. Expiry/invalidation applies immediately even if physical
cleanup is late. Preserve the accepted deadlines: deleted account data and ended
credentials within 24 hours; Pending expiry after 90 days without Google portal
login; quota history current plus 12 completed weeks; audit 180 days; retained
usage until window end. Consumed refresh hashes survive while their grant is
active. Cleanup delay is observable and never resets the retention clock.

Under accepted Q241, reject removal of a pepper protecting unexpired Retained
Quota Usage. Starting with such a key missing temporarily blocks new-account
registration until restoration or expiry of the affected window; unreadable
protected usage is never a zero balance. Other operations retain their normal
credential/key checks, with no raw or unkeyed fallback.
Cleanup cadence, batch sizes, and related operational limits belong to
[Define remaining operational Safety Limit defaults](issues/16-define-remaining-operational-limit-defaults.md).

Serving credentials cannot perform DDL. Each build checks its exact schema
revision. Operators drain/stop serving before the separate migration command;
serialize migration runners on a dedicated connection, verify checksums, and
commit each transaction-compatible migration with its revision marker. Initial
migrations must not require concurrent index creation. Serving never auto-migrates.
Image rollback requires compatible schema; otherwise use a forward fix or the
separately designed recovery procedure. This is not an online migration promise.

Future verification uses real PostgreSQL, not SQLite or mocked SQL behavior:

- Concurrent code redemption, refresh replay, issuance versus revocation, and
  unknown-account Google completion versus deletion/re-registration.
- Allocation lost responses, known rollback, late receipts, cancellation,
  restart, quota equality/downgrade/zero, and lock waits spanning Monday UTC.
- Repeated delete/register cycles, key rotation/missing keys, exactly-once usage
  import, and absence of links to the old account.
- Same-peer cross-account replacement with stale timers and callbacks;
  database outage consumes only confirmed local credit until fixed deadlines.
- Bounded cleanup, authoritative expiry despite lag, permitted audit retention,
  schema privacy checks, and no raw credentials or stable identities in logs.
- Fresh database migration, supported upgrade paths, runner contention,
  checksum/version mismatch, and failure rollback of each migration transaction.

No implementation or tests are claimed by this contract. Helm/Argo stop-migrate-start
orchestration belongs to the deployment ticket; restore anti-resurrection belongs
to the recovery ticket; production performance and release evidence belong to
the acceptance ticket. No further frontier is claimed here.

Recovery-policy cross-reference: accepted revised Q289 in
[Define PostgreSQL backup, restore, and deletion recovery](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md)
allows unrecoverable current-week consumption to reset to zero during an
explicit disaster restore. It does not alter normal non-refundable allocation,
restart recovery, or missing-pepper refusal. The recovery ticket owns this
exception, its scope and reconciliation gates; do not infer a normal-operation
zero-on-error path from it.
