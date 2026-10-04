# Managed Clipp relay — single-process v1 specification

Status: ready-for-agent
Consolidated: 2026-09-27
Decision baseline: accepted Wayfinder decisions through Q358, including explicit revisions.
Testing seams: confirmed by the user on 2026-09-27.
Production implementation and release acceptance: not completed by this document.

## Problem Statement

Clipp needs a self-hosted relay that works with Electron, Android and the Chrome
extension when direct connectivity is unavailable. Operators need to approve
access, set consumption allowances, inspect service health and deploy through
Argo CD on an existing OCI Kubernetes cluster. Users need browser registration,
login and useful account statistics without a device registry or a change to
Clipp's end-to-end Device Network trust model.

The initial service must remain simple enough to operate as one process while
handling expiry, concurrent mutations, uncertain commits, restart and disaster
recovery explicitly. Approved limits and throwaway prototypes are not evidence
of production safety or capacity.

## Solution

Build one Go service with an internal Relay Coordinator, one Relay Instance,
a server-rendered account/admin portal and PostgreSQL-backed account, credential
and quota operations. Google login establishes a Relay Account; administrator
approval and assigned weekly traffic/concurrent-session allowances govern use.
Stock Circuit Relay v2 forwards opaque bytes through TCP, WSS and WebRTC Direct,
with a small connection-specific authentication adapter rather than a fork.

Each managed Relay Configuration uses one stable HTTPS discovery endpoint to
learn the process's ephemeral Peer ID and transport addresses. Clipp verifies
that peer, authenticates the exact connection, then reserves and registers with
Rendezvous. Every configured relay is attempted independently. Explicit
unauthenticated persistent-identity relays remain supported client configurations,
never an automatic downgrade.

Ship a hardened singleton Helm deployment, external/bundled PostgreSQL options,
manual stop/migrate/serve Argo operation, verified off-cluster recovery and private
Prometheus-compatible metrics. Keep the existing budgets and the 1,000-account /
5,000-concurrent-session scale goal; start at lower demand and tune after code
exists. Full-scale proof is not an initial-release prerequisite, but security,
correctness and recovery gates are.

## User Stories

1. As a Clipp user, I want relay connectivity across desktop, Android and the extension, so that my Device Network works when direct connections fail.
2. As a Clipp user, I want one discovery endpoint per managed relay, so that process identity and address changes need no manual reconfiguration.
3. As a Clipp user, I want all configured relays attempted independently, so that one failure does not block another.
4. As a Clipp user, I want direct networking without relay login, so that relay availability does not disable my Device Network.
5. As a Clipp user, I want an explicit unauthenticated-relay option, so that I can use separately operated persistent relays without an unsafe downgrade.
6. As a Clipp user, I want relay add/update/remove on every runtime, so that settings are not desktop-only.
7. As a Clipp user, I want duplicate and conflicting entries detected, so that credentials cannot silently switch endpoints or accounts.
8. As a Clipp user, I want settings changes without host restart, so that direct peers and unrelated relays stay connected.
9. As a Clipp user, I want browser sign-in only on my action, so that reconnect never unexpectedly opens a browser.
10. As a Clipp user, I want login scoped to one relay, so that independent services never share credentials.
11. As an Android user, I want any self-hosted relay to work without a custom APK or callback website, so that setup stays simple.
12. As an Electron user, I want OS-protected renewable credentials or memory-only use, so that absent secure storage never means plaintext persistence.
13. As an Android user, I want Keystore-protected credentials excluded from backup/transfer, so that ordinary preferences are not a credential store.
14. As an extension user, I want renewable credentials confined to trusted contexts, so that content scripts and the offscreen network do not receive them.
15. As a Clipp user, I want one refresh in flight per relay, so that concurrent consumers cannot replay a single-use token.
16. As a Clipp user, I want a warning after credential-save failure, so that I know restart will require login.
17. As a Clipp user, I want accurate Ready/degraded/limit/login-required states, so that connection alone is not mistaken for usable service.
18. As a Clipp user, I want working circuits preserved during Rendezvous-only failure, so that independent repair does not disrupt traffic.
19. As a Clipp user, I want limit rejection to retain valid credentials, so that capacity problems do not force authorization.
20. As a Clipp user, I want bounded retries honoring server hints, so that outages do not cause reconnect storms.
21. As a Clipp user, I want signed reachability records to authorize no new relay, so that a peer cannot expand my credential trust boundary.
22. As a Clipp user, I want Device Identity and history preserved, so that relay integration does not migrate unrelated data.
23. As a new owner, I want Google registration to create one Pending account, so that repeated login cannot create duplicates.
24. As an owner, I want all four account states explained, so that I understand whether relay access is available.
25. As an owner, I want current Quota Committed displayed, so that I understand conservative consumption rather than a misleading exact-byte total.
26. As an owner, I want historical weekly committed totals, so that I can review usage without percentages based on today's plan.
27. As an owner, I want aggregate session and Login Grant capacity, so that I can see limits without a device registry.
28. As an owner, I want portal-only logout, so that leaving the website does not revoke Clipp credentials.
29. As an owner, I want Sign out everywhere, so that all portal and relay credentials are invalidated together.
30. As a Clipp user, I want Manage relay account to open the correct portal, so that I can inspect statistics and revoke access deliberately.
31. As an owner, I want self-deletion after recent Google authentication, so that I can erase account data safely.
32. As a returning deleted-account owner, I want a fresh Pending account with only current-week consumption retained, so that privacy is preserved without a quota-reset loophole.
33. As an owner, I want incomplete deletion reported honestly, so that an uncertain journal write is not called completed erasure.
34. As an owner, I want recovery resets identified, so that a zero total is not presented as proof of no traffic.
35. As an administrator, I want a Secret-backed authoritative-email allowlist, so that Google needs no custom role claims.
36. As an administrator, I want to approve my own Pending account when allowlisted, so that bootstrap needs no database role editing.
37. As an administrator, I want explicit approval/suspension/denial/review transitions, so that status cannot silently grant access.
38. As an administrator, I want Quota Plans and overrides, so that clients cannot select or raise allowances.
39. As an administrator, I want assigned plan allowances immutable, so that editing a plan cannot silently change every account.
40. As an administrator, I want reductions applied without resetting consumption, so that policy changes are predictable.
41. As an administrator, I want revisions and structured reasons, so that concurrent edits conflict safely and remain accountable.
42. As an administrator, I want transactional restricted audit records, so that a mutation cannot succeed without required accountability.
43. As an administrator, I want account-wide revocation, so that suspicious access can be stopped without tracking devices.
44. As an administrator, I want restored accounts held for review, so that an old backup cannot silently restore unsafe access.
45. As an operator, I want cross-account circuits, so that Relay Account ownership does not redefine Device Network membership.
46. As an operator, I want exact live admission counts, so that concurrent authentication cannot bypass session limits.
47. As an operator, I want a newly authenticated same-Peer-ID connection to replace stale authority within its transport family while independently authenticated transport families can coexist.
48. As an operator, I want both circuit endpoints charged, so that sending and receiving each consume allowance.
49. As an operator, I want finite prepaid local credit, so that forwarding needs no SQL per buffer or unconfirmed credit.
50. As an operator, I want no restart/disconnect refunds, so that reconnecting cannot reset committed usage.
51. As an operator, I want Monday UTC rollover without unused allowance carryover, so that weekly policy is unambiguous.
52. As an operator, I want stock circuit/resource safety limits, so that account quotas are complemented by bounded work.
53. As an operator, I want exact-connection authentication before HOP/Rendezvous, so that another connection inherits no permission.
54. As an operator, I want exact-Peer-ID Rendezvous without enumeration, so that discovery exposes no account/device directory.
55. As an operator, I want complete atomic address publication, so that clients never receive partial enabled-transport state.
56. As an operator, I want fresh process identity on restart, so that relay keys need no persistent custody.
57. As an operator, I want non-overlapping replacement and bounded drain, so that one slot never routes to two ephemeral identities.
58. As an operator, I want DB failure to deny new authorization without failing data-plane liveness, so that outages do not create restart loops.
59. As an operator, I want external or bundled PostgreSQL, so that deployment fits my infrastructure.
60. As an operator, I want verified DB TLS and separated roles/Secrets, so that serving receives no maintenance authority.
61. As an operator, I want explicit stop/migrate/serve, so that migrations cannot race serving.
62. As an operator, I want missing data to fail closed and PVCs retained, so that restart/uninstall cannot silently create an empty service.
63. As an operator, I want verified leaf-certificate reload, so that Secret changes are not mistaken for successful renewal.
64. As an operator, I want private metrics and redacted stdout logs, so that operation is diagnosable without Prometheus or device tracking.
65. As an operator, I want tested external notifications, so that loss of the relay or observations is actionable.
66. As an operator, I want off-cluster backup and deletion evidence, so that database rollback cannot resurrect acknowledged deletions.
67. As an operator, I want isolated idempotent recovery, so that retries cannot revive credentials or reset quota twice.
68. As an operator, I want bounded cleanup and visible breaches, so that outages never silently extend privacy deadlines.
69. As a maintainer, I want complete-operation/public-interface tests with real PostgreSQL, so that tests prove behavior rather than mock transactions.
70. As a maintainer, I want executed ARM64/AMD64 and real-client evidence tied to immutable releases, so that building is not mistaken for compatibility.
71. As a maintainer, I want limits and capacity caveats documented together, so that later tuning cannot silently change goals or claim unmeasured capacity.
72. As an operator, I want human promotion and tested runbooks, so that CI alone cannot authorize production changes.

## Implementation Decisions

### I1. Architecture and module boundaries

- One Go dependency module, binary, serving process and Pod. Coordinator and Relay
  Instance roles communicate in-process: no network introspection/registration,
  independent control plane or pool.
- Account owns complete authorization, exchange/refresh/access checking,
  profile/admin reads, account policy, plan creation/archival, revocation and
  deletion operations, including atomic durable audit.
- Quota owns account-week funding/reconciliation, observed endpoint consumption,
  local credit generations/invalidation and rollover, with one in-flight allocation.
- Relay owns exact-connection auth, replacement, account invalidation,
  reservation/Rendezvous ownership, aggregate capacity and drain. Peer IDs and
  session/timer generations live only here in memory.
- HTTP, Google and libp2p adapters translate complete operations. Interfaces
  include ordering, authorization, expiry and uncertainty, not generic table CRUD.
  Use concrete pgx v5 and parameterized SQL, no ORM/code generator or hypothetical
  interchangeable datastore interface. SQL transaction handles remain internal.
  Typed invalid-credential/blocked/conflict/quota/capacity/temporary-uncertain
  outcomes map to public behavior; never expose raw SQL errors.
- Portal uses Go net/http, html/template, embedded CSS/assets and small plain-JS
  enhancements, not a separate frontend app or deployed Node runtime.
- Relay Account, Device Identity and Device Network remain distinct. Persist no
  Peer ID/device ownership/installation/hardware/fingerprint. Cross-account circuits
  are allowed. Existing trust, pairing, signed reachability and clip/history
  semantics remain; the relay forwards opaque bytes without content inspection.
- Keep stock Circuit Relay v2 wire/forwarding with a small host/HOP adapter that
  gates the exact stream connection and a stock bandwidth reporter. No circuit
  ownership registry or fork. STOP uses existing authenticated connections;
  reject new relay-initiated peer dials. Disable unused AutoNAT/dial-back, UPnP,
  auto-relay/client-relay transport and relay-side hole punching.
- Pin exact production dependencies and reverify behavior at release. Evidence
  used go-libp2p 0.49.0 and go-yamux/v5 5.1.0; prototype pins do not automatically
  select every production dependency or approve future behavior.

### I2. Accounts, administrator policy and portal

Random account UUID is separate from unique canonical Google `(issuer, sub)`.
Validate Google's documented issuer spellings and canonicalize to
`https://accounts.google.com`. Email is presentation, never identity. Concurrent
first logins resolve atomically to one Pending account. Keep only latest email,
email_verified, hd, validation time and last successful Google portal-login time;
no email history, name, avatar, contacts or Google tokens.

| State | Permitted administrator transition |
| --- | --- |
| Pending | Active with Quota Plan assignment, or Denied |
| Active | Suspended or Denied |
| Suspended | Active or Denied |
| Denied | Pending for review; separate subsequent approval |

No automatic status transitions. Every state may use the portal. Only Active,
non-deleting, non-held accounts obtain/use relay credentials. Blocking revokes
credentials and closes sessions while preserving policy/history; reactivation
restores no credential. Recovery Review Hold is not a fifth public state.
Owners see exact status and generic explanation, not internal reason codes,
administrator identity or audit history. Protocol authentication errors remain
non-specific.

One administrator role derives solely from the current mounted allowlist.
Require validated Google identity for this web client, email_verified=true,
exact match after trimming and ASCII case-folding only, and authoritative email:
Gmail or verified email with hd present. Do not strip dots/plus suffixes. Ordinary
accounts can use other Google identities. Every admin request checks the current
allowlist against latest validated metadata; account status is irrelevant to
admin authority. An allowlisted admin may approve its own account.

All admin mutations and self-deletion require Google authentication at most ten
minutes old. Viewing and owner revocation need a valid Portal Session; admin
credential revocation remains an admin mutation. Use optimistic revisions;
stale updates conflict and require reload. Enumerated reasons are
routine_administration, policy_enforcement, suspected_abuse, security_response,
support_correction; no free-form notes. Plan operations use the same fresh
authority/reason/atomic-audit rules.

Quota Plans have nonnegative weekly-byte/session limits; zero means no allowance,
not unlimited. Once assigned, allowance values are immutable. Replacement plans
need explicit account reassignment; archive blocks new assignments but preserves
existing ones. Only admins assign plans/overrides. Redeploy never resets them.

Use the accepted light portal shell and explanatory non-Active views. Active
profile: current-week **Quota committed** doughnut and compact aggregate session/
Login Grant capacity table; no individual devices/sessions/grants. Explain unused
funded credit, exceeded and zero-allowance states. Historical weeks show absolute
committed bytes, not percentages against today's plan. Policy/usage reads are
one consistent DB snapshot, live counts a separate sample. Admin workspace has
service totals, account table and selected-account mutation pane with shared
styling, reason/revision controls. Detailed layout/polish remains deferred.

### I3. Authorization and credentials

Google is the sole initial provider. Coordinator is a confidential web OIDC
client with Secret-backed client ID/secret, scopes `openid email`, no Google
offline access. Exact registered HTTPS callback, separate random Google state/
nonce and browser binding. Maintained verifier checks signature, issuer, audience,
expiry and nonce; discard Google tokens. Never trust decoded-only claims,
token-selected URLs or arbitrary proxy headers.

Clipp is a separate authorization hop: three registered public client types,
no client secret, authorization code plus S256 PKCE only, independent state and
32-byte CSPRNG verifier. Code binds account/generation, client, exact redirect
and challenge. `/oauth/authorize` and HTTPS form POST `/oauth/token`; no-store
token responses put credentials only in body. Only the one-use code/correlation
state enter the intended callback, never Google/Relay Access/refresh tokens.

| Runtime | Browser and registered callback |
| --- | --- |
| Electron | System browser, temporary IPv4-loopback `http://127.0.0.1:{ephemeral-port}/oauth/callback`; only port varies, one callback then close listener. |
| Android | External Custom Tab, fixed registered private application URI; exact URI/state and S256 redemption at initiating relay. No shared callback website, App-Link hosting or per-relay APK. |
| Extension | Explicit interactive chrome.identity.launchWebAuthFlow; exact `https://<extension-id>.chromiumapp.org/clipp-relay` from identity API. |

Reject arbitrary return targets, localhost aliases, non-loopback desktop hosts,
wrong paths, userinfo, fragments and redirect mismatches. Android URI/extension
release identity are exact build/deployment inputs, not client-selected arbitrary
redirects. Pending client login gets no code and access_denied with safe approval
guidance; Suspended/Denied get no credential. Portal access remains available.

| Credential/control | Lifetime or ceiling |
| --- | --- |
| Authorization transaction | 10 minutes |
| One-use authorization code | 5 minutes |
| Relay Access Token | 15 minutes |
| Portal Session | 30 minutes inactivity; 12 hours absolute |
| Sensitive-action Google authentication | At most 10 minutes old |
| Login Grant | 30 days inactivity; fixed 180 days absolute |
| Active Login Grants/account | 20; reject excess without eviction |

Security ceilings may be shortened, not extended; no expiry grace. Each client
authorization creates a grant bound to client type, never a device. Opaque Relay
Access Tokens allow only this service's Discovery and Relay Authentication, no
portal/admin authority. Check current account/generation before code issuance,
exchange, refresh, discovery and relay authentication.

Refresh atomically consumes its strictly single-use generation and issues new
refresh/access tokens. Inactivity resets, absolute lifetime does not. Previous
Access Tokens remain valid until their expiry. Consumed-token reuse revokes that
grant and its Access Tokens; no overlap/retry window. Lost committed response can
require new browser login. Existing Relay Sessions have no grant association and
remain only until fixed expiry; other grants survive. No user/admin individual
grant listing or revoke API. Independently issued credentials do not depend on
later Google outage/logout/email change/disablement. Bearer possession remains
sufficient; there is no durable proof-key/device binding.

Portal cookie is rotated opaque `__Host-`, Secure, HttpOnly, SameSite=Lax, referencing
server-side state. State-changing POSTs require allowed Origin and synchronizer
CSRF. Derive a purpose-separated per-session token from session credential/pepper,
store only digest and regenerate for forms; stable across tabs/restarts, replaced
with credential, never in URL or JS-readable cookie. No selectable redirects or
identity claims in cookies.

Log out ends current Portal Session only. Sign out everywhere / admin Revoke
credentials invalidate all grants, tokens and Portal Sessions, advance credential
generation and close all account Relay Sessions within ten seconds. Commit before
success and clear initiating cookie; preserve status/policy/usage. Suspension and
denial apply equivalent invalidation. Reactivation restores no credentials.

Codes, tokens, portal IDs, state/nonce use independent 32-byte OS-CSPRNG values,
unpadded base64url. Store only purpose-separated HMAC-SHA-256 digests/key versions.
Use current/retiring Secret peppers, validate all configured versions, keep keys
through protected-record expiry and cleanup. Reject removal protecting unexpired
Retained Quota Usage. If missing at startup, block new-account registration until
key restored or affected window expires, never treat unreadable usage as zero.
Early removal may invalidate credentials; no raw/unkeyed fallback.

Unfinished login requires digest-only durable transaction **and** bounded live
continuation holding recoverable outer client state. Separate opaque host-only
Secure/HttpOnly/SameSite=Lax browser-binding cookie checked by digest. Claim once
before Google exchange; no DB locks during provider I/O. Restart cancels unfinished
flows, not durable valid Portal Sessions/grants. Process-random-HMAC identity
fences compare flow-start sequences after identity resolution, preventing old
flows completing across revocation/deletion/re-registration. Keep until older
flows expire. Full continuation capacity refuses newcomers. If security mutation
cannot install a fence, atomically invalidate all unfinished flows, including
provider exchanges, before reclaiming fences; unrelated established sessions/
credentials remain valid. Uncertain revocation fences conservatively.

### I4. Discovery, authentication and Rendezvous contracts

Bearer-authorized `GET /v1/relay` at the exact canonical HTTPS URL, no redirects.
Success fields: version=1, relay.peerId, relay.addresses (complete multiaddrs),
validUntil; no account/capacity/pool/extra epoch/Rendezvous-version data or extra
signature. HTTPS authenticates discovery; Peer ID identifies process generation.
Eligible Active accounts can discover regardless of account quota; auth admits.

Addresses form an unordered set. Atomically reject wrong version/expiry/Peer ID,
malformed address or any address naming another peer. Valid unsupported transports
may be ignored if at least one supported address remains. Try all supported
addresses. Refresh at validUntil, peer mismatch or all-address failure. Document
expiry never kills a live authenticated connection. Send token only after Noise,
including embedded transport Noise, proves expected remote Peer ID.

| Protocol | Wire contract |
| --- | --- |
| `/clipp/relay-auth/1.0.0` | One bounded unsigned-varint-length-delimited UTF-8 JSON request/response; request only accessToken; success ok=true, sessionExpiresAt, renewAfterMillis. |
| `/clipp/rendezvous/2.0.0` | Framed JSON; fixed topic clipp; register/lookup/unregister; unchanged Signed Peer Record as unpadded base64url; registration/found result leaseExpiresAt; miss ok=true without record. |
| `/clipp/rendezvous/1.0.0` | Existing unframed JSON/numeric byte-array format unchanged; same auth gate/lease store. Instrument throughout initial release. |
| Circuit Relay v2 | Stock HOP/STOP protobuf/forwarding/statuses with exact-connection HOP auth gate. |

Before auth allow only transport setup, Identify, Ping and Relay Authentication.
Every physical connection crosses the barrier; no peer-global inheritance.
Initial auth failure returns one error where framing permits and closes whole
connection. Same-account reauth can renew; failed renewal retains old deadline.
Same-connection account change returns account_change_requires_new_connection,
unchanged. Deadline is min(token expiry, session maximum), then whole-connection
closure. The user amendment of 2026-10-05 permits independently authenticated
same-account, same-Peer-ID connections on distinct TCP, WSS and WebRTC Direct
transport families. Each physical connection counts toward the existing account
and global session limits. Replacement within a family, or replacement across
accounts, is atomic only after admission checks; accounts must never share peer
authority. Failed admission leaves old sessions intact; renewal uses no new slot.
Close only captured replaced connections; timers/close/lease callbacks compare
ownership generation so stale cleanup never removes replacement or healthy
transport authority. Stock per-peer reservation and Rendezvous ownership remains
coherent across connections and can be re-established through a healthy transport.

Rendezvous register requires active session/live reservation, valid signature and
subject matching remote Peer ID, bounded envelope/address parsing and at least
one current-relay circuit address. Store/return envelope unchanged. Lease=min
(session, reservation, configured TTL), removed on owner loss/unregister; newest
valid registration atomically wins. Active-session lookup is exact-Peer-ID and
cross-account; no enumeration and no distinction among missing/expired/unregistered.
Prefer v2; fallback only on multistream unsupported, never errors/auth/quota/
validation/timeouts. No-settings-migration does not remove v1 wire compatibility.

Clipp requests: single bounded JSON object, unique keys, required exact types,
no unknown fields/trailing frames. Optionals omitted not null, RFC3339 UTC Z
timestamps, decimal millisecond integers, no compression; whitespace/key order
insignificant. Ignore additive response fields, reject unsupported majors.
Semantic error: one ok=false/code/optional retryAfterMillis then orderly close.
Bad framing/oversize/extra frames/write timeout reset. Enforce encoded/decoded
bounds before allocation, validate v1 integer bytes; no truncated signatures or
incomplete published snapshots.

Shared error registry: invalid_request, authentication_failed,
account_change_requires_new_connection, quota_exhausted, session_limit_exceeded,
reservation_required, invalid_peer_record, rate_limited, temporarily_unavailable,
unsupported_version. Unknown error fails closed; automatic retry needs recognized
retryable meaning or valid hint, else incompatibility plus slow health probe.
Discovery JSON/no-store: 200 success, 400 invalid, 401 auth, 429 rate_limited
**only at global active-session capacity**, 503 temporary for dependency,
publication, drain or front-door/rate overload. Retry-After agrees with JSON hint.
Other HTTP rate denials may use 429. Stock PERMISSION_DENIED covers absent/expired/
inactive auth; RESOURCE_LIMIT_EXCEEDED covers exhausted quota; preserve others.

### I5. Quota, transactions and schema

Weekly Quota Window: Monday 00:00 UTC to next Monday, no unused rollover. Initial
plan: 1 GiB (1,073,741,824 bytes) and five concurrent sessions. Exact process-local
session counts are account quota; reservation/circuit counts are Safety Limits.

Stock reporter charges HOP/STOP endpoint bytes independently, possibly including
control bytes; same account at both ends is charged twice. Callback has peer,
protocol and count, not connection ID: charge current association at report time.
Old-connection tail can charge replacement account; without association record
aggregate unattributed bytes only. No history reconstruction. Async cutoff may
allow buffered frames to finish; no proven numerical tail/overshoot bound.

Quota Committed debits at funding, not each observed byte. One account-week local
balance serves all sessions, funded before protected permission. One in-flight
allocation, no speculative next block; allocate ≤64 KiB (65,536 bytes) or remaining
allowance. Disconnect keeps unused live credit. No refund on disconnect/graceful
exit/crash; restart abandons old unspent credit while debit remains. A 64KiB
allocation with 10KiB observed shows 64KiB committed, 54KiB locally usable.

Persist account-week total/sequence and one latest operation receipt with stable
server operation ID/grant amount, not per-block history. Commit before local
install; reconcile same operation after lost response, apply once, recheck
account/week/generation. No later allocation until reconciliation; old sequences
cannot become new debits. Cancellation/invalidation/restart cannot install late
credit. Unconfirmed credit is unspendable; exhausted confirmed credit with
uncertain funding closes sessions as temporary failure, not false quota exhaustion.

Monday discards old credit. Valid sessions may continue after confirmed new-week
funding without mandatory sign-in/reset; DB failure closes them rather than
spending old credit. Reporter observations belong to processing week. Exhaustion
closes all account sessions, not selected circuits. DB outage permits established
sessions only within valid local credit/deadlines; portal/callback/exchange/
refresh/discovery/new auth fail closed, with no cached policy/DB substitute.

Reassignment/override applies this week without resetting usage. New traffic cap
below committed invalidates credit/closes all; equality preserves funded remainder
but no more allocation. Session cap below live count closes all then admits
reconnects within limit, no chosen survivors. Apply within ten seconds after
commit/local invalidation, no credential revocation for quota edit. Increases
do not refund discarded/committed credit.

Short Read Committed transactions, explicit locks and authoritative recheck.
Bound DB work admission before logical-guard waits. Lock order:

1. Temporary External Identity guard for identity/security operations **or**
   Peer-ID guard for session auth; never nest these guards.
2. All involved account guards sorted by ID, including acting admin if validating
   its session.
3. Account DB rows in same order, then relevant plan/credential/week rows in
   consistent key order; plan-only admin locks actor then plan.
4. Brief live-registry update, no I/O under its mutex.

No reverse acquisition or DB locks across provider/object-storage/connection-close
I/O. Uniqueness backs absent-account serialization. Use DB wall time **after**
lock waits for expiry/week, not transaction-start time; derive conservative
monotonic deadlines including transit uncertainty. Clock regression extends
nothing. Retry only known-aborted retryable work, bounded by attempt/deadline;
ambiguous commits need operation-specific recovery, never generic blind retry.

Issuance/mutation serialize on account row. Credential generation is separate
from admin revision; revocation/blocking fences older credentials/flows/codes,
ordinary plan edits do not advance it. Commit mutation+audit then invalidate
live permissions/close. Unknown security commit conservatively closes/quarantines
admission until reconciled, reports uncertainty and never resurrects closed
sessions. Same-peer replacement guards both accounts in order, swaps after checks.

| Table | Required typed contents/constraints |
| --- | --- |
| accounts | Random ID; unique live issuer/sub; latest permitted metadata/times; four-state status; plan/nullable overrides; admin revision; credential generation; private deletion/recovery markers. Active requires plan. Completed deletion detaches identity/presentation/assignment and hides row. |
| quota_plans | ID/name/nonnegative allowances/revision/create/archive/first-assignment markers; assigned allowances immutable. |
| weekly_quota_usage | Unique account/Monday-window, committed bytes, sequence/latest receipt; committed may exceed reduced cap. |
| retained_quota_usage | Key-version/identity-HMAC/window/committed only, no account IDs/claims/plan/state. |
| authorization_transactions | Flow/client/exact redirect, client-state digest/S256 challenge, Google-state/nonce/browser-binding digests, optional account/generation, create/expiry/one-use status; recoverable outer state only RAM. |
| authorization_codes | Unique digest/version, account/generation/client/redirect/challenge, issue/expiry/consumed. |
| login_grants | Account/generation/client type, refresh generation, create/use/idle/absolute/termination; no device identity. |
| refresh_token_generations | Grant/generation unique, digest/version, issue/consume; retain consumed hashes while active grant. |
| relay_access_tokens | Unique digest/version, account/grant, issue/expiry; validate owning generation/grant, fixed permissions/audience. |
| portal_sessions | Digest/version, account/generation, Google-auth/create/use/idle/absolute times, CSRF digest. |
| audit_events | Enumerated event/outcome/reason/time, optional account/plan target, actor admin email/client type; no cascade erasing retained audit. |
| schema_migrations | Ordered revision/immutable checksum/application time, atomic with migration. |

Finite UTC timestamptz, nonnegative bigint counts/revisions, explicit enums/bounds/
digest sizes/reference/uniqueness/lifecycle constraints. Index live identity,
credentials, account children, status/plan pagination, expiry/cleanup, account-week
and bounded audit reads; FK referencing indexes are explicit. No generic JSON
bags/request blobs/unneeded email indexes, durable sessions/Peer IDs/reservations/
Rendezvous, per-buffer or per-allocation history. Recovery adds typed bounded
deletion-operation/run/source/target/step markers, review holds and reset notices,
not a general event store. Consistent profile snapshot excludes live count sampling.

### I6. Privacy, deletion and retention

Mutation and audit commit together: creation, state/quota/plan changes, account-wide
revocation, replay revocation, deletion. Audit failure fails mutation. Allowed
fields: time, enumerated event/outcome/reason, optional random account/plan target,
normalized acting admin email and registered client type. Admin-only reads,
append-only application writes, no edit/delete API. No peer/IP/user-agent/token/
claims/body/free-form fields. Credential-failure audit only for resolved account/
registered client with blocked status, grant cap or refresh reuse. Unknown input/
scanning/routine success uses redacted aggregate diagnostics, not durable history.

| Data | Retention |
| --- | --- |
| Ended credentials, redeemed/expired flows/codes, tokens/sessions/grants | Purge within 24h; consumed refresh hashes remain while owning grant active. |
| Completed self-deletion | Raw identity/presentation detach at finalization; hidden old account/dependencies purged within 24h. |
| Retained Quota Usage | Current week only; expires at week end. |
| Account-linked quota history | Current plus 12 completed weeks. |
| Abandoned Pending | Delete after 90d without successful Google portal login; pending_expired audit, no retained identity/deletion record. |
| Active/Suspended/Denied | No inactivity expiry. |
| Audit | 180d; deleted random account ID may remain but no identity/new-account mapping. |
| Restricted infrastructure diagnostics | Maximum 7d including exports. |

Self-delete permitted in all states after recent Google auth; admins do not delete
others. Immediately revoke and follow journal-proven ordering in I10. Completed
deletion frees identity for fresh Pending registration, even after Denied. Only
current-week usage follows via keyed identity digest, imported/deleted atomically
exactly once. New plan sets allowance; no old state/plan/overrides/credentials/
email/older usage/audit links return. Duplicate logical retained records fail
closed. Pending deletion fences re-registration until identity detached.

One bounded indexed cleanup worker catches up at startup/periodically with normal
ordering; child batches then hidden parent, no unbounded cascade. Expiry applies
at authorization/read time regardless of purge lag; report breaches, never restart
retention clocks. Online 24h erasure excludes old encrypted backups governed by
I10; normal serving cannot query backups.

Application structured logs go to stdout, only ephemeral request/session IDs and
safe outcomes, no stable account/External Identity/email/peer/public address/
credential/content. Metrics use bounded enums, no arbitrary URLs/errors or
identifying labels. Durable accountability is restricted audit only.

Infrastructure ingress/DB diagnostics may include IPs, DB IDs/error details under
operator-only access and 7d retention. Disable query/body, credential/header and
SQL statement/parameter logging; suppress credential-bearing route errors at
source, test canaries. Credential leakage is a defect, not an exception. Do not
promise arbitrary upstream text sanitized or require a custom log supervisor/
isolated ingress. Scoped F5 logging/template control is an operator prerequisite,
not permission to enable arbitrary snippets.

### I7. Clipp runtime integration

Configuration strict union: exact canonical HTTPS Discovery URL, or nonempty
complete multiaddrs for one persistent unauthenticated Peer ID. Local key/name
only; separate credentials exact-endpoint/audience scoped. Accept self-hosted
HTTPS/show host. Reject duplicate canonical URLs and duplicate unauthenticated
Peer IDs. Different discovery URLs naming same Peer ID preserve existing
connection and flag conflict, never share credentials/change account.

Metadata edits in-place. Changed discovery URL or unauthenticated Peer ID replaces
entry without credentials/retry state; same-peer explicit addresses can update.
Remove closes owned resources and erases local credentials, not account-wide
revocation; abandoned grants persist until expiry or explicit account revocation.

Keep one host/existing Device Identity per runtime. Shared core owns independent
discovery→dial/verify→auth→reservation→Rendezvous, renewal and retry; runtime adapters
own browser/HTTP/lifecycle/storage. No automatic circuit-relay listeners bypassing
barrier. Dynamic config does not restart host/direct peers/other relays. The user
amendment of 2026-10-05 requires one effective connection per supported available
transport family/config: Electron TCP/WSS/WebRTC Direct; Android/extension
WSS/WebRTC Direct. Use bounded dialing of address alternatives within each family;
keep successful families connected and retry failed families independently. Verify
the expected Peer ID before sending credentials on every physical connection.
One coherent stock reservation/Rendezvous owner serves the configured peer, with
promotion to a healthy authenticated transport when needed. Single-transport
acceptance selection remains opt-in to prove the specific transfer path. Direct
networking starts even without relays.

- Electron main owns auth/renewal/browser and OS-backed safeStorage. No usable
  OS provider (including insecure basic_text) means memory-only. Renderer gets
  no token strings.
- Android native Keystore encrypted app-private credentials, excluded from cloud
  backup/transfer; no plaintext Preferences/localStorage fallback. Activity/WebView
  owns network; foreground service does not reconstruct host after process loss,
  reopening app resumes it.
- Extension background owns configs/PKCE/browser/renewable storage. Set
  chrome.storage.local TRUSTED_CONTEXTS before access. Offscreen owns libp2p/shared
  controller, receives only short Access Tokens via narrow port. Recreation does
  fresh discovery/auth; popup/options/content scripts get no refresh token.
  Browser storage is not an OS-keychain encryption guarantee.

Persist only config/renewable credentials (including explicit unauthenticated
Peer ID). Managed discovered Peer IDs, Access Tokens, PKCE, documents/connections/
reservations/leases are transient. Erase unreadable credentials and require login.
Single-flight refresh/config serves all consumers. Successful rotation save
failure can retain new credential in RAM with restart warning, never replay old
token or store plaintext. Only explicit user action starts interactive browser.

Ready requires auth+reservation+Rendezvous. Rendezvous-only loss is degraded,
preserving circuits while repairing. Known quota/session refusal preserves valid
login and slow-retries; ambiguous stock statuses are not labeled quota. Invalid
renewable credential needs user login; blocked state never launches browser loops.
Manage relay account opens portal, possibly another browser account; only portal
confirms committed revocation. Opening it neither erases credentials nor proves
success. Access Tokens gain no account-management scope.

New installs/upgrades first adopting model start empty, no built-ins/legacy
import. Later upgrades retain new-model config; identity/other data unchanged.
Signed Peer Record remains intact; skip unconfigured relay routes, try direct/
eligible routes, never auto-configure/login. Existing Device Identity key storage
and history protocol are not changed by relay credential requirements.

### I8. Process, publication and drain

Starting → Serving → Draining → Stopped. Every start generates Ed25519 libp2p
key/WebRTC certificate from OS CSPRNG in RAM only. Sessions/reservations/leases/
circuit count restart empty; no identity tombstone/epoch/registration protocol.

One stable logical relay-0 slot, singleton StatefulSet and ordered nonoverlap.
Use consistent generated physical names. Old process fully stops before new;
release renaming is no bypass. Uncertain node/process needs fencing, not forced
Pod deletion as proof. Planned downtime accepted.

WSS/public ports configured; namespace read-only named-Service watches supply
TCP/UDP addresses. Per-transport explicit overrides replace watch dependency.
Publish only complete enabled listener/address set with current Peer ID/certhash,
all valid observed addresses deduplicated/sorted internally. Disabled transports
omitted; change republishes atomically, incompleteness withdraws entire document.
Last complete snapshot usable only through staleness deadline, successful API
resync verifies even unchanged state. validUntil=min(now+TTL, staleness deadline).
After expiry withdraw readiness/discovery until refreshed.

Readiness is local listeners/routing/publication/not-draining, not DB/capacity/
public self-dial. Publication loss remains Serving/lively. External checks test
Internet reachability. Discovery reserves no slot; immediate capacity transitions,
no queue/hysteresis. Only active-session ceiling yields global discovery 429;
other gates act at their operations, not global withdrawal.

First SIGTERM/SIGINT drains: unready/discovery withdrawn, reject new auth including
renewal, reservation create/renew, HOP connect and RV register. Existing circuits
continue; active sessions may Ping/lookup/unregister. Stock tracer drives atomic
active-circuit count, no identity/account circuit map. Zero count closes remaining
sessions and exits. Second signal or 30s forces; K8s grace 45s. Provider backend
removal may interrupt sooner. Operations stays until final shutdown, readiness
503 during drain. Private unauthenticated listener only livez/readyz/metrics;
health plain 200 ok or 503 unavailable, bounded local state, no synchronous DB/
provider checks. Startup uses readyz; no extra debug/startup endpoint.

### I9. Helm, PostgreSQL and Argo CD

Deliver chart, documented/schema-validated values, complete external/bundled
examples and pinned-Git-revision Argo Application. Own workloads, Services/Ingress,
narrow RBAC/configuration; consume existing namespace/Secrets/F5 NGINX/issuers/
OCI network inputs. Optional Certificates reference existing issuers. Do not
provision tenancy, cluster, IAM, DNS, registry/controllers or secret infrastructure.

One versioned non-secret config with Secret-file references; reject unknown,
missing and conflicting fields at render where possible and startup. CLI selects
operation/location, not another policy precedence. Values groups cover phase/run/
schema/stable resource identity; pinned image; canonical portal origin and distinct
WSS hostname; unprivileged non-conflicting listeners, transports/public ports/
overrides; explicit DB mode/endpoints/name/trust/roles/bundled storage; watcher;
recovery repositories/identities/runs; Google/admin/peppers; exact public clients;
F5/OCI routing; enforcing NetworkPolicy inputs; resources/scheduling; operations.

| Surface | Deployment/access |
| --- | --- |
| Relay | StatefulSet one only while serving, zero in maintenance; headless Service as needed, no PVC/HPA/blocking PDB. |
| Portal/discovery/Google callback | Exact HTTPS host through existing F5 Ingress to dedicated ClusterIP backend. |
| WSS | Distinct exact hostname, backend port/Service, TLS termination then internal WebSocket; no account/portal routes. |
| TCP / WebRTC Direct UDP | Separate OCI NLB Services with direct Pod backends, no NodePorts, NSG rules and explicitly disabled instant failover. Private readyz JSON HTTP health check. |
| Operations | Separate private listener, no public Ingress/NLB frontend port; verified kubelet/provider checks and approved scraping only. |
| Bundled PostgreSQL | Separate single-instance StatefulSet, internal Service and persistent claim; remains during relay maintenance, no HA/operator/subchart requirement. |
| Reload watcher | Independently pinned non-root database sidecar, public certificate/trust and restricted reload credential only. |
| Backup scheduler | Same database Pod, distinct non-root UID, same pinned pgBackRest, read-only PGDATA and bounded scratch/locks. |
| Maintenance | Explicit bootstrap/migration/restore/cleanup work with only relevant authority; no hidden serving scaling. |

Two exact names need no wildcard DNS/cert. Operator owns DNS/public TLS Secret
or existing HTTP-01-capable issuer. TCP/UDP learned addresses need no extra client
configuration. Build URLs/callbacks/Origin checks from configured origin, never
arbitrary Host/Forwarded; trust proxy metadata only from explicit ingress paths.
Additional proxies need explicit settings. Verify actual F5 controls, not community
ingress-nginx annotations. Provisioning must not create a publication/backend
readiness cycle; Ready does not prove public reachability. Preserve network-slot
resources across maintenance; application still refuses new work via stale routes.

Non-root, read-only root filesystem, no privilege escalation, dropped capabilities,
runtime-default seccomp, bounded disk scratch. No host ports/network/mounts,
privilege/root repair/automatic force-attachment. ARM64 example; configurable
selectors/tolerations, prefer separate eligible relay/DB nodes without mandatory
anti-affinity blocking scheduling. Verify real allocatable capacity. Immutable
Linux ARM64/AMD64 relay images, same intended digest for migration; Go-only runtime,
no floating latest/startup build. DB/watcher pins independent of relay upgrades.

Require actual enforcing default-deny ingress/egress. Explicit sources/CIDRs:
public data ports, ingress-only portal/WSS, approved health/scrape operations,
serving/authorized maintenance to DB. Egress only required DNS, verified DB, API,
public HTTPS and relevant storage HTTPS. Replies to inbound transport permitted,
not arbitrary new peer dials. HTTPS allowance is not a Google-domain firewall;
HTTP clients separately restrict endpoints/redirects. Verify NAT/host-network
behavior and all transports before release; no silent broader egress or installed
cluster-wide network engine.

Existing Secrets, least-privilege read-only file/key mounts, no Helm lookup,
generated persistent passwords/peppers, secret checksums/annotations or Secret-read
API authority. Normal projected volumes, not subPath for live files. Suggested
configurable keys: Google client-id/client-secret; admin allowlist.json with
applied revision; pepper keyring.json; separate username/password for serving,
migration, bootstrap and reload; TLS tls.crt/tls.key and CA. Separate backup/journal/
cleanup identities. DB alone gets server private key. Serving never receives
bootstrap/migration/cleanup credentials or bulk-backup read/delete authority.

Live-reload admin allowlist, check every request. Empty grants nobody; observed
malformed/unreadable disables administration, not stale permission retention.
Expose non-sensitive applied revision; eventual projection is not immediate
removal. Urgent removal needs verified revision or shutdown. Other settings use
controlled restart, non-reused versioned Secrets and non-secret config checksum;
verify selected material before serving. Plan edits remain live DB operations.

Require explicit external/bundled DB mode, reject conflict and fallback. External
17/18 initially; bundled tested PostgreSQL 18.x Debian image derived from official
base with pgBackRest/libraries installed at build. Preserve the accepted upstream
PostgreSQL-18 volume/data-directory layout recorded in Further Notes. Server
major differs from app schema; deliberate minor verification, separate major
upgrade, no silent chart-triggered upgrade.

All network DB clients use full chain/hostname TLS verification and SCRAM; server
rejects plaintext, encryption-only require is insufficient. Bundled certificate
matches internal Service/cluster-domain hostname and private issuer, not portal
certificate. No mandatory mTLS. Sole exception is the explicitly permissioned
same-Pod backup Unix socket with restricted SQL authentication.

Either new configurable 10GiB claim or referenced existing claim, never both.
Production Longhorn class operator-managed, expandable/Retain; protect created
PVC from Argo prune/application deletion, don't adopt existing claim. No automatic
shrink/replace/delete or empty fallback. Direct PVC/namespace deletion requires
operator protection. Replicas/snapshots consume additional space and are not backup.
Guard expected data/major markers before official entrypoint. Initialization
only explicit first-install while relay stopped, cleared before first serving;
missing/empty/wrong expected data after use fails closed. Bootstrap never recreates
lost data, overwrites conflicting roles or silently resets passwords.

Explicit resumable bootstrap creates/checks DB and serving/migration/reload/backup
roles before migration. Privileged credential only initialization/bootstrap and
authorized operator work; serving no DDL. Reload role gets connection and EXECUTE
pg_reload_conf, no table/schema-creation/role/superuser rights, including PUBLIC/
default privilege hygiene. Watcher/backup wait for role provisioning without
blocking initial DB readiness. Backup SQL is restricted but OS user reads physical
DB; prove no PGDATA writes/superuser login/bootstrap-secret access, preserve group
readability across boots without root repair.

Password rotation: planned stop of clients, authenticated role change, fresh
verified TLS check, select replacement Secret and restart. Partial failure stays
stopped; compromise also terminates old DB sessions. Same-CA leaf renewal observes
complete files, reloads then proves expected served certificate by fresh handshake.
Watcher needs no key/root/shared PID/API write; failure never restarts DB or
disables TLS. Expiry recovery uses operator local reload. CA rotation is staged
maintenance with trust coordination and verified removal of old trust.

| Desired phase | Required gate |
| --- | --- |
| stopped | Relay zero, preserve routing/bundled DB, verify termination/fence uncertain node; first-init permission only here. |
| migrating | Relay zero, read-only verifier checks actual old Pod absence, API denial/unavailability fails; bootstrap then migrate intended target. |
| serving | One relay only after intended image/target/schema/run checks; initialization false; role/watcher/config and applicable recovery/monitoring prerequisites verified. |

Each build expects exact schema revision. Explicit ordered checksummed
transaction-compatible migrations, revision marker atomic with change, runners
serialized on dedicated connection. No automatic serving migration/concurrent
schema change/down migration. Same-schema upgrade nonoverlap; changed schema
stop/migrate/serve. Rollback only to compatible image, else forward fix/recovery.

Ordinary run-specific Jobs, not generic PreSync. Explicit run ID and bounded
target/config identity in names; changed input requires new run. Current success
must match image/database/schema, not cached Healthy/old successful Job. Jobs
never secretly scale, restartPolicy Never, no failed automatic retry, bounded
deadlines, retained failure evidence/no TTL erasure. Diagnose/reconcile before
new run; prune reviewed old Jobs last.

Argo: existing project/namespace, pinned Git revision/chart/selected values,
manual full sync, PruneLast=true, no automatic self-heal/Force/Replace or selective
maintenance sync. Explicitly confirm routing/DB workload deletion; preserve PVCs/
operator resources. Uninstall or release-name/database-mode change first verifies
old shutdown and deliberate target/data handoff. Mode switch is validated
transfer/restore, not empty-DB toggle or merge. Preflight is non-mutating, no
Secret printing, distinguishes static rendering/existing environment/live checks;
missing inputs mean operator tasks, never relaxed TLS or infrastructure mutation.

### I10. Recovery and independent deletion evidence

RPO target 15m from **proven recoverable off-cluster state** including archive lag;
RTO four hours through safe recovery including required account review. Report
portal availability, ready accounts and outstanding holds separately. Scenario
is DB-volume/cluster loss with OCI region, repositories/operator access surviving,
not region loss or fully privileged compromise. Missing evidence keeps recovery
closed even if time target missed.

Bundled pgBackRest: daily full plus continuous WAL, 60s forced segment switch,
target seven days usable PITR once established. Advertise actual recoverable
range with base and uninterrupted WAL, not scheduler success. External managed
PITR may be equivalent but needs verified recovery and the application journal.
Thirty days is maximum restore eligibility and cleanup-request deadline across
required copies/manifests/versions/incomplete uploads/journal/bundles, not instant
provider erasure. Monitor/retry overdue cleanup, expired sources never regain
eligibility; no perpetual archive. Infrastructure log ceiling remains seven days.

Operator off-cluster buckets use verified HTTPS and OCI encryption with Oracle-
managed keys. No mandatory extra client-side cipher/KMS or locked retention on
mutable metadata. Encrypted operator recovery bundle outside failed cluster holds
needed peppers/current access/trust/config/image/schema references, never raw
secrets in Git/reports/logs. Keep required live/eligible-recovery keys; historic
bundle never restores old admin/provider authority. PVC snapshots/Velero/logical
dumps supplement, not replace, independent DB-aware recovery and journal.

DB main process invokes local archive helper in derived image. Same-Pod distinct
backup UID scheduler, same pgBackRest build, read-only PGDATA, bounded scratch/
shared lock and restricted backup-control socket. No second-node live-volume
attachment, SSH/exec authority, shared PID or root repair. Dedicated OCI S3 Customer
Secret Key for archive/backup; separate expiry credentials/no automatic writer
expiry. Live journal uses native OCI API-signing identity, immutable-event create/
read and narrowly conditional head replacement; no event overwrite/delete or
backup access. Separate cleanup/restore authority. Buckets/IAM/bundle remain
operator-provided; no enhanced-OKE workload/node-wide identity prerequisite.

Journal independent of DB rollback contains only old random account ID, stable
event ID, sequence/hash/format and necessary times, never identity/email/digest/
Peer ID/token/quota ledger. Conditional head/checkpoint defines committed sequence
and retained coverage. Repository identity/initial floor are explicit setup;
unexpected empty/wrong repository is not auto-initialized.

Deletion ordering:

1. Admit bounded pending work before irreversible start; under identity/account
   guards durably record stable operation, revoke/fence access and competing
   mutations, close sessions within ten seconds. Re-registration waits.
2. Release DB locks, append stable event and conditionally commit/verify head.
   Ambiguous retry compares exact existing content and committed membership;
   upload alone is not proof. Serialize appends, compare maintenance generations.
3. After proof, atomically detach identity/presentation, create Retained Quota
   Usage, invalidate credentials and append audit. Resume same operation after
   crash; committed intent irreversible.
4. Only then report success and begin 24h purge clock. Pending uncertainty is
   incomplete/retrying and fenced, never abandoned/expired to free capacity.
   Unrelated accounts and account-wide revocation do not wait on journal health.

Cleanup conditionally enters journal maintenance generation, pauses new deletion
commits but not unrelated traffic, validates monotonically advancing recovery
cutoff and freezes exact deletion targets. Never prune evidence needed for an
eligible restore; complete list pages. Resume interrupted work with generation
checks; timeout alone proves neither worker termination nor safe fence release.
Late requests cannot broaden targets or make expired recovery eligible again.
Backup expiry chain-aware, never drop required/unarchived WAL for pressure.

Explicit run-specific offline restore:

1. Validate current operator authority/source eligibility/repository identity and
   fence old serving/append/cleanup writers; uncertain node requires real fencing.
2. Restore separate isolated DB/volume matching physical major/metadata; forward
   migrate to supported app, no down migration/public obsolete image.
3. Verify journal identity/head/floor and every required event/hash through committed
   sequence, all pages. Empty list/checksum of remaining objects cannot prove
   missing acknowledged deletion absent. Reconcile pending/committed intents
   by old random account ID, never new account with same External Identity.
4. Invalidate all restored Portal Sessions, grants, refresh/access and unfinished
   flows; retain necessary usage peppers. Preserve restrictions and install
   separate Recovery Review Holds.
5. Preserve demonstrably recoverable current-week consumption/history; zero
   unrecoverable current-week amounts, including matching retained usage. Unknown
   affected set means restored scope unrecoverable. Never reimport classified-
   unrecoverable usage or move old week into new. This recorded/idempotent extra
   allowance is restore-only, never normal refund/error/missing-key fallback.
6. Apply actual-current expiry/retention and current admin/provider/DB/trust.
   Record immutable run/source/target/schema/journal step completion. Retry
   cannot reset quota twice after reopening; changed target needs new run.
7. Privacy-safe report and new verified baseline before deliberate cutover. Only
   structurally complete run opens portal; freshly authenticated admins explicitly
   review each restored account status/assignment before clearing hold. Visit/
   quota edit cannot clear it. Reviewed accounts need not await all others;
   new registrations follow Pending approval.

Display current-week Usage reset during recovery after zeroing, not proof of no
traffic; mark historical incompleteness instead of invented totals. Journal does
not detect coherent rollback by privileged/compromised recovery authority. Normal
healthy restart does none of this.

Before first public serving require initialized verified journal, successful
off-cluster full backup and WAL-recovery check. Before schema change verify
suitable pre-change recovery point/journal coverage; after restore new baseline
before cutover. Ordinary restart needs no new full. Isolated restore exercise
before production, quarterly and after material backup/storage/key/schema changes.

### I11. Accepted numerical operating profile

These are provisional **selected defaults**, not measurements or client options.
Operational settings follow admin-controlled restart policy; plans/overrides
remain live DB policy and credential ceilings non-extendable. Stock constants
are not advertised as exported configuration. Intersecting counts/memory/rates/
OS budgets neither promise simultaneous saturation nor reserve a control lane
or fairness under attack. No auto-tuning/hidden host-scaled defaults.

#### Relay, client and HTTP

| Control | Default / required interpretation |
| --- | --- |
| Global sessions | 6,000; immediate admission, no queue |
| Secured pre-auth | 256 connections; complete initial auth within 10s of security establishment |
| Session/renewal | Max 15m capped by token; renewal at server-jittered 75–85% admitted lifetime |
| Reservations | 6,000 globally, 6,000 per observed IP, 6,000 per recognized IPv6 ASN; no tighter shared-proxy bucket |
| Reservation lifetime / circuits per peer | 30m /16 simultaneous touching a Peer ID; circuit counts at both endpoints |
| Whole circuit data/duration | 2MiB (2,097,152 bytes) each direction /120s, not per application stream/message |
| Discovery / address dial / auth / client reserve / RV | 10s /20s /10s /15s /12s whole-operation bounds; earlier initial-auth deadline wins; client cancel/reset |
| Stock server constants retained | Relay control/STOP handshake 60s, STOP connect 30s; stock TCP upgrade; WebRTC Direct 10s/128 in-flight setup |
| WebSocket negotiation | 10s supported timeout; no ordinary portal write/idle deadline after upgrade |
| Discovery TTL / snapshot staleness / API verification | 60s /5m /successful bounded resync at least every 60s even without changes |
| RV TTL / managed refresh | 5m /60s ±20%, earlier for actual session/reservation/record expiry; more frequent legacy refresh compatible |
| Control payloads | Auth JSON 4KiB, discovery JSON 16KiB, raw signed envelope 16KiB, RVv2 JSON 32KiB, full RVv1 JSON 128KiB |
| Transient retry | Ceilings 1,2,4,8,16,30s with uniform half-to-full jitter; reset only after complete Ready |
| Retry hints | Minimum wait, may exceed cap; default overload 5s, manual cannot bypass |
| Known quota/session refusal | 5m ±20% or longer hint; preserve credentials, no browser launch |
| Client setup concurrency | 4 configurations, 2 active address dials/config across transport families; waiting backoff uses no active setup slot |
| Drain / termination grace | 30s /45s |
| Public backend HTTP connections / idle | 512 /60s; excludes WSS and shared ingress front sockets |
| Active public HTTP requests | 128 including multiplexed work; no unbounded queue |
| Header / target / body | 16KiB /8KiB /16KiB; no uploads or compressed request bodies |
| Header / body / ordinary handler-write deadline | 5s /10s /15s, shortened by tighter route bound |
| Dynamic pagination/output | Stable cursors, 50 rows default /100 max, 1MiB encoded response; bounded query/serialization, smaller complete page not truncation; oversized single record explicit error |
| HTTP global / resolved account rate | 200/s burst 400 /10/s burst 20 |
| Relay Auth global / secured connection rate | 200/s burst 400 /1/s burst 2 |
| RV global / authenticated connection rate | 500/s burst 1,000 /4/s burst 8 |
| Google workflows/provider I/O | 8 concurrent, 8s combined within remaining parent 15s HTTP deadline |
| Provider response bounds | Headers 16KiB, decoded body 256KiB including decompression |
| Verification key limits | 32 keys, 256-byte key ID, no growing attacker-keyed negative cache |
| Key freshness | Respect shorter provider cache lifetime, local max 6h; no expired fallback; still-fresh matching key valid after failed fetch, failure never extends freshness |
| Unknown key refresh | Deduplicated bounded fetch with shared 60s cooldown, initial empty-cache fetch excepted |
| Login RAM bounds | 1,024 continuations ×≤16KiB payload, 4,096 fences; existing 10m lifetime |

No IP buckets or unbounded attacker-chosen account-key maps: rate state is global
or bounded resolved account/live connection. These are control rates, not bytes.
Acquire provider capacity before claiming one-use flow where possible; no queued
provider work or unverified-claim fallback. Key fetch shares provider budget;
configured endpoints only, no arbitrary redirects. Unknown-key cooldown can
require fresh explicit login. No automatic retry of ambiguous one-use exchange.

Disable ordinary connection-manager trimming/ForceTrim. Use explicit admission,
expiry/revocation/quota/replacement/drain, no valid-idle eviction for newcomer.
Explicitly disable independent RM IP/subnet concurrency/rate restrictions through
supported nonrestrictive settings. No allowlist capacity bypass.

#### Complete Resource Manager scopes

C/I/O means combined/inbound/outbound. Every zero **blocks**, never inherits or
means unlimited. Set all eight fields explicitly from an empty fixed base;
avoid autoscaling and hidden built-in service/protocol overrides. Verify resolved
profile before construction. Connection scopes do not aggregate all streams.
Use block-all representations, not Go zero-as-default. Empty allowlist and
explicit non-nil empty subnet/rate configuration prevent implicit IP gates.
Custom handlers SetService and account for bounded parsing/serialization buffers;
assert exact enabled inventory, don't merely rely on unknown-protocol rejection.

| Scope | Connections C/I/O | Streams C/I/O | FD | Memory |
| --- | --- | --- | --- | --- |
| System | 8192/8192/512 | 32768/32768/16384 | 8192 | 512MiB |
| Transient pre-peer/pre-protocol | 256/256/256 | 1024/1024/512 | 256 | 512MiB |
| Default peer | 4/4/4 | 64/64/32 | 4 | 32MiB |
| Each connection | 1/1/1 | 0/0/0 | 1 | 8MiB |
| Each stream | 0/0/0 | 1/1/1 | 0 | 1MiB |
| Allowlisted system and transient, each | 0/0/0 | 0/0/0 | 0 | 0 |
| Unknown service/service-peer/protocol/protocol-peer, each | 0/0/0 | 0/0/0 | 0 | 0 |

All following service/protocol scopes and peer subscopes have connections 0/0/0
and FD 0. No identity-specific overrides.

| Service | Streams C/I/O | Memory | Service-peer streams C/I/O | Peer memory |
| --- | --- | --- | --- | --- |
| libp2p.relay/v2 | 24576/12288/12288 | 128MiB | 32/32/32 | 2MiB |
| libp2p.identify | 4096/2048/2048 | 64MiB | 8/4/4 | 1MiB |
| libp2p.ping | 1024/1024/512 | 16MiB | 4/4/2 | 256KiB |
| clipp.relay-auth | 512/512/0 | 8MiB | 8/8/0 | 256KiB |
| clipp.rendezvous | 2048/2048/0 | 64MiB | 8/8/0 | 2MiB |

| Protocol | Streams C/I/O | Memory | Protocol-peer streams C/I/O | Peer memory |
| --- | --- | --- | --- | --- |
| /libp2p/circuit/relay/0.2.0/hop | 12288/12288/0 | 64MiB | 32/32/0 | 1MiB |
| /libp2p/circuit/relay/0.2.0/stop | 12000/0/12000 | 64MiB | 32/0/32 | 1MiB |
| /ipfs/id/1.0.0 | 2048/1024/1024 | 32MiB | 4/2/2 | 512KiB |
| /ipfs/id/push/1.0.0 | 2048/1024/1024 | 32MiB | 4/2/2 | 512KiB |
| /ipfs/ping/1.0.0 | 1024/1024/512 | 16MiB | 4/4/2 | 256KiB |
| /clipp/relay-auth/1.0.0 | 512/512/0 | 8MiB | 8/8/0 | 256KiB |
| /clipp/rendezvous/1.0.0 | 1024/1024/0 | 32MiB | 4/4/0 | 1MiB |
| /clipp/rendezvous/2.0.0 | 2048/2048/0 | 64MiB | 8/8/0 | 2MiB |

Go soft memory 1,536MiB within 2GiB container, overlapping RM 512MiB accounting,
not additive allocation or all native/kernel memory. RM FDs 8,192, verified process
soft nofile ≥16,384; inadequate envelope fails preflight, not silent lowering.
Counts imply neither 12,000 simultaneous circuits under memory pressure nor
guaranteed headroom for control work; pending CONNECT also consumes HOP capacity.

#### Database, clocks, cleanup and health

| Control | Default / semantics |
| --- | --- |
| Runtime DB pool/work | Max 8 connections incl cleanup, max 64 active+waiting units admitted before logical guards |
| Pool acquisition / ordinary unit | 500ms /3s including guards/acquire/SQL/commit; parent deadline wins |
| Runtime SQL statement/lock/idle-in-txn | 2s /250ms /5s; not imposed on backup/bootstrap/migration |
| Pool min / idle / lifetime / jitter | 0 /5m /30m /uniform +0–5m |
| Pool health / new connect | Every 60s /3s parent-capped; no extra pool or killing borrowed txn when lifetime expires |
| Bundled PG connections | 32 including 3 superuser-reserved, no additional reserved slots |
| Planned SQL slots | Runtime 8, repository/journal maintenance 1, watcher 1, backup ≤2, probe 1, bootstrap-or-migration 1, operator/emergency 3 =17, 15 unassigned; serialize single-client categories |
| PG memory | shared_buffers 128MB, work_mem 2MB, hash_mem_multiplier 2, maintenance_work_mem 32MB, autovacuum_work_mem 16MB ×≤2 workers |
| PG parallel/temp | No parallel query/maintenance workers; runtime temp 64MB/backend; retain vacuum/durability/synchronous commit |
| Application cleanup | One worker startup/every minute, ≤30s pass, ≤250 rows/transaction, ordinary DB deadlines/indexed fair batches |
| Cleanup age | Warn >1h, escalate 12h, required 24h purge breach recorded; no expiry grace |
| Clock observation | Startup/every 60s, request/response uncertainty interval; after-lock operation sample can refresh |
| Clock warning/closed | Interval not contained ±1s warns; not within ±5s, >1s uncertainty, or last usable sample >5m closes time-sensitive issuance/admission/allocation |
| Clock recovery | 3 valid ordinary-cadence samples, no deadline extension/credit revival/reset; no readiness/liveness dependency |
| Relay startup readyz | Every 5s, timeout 2s, 120 failures (~10m) |
| Relay readyz | Every 5s, timeout 2s, 1 failure/1 success |
| Relay livez | Every 10s, timeout 2s, 3 failures |
| Private HTTP | 32 connections, 16 active requests, ≤2 scrapes; health 1s, metrics 5s, headers 2s, idle 30s; metrics cannot take every health slot |
| DB probe | Final-server single loopback TCP with verify-full hostname/trust, not Service/init socket; inner 2s, Kubernetes 3s, every 5s; readiness 2 failures/1 success; startup 360 failures (~30m) |
| DB liveness/restore | No I/O-dependent liveness; explicit restore startup allowance within run deadline |
| Leaf watcher | Poll 30s, one operation, reload+fresh verify 10s; retry 5/15/30/60s, ±20% at cap; independently recheck served cert every 5m |
| Certificate incidents | Warn nonconverged 5m or expiry ≤7d; escalate ≤24h, expired immediate; explicitly adjust for short-lived certs |
| Offline jobs | Bootstrap 10m, migration 30m, restore/reconciliation 3h; reviewed new run may override deadline, not hidden resource increase |
| Bootstrap/migration SQL | Statement 5m, lock 5s, idle-in-txn 60s, maintenance 32MB, no parallel maintenance, temp 256MB/backend, parent deadline wins |

PG MB units above are binary. Per-query work may multiply; these are not RSS
proof. External DB supplies corresponding application/maintenance allowance
without chart editing global settings. Connection health object creation is not
startup connectivity/schema proof. Acceptance probe is not authenticated role/
schema/leaf-rollout validation; fresh pre-serving checks remain independent of
unprovisioned reload role. Relative clock comparison cannot detect shared wrong
UTC; infrastructure synchronization required. Existing sessions keep only confirmed
credit/deadlines through clock fault; unsafe week transition/regression fails
closed even below warning thresholds. Cancellation never proves rollback.

#### Recovery work and storage

| Control | Default / semantics |
| --- | --- |
| Full backup concurrency | 1 active +≤1 coalesced pending, manual/check through same scheduler |
| pgBackRest | process-max 1; buffer-size 1MiB; synchronous archive-push; db-timeout 30m, protocol-timeout 31m, io-timeout 60s, archive-timeout 120s |
| Scheduled backup / retry | Whole 2h /15m ±20%, no accumulating missed daily-run queue |
| Local archive attempt | 2m watchdog, fail on uncertainty, terminate/reap own children before overlap |
| Disk scratch | Backup 256MiB, archive 64MiB, shared locks 8MiB, watcher 8MiB, each maintenance Job 256MiB |
| Pending deletions | ≤1,024 durable, one append/reconciliation worker; refuse before irreversible/fenced admission, never evict accepted operation |
| Journal attempt/object | 20s /5s; short DB units, no locks during storage |
| Journal retry | 1/2/4/8/16/30/60s half-to-full jitter, fair revisit, manual cannot bypass |
| Journal payload | Encoded event 4KiB, head/checkpoint 16KiB, streamed bounded pages |
| Pending incidents | Warn 15m, escalate 1h or 80% capacity |
| Repository/journal cleanup | One nonoverlap pass/hour, 30m deadline, custom pages ≤250 objects; backup expiry chain-aware tool-driven |
| Repository cleanup incidents | Warn successful pass overdue 1h, escalate 6h, explicit retention breaches; timeout cannot release uncertain generation |
| Recovery/disk observation | Bounded every 60s, missing equivalent external/provider signal unknown not healthy |
| Recoverability lag | >5m warns; >15m or unprovable RPO breached |
| Full backup age | >26h warns; >48h escalates |
| Data filesystem | 80% warn, 90% escalate, 95% operator protective stop/drain, earlier for predicted exhaustion |

Stream backup/restore to repository/intended data volume, no full-DB scratch
stage. Exhaustion fails visibly, never privileges/WAL deletion/automatic growth/
DB kill/quota reset/auto-restore. Preserve archival/recovery through protective
maintenance. A killed client/timeout proves neither stopped server work nor
safe fence release. Scratch controls do not synchronously cap every write.

| Container | CPU request/limit | Memory request/limit | Ephemeral storage request/limit |
| --- | --- | --- | --- |
| Relay | 1/2 cores | 1/2GiB | 64/128MiB |
| PostgreSQL + local archive helper | 400m/750m | 384/768MiB | 128/256MiB |
| Backup scheduler | 90m/225m | 96/192MiB | 256/512MiB |
| Reload watcher | 10m/25m | 32/64MiB | 16/32MiB |
| Each maintenance worker | 100m/500m | 128/256MiB | 256/512MiB |

DB Pod total 500m/1 CPU, 512MiB/1GiB, ephemeral 400/800MiB; scratch caps 336MiB.
Sibling slack cannot increase container cap; all archive/backup/watcher work is
inside envelope, not free resources. Logs/writable layers/shared emptyDirs need
actual node accounting/rotation checks. These are scheduling/eviction controls,
not synchronous hard disk caps. PVC data/WAL and off-cluster repositories separate;
no relay disk queue/log files.

### I12. Observability and capacity qualifications

Private aggregate metrics and redacted stdout transitions cover lifecycle,
discovery, connections/pending handshakes/sessions/reservations/circuits/streams,
capacity/denial/auth outcomes, charged/unattributed bytes, allocation/stranding,
retries, DB/provider failure/latency, restarts/drain, memory/CPU/storage, certificate,
cleanup/pending deletion and proven backup/recovery freshness. Bounded enum labels
and diagnostic queues only, no account/peer series, arbitrary URLs/errors/content.
No Prometheus requirement.

Before public serving require operator-owned monitoring and tested notification
destination outside process, detecting critical conditions and lost observations/
availability. Deliver an already-detected critical condition within five minutes,
route warnings. Logs alone suffice for development only. No built-in email/
webhook/paging or automatically created monitoring job.

Known capacity facts stay visible: inspected stock WebRTC Direct reserves
2,631,680 bytes/connection at medium RM priority; under 512MiB at most 121 such
reservations fit before other use. This is not RSS/practical-capacity measurement.
Yamux stream windows also consume peer/system memory. Count ceilings cannot all
saturate under accepted budgets. Keep limits and 5,000-session goal now; no forced
transport restrictions, dishonest undercounting or silent budget increase. Prove
representative enforced Linux ARM64 mix after code before claiming full goal.

2MiB/direction and 120s can cut off multi-frame history; reconnect may replay
prefix because existing protocol has no resume cursor. Fitting one maximum clip
does not prove arbitrary history completion. Verify/report without silent loss,
duplicates or uncontrolled retry, and without hidden cutoff/protocol changes.

## Testing Decisions

### User-confirmed seams and prior art

1. Primary: running relay's HTTP/libp2p interfaces with real PostgreSQL, observing
   responses, refused/successful circuits, expiry, portal state and authorized
   durable effects.
2. Focused: complete Account, Quota and Relay interfaces for deterministic clocks,
   concurrency and commit-fault ordering difficult to force through a browser.
   Keep invariants inside operations. Real supported PostgreSQL, no SQLite or
   mocked-SQL correctness claim, no generic repository-per-table abstraction.
3. Client: Clipp shared networking/controller interfaces and existing runtime
   conformance harness, then actual Electron/Android/extension execution. Adapter
   doubles test orchestration, not native storage, real Google or interoperability.

The user confirmed these seams on 2026-09-27. Good tests assert externally
observable behavior, safety invariants and deadlines, not private helper calls
or internal layout. Reuse existing network/Rendezvous test patterns, shared
runtime history conformance and extension bridge tests. Preserve history
idempotency/no-current-clipboard mutation. Mocked network tests and throwaway
real relay-auth probe inform work, not production acceptance. Add no general
mock interface solely to test an internal helper.

### Required behavior and fault coverage

- Force actual relay traffic and each supported transport: Electron TCP/WSS/
  WebRTC Direct, Android/extension WSS/WebRTC Direct. Cover same/cross runtime,
  both directions, cross-account, RVv1/v2, multiple configs and unauthenticated
  mode. Direct/fallback success must not conceal broken path. Record exact OS/
  runtime versions, not all-platform claims from one machine.
- Exact-connection HOP/RV gate; mismatch before token; full-capacity/cross-account
  replacement, renewal failures, stale callbacks, same-connection account change;
  framing/bounds/redirect/downgrade/version/error/fallback behavior.
- Deterministic hostile Google doubles: signature/issuer/audience/expiry/nonce/
  state, callback/PKCE/replay, key cache/rotation/cooldown/oversize/outage and
  ambiguous exchange. Real Google test-account flows in all runtimes before
  first serving and after material authorization changes.
- Canonical identity/concurrent registration; all status transitions; authoritative
  admin email/current allowlist/malformed reload/recent auth/revision conflicts;
  Origin/CSRF/logouts. Assert no individual-grant surface or durable device binding.
- Code/refresh races/replay/cap/lifetimes, old-token validity, account generations/
  unknown-identity fences/overflow, process/browser loss, all storage boundaries.
  Synthetic secret canaries across app/infrastructure logs, metrics, callbacks
  and unauthorized retained storage.
- Real PostgreSQL 17/18 and both modes: known rollback, ambiguous commit, lost
  allocation response, late receipts, cancellation/crash/restart, Monday-crossing
  waits, clock faults and outage. No duplicate install/unconfirmed spend/ordinary
  refund; both-end charging, all-session cutoff and honest attribution caveats.
- Quota reductions below/equal committed, zero limits, session reductions, repeated
  delete/register and exactly-once retained import, missing/rotating peppers,
  bounded indexed cleanup and immediate validity despite purge lag. Schema checks
  enforce typed privacy allowlist/no raw credentials, not replace safe SQL/logging.
- Journal event/head/DB ambiguity and every crash boundary, missing/misordered
  evidence, pagination, pending cap/fairness, interrupted prune/late workers.
  Restore credentials invalid, deleted accounts absent, restrictions/review holds
  preserved, only explicit once-per-run quota reset, current authority/baseline
  enforced. Missing evidence never means healthy or safe empty history.
- Exact-schema startup/checksums/runner contention/transaction rollback; fresh,
  failed/retried target-specific Jobs and fencing. Helm lint/render/schema across
  both DB modes/all phases and independent current-target startup validation.
- Isolated authorized OCI: all public transports externally, private operations,
  F5 logging/proxy behavior, enforcing default-deny/NAT, publication ordering,
  replacement/drain, empty/wrong PVC failure, Longhorn permissions/retention,
  leaf/CA/password rotation, watcher/backup bootstrap cycle, sidecar denied
  PGDATA write/superuser login. No production fault injection implied.
- Timed real off-cluster backup/WAL/journal restore, missing keys/WAL/events,
  baseline and retained-copy cleanup, actual RPO/RTO. Before production, quarterly
  and material recovery changes; measure complete reopening/review, not just DB.
- Each overload gate, slow reader, bounded rejection/recovery/retry, private-health
  isolation, bounded diagnostic queues; node log rotation/storage accounting,
  cleanup lag/disk protection and external notification/loss detection.

### Release evidence and gates

Applicable formatting, static/type, unit/contract/integration checks on each
change; Go race detection for concurrency-sensitive code and required Clipp
npm check plus applicable lint/build/runtime suites. Candidates also need E2E,
fault/operation suites and **executed** Linux ARM64/AMD64 image smoke/integration,
not cross-compilation alone. Release manifest records source/dependency locks,
immutable image digests, chart/schema, client/protocol versions, config and
evidence. Scan dependencies/images; reachable serious issues block release.
False-positive/irrelevant findings need rationale, owner/review date. Missing
devices/credentials/runners mean not run, never passed. Build success authorizes
no publishing/deployment.

Initial endurance: four hours Linux ARM64, twenty concurrent authenticated
sessions across multiple accounts within allowances, recorded TCP/WSS/WebRTC
mix, repeated renewals/reservations/circuits/bounded traffic plus normal portal/
DB/backup work. No unexpected crash/OOM/stuck renewal/integrity error/deadline
breach or accumulating unreleased live state; distinguish heap retention from
leaks. Separate isolated overload profiles record test-only overrides, not higher
production proof. Twenty is not a new cap/SLA/replacement scale goal.

Tested runbooks: install/upgrade, schema, credentials/CA, degraded dependencies,
pressure/failed Jobs/compromise, backup/restore/journal repair, account review,
uninstall. Each names signal, safe action, forbidden shortcuts and reopening
verification. Human promotes recorded candidate through manual Argo phases;
CI never authorizes production mutation. Known security/privacy/quota-integrity/
schema/recovery defects block. Cosmetic UI and later capacity work may remain
documented; other omissions/failures require explicit decision. Evidence reuse
requires candidate applicability; rerun affected protocol/dependency/image/schema/
chart/auth/backup checks after material change.

| Production evidence area | Current state |
| --- | --- |
| Real clients/transports/cross-runtime relay | Not run |
| Account/auth/admin/privacy/real Google | Not run |
| Quota/concurrency/crash/real PostgreSQL | Not run |
| Automated checks/ARM64+AMD64/release manifest | Not run |
| Helm/Argo/OCI/TLS/network/storage | Not run |
| Timed restore/off-cluster baseline/recovery integrity | Not run |
| Twenty-session/four-hour endurance and overload | Not run |
| Private bounded metrics/redacted stdout | Not run |
| Operator monitoring/notifications | Not run |
| Runbooks/human promotion | Not run |
| Eventual 5,000-session capacity claim | Not run; deferred until implementation |

## Out of Scope

- Multiple Relay Instances/separate Coordinator/pool/horizontal scaling,
  distributed leases or relay-registration protocol in v1.
- Persistent relay keys, durable device ownership/administration, same-account-only
  circuits or changes to Device Network trust.
- Forked/custom relay wire, exact payload/per-circuit accounting, endpoint
  deduplication, per-account circuit/reservation quotas, targeted circuit shutdown
  or durable traffic ledger.
- Initial IP throttling; billing/subscriptions/client-selected quotas; other IdPs,
  provider linking, Google continuous-security integration, durable grant proof keys.
- Individual grant listing/revocation, native admin app, in-app account revocation
  API or expanded Relay Access Token management authority.
- Legacy relay-setting import/built-in defaults, identity-key migration, automatic
  Android host resurrection or resumable history redesign.
- Content inspection/moderation, arbitrary debug/export surfaces, final UI polish
  or unmeasured platform/capacity guarantees.
- DB HA/operator, online schema migration, automatic down-migration/failover/
  restore/quota reset/PVC expansion or region-loss recovery guarantee.
- Provisioning OCI/cluster/network/IAM/DNS/issuers/registry/Secrets/monitoring or
  external DB infrastructure. Optional bundled DB is in scope.
- Prometheus installation, built-in paging, silent controller-wide logging changes
  or protection from fully privileged tenancy/database operators.
- Production implementation, release publication, implementation-ticket generation,
  cluster changes or executed acceptance in this consolidation task.

## Further Notes

### Authority and provenance

This is the consolidated build-facing specification, replacing the former
handoff-only index at this location by explicit request. Accepted history is
preserved; obsolete proposals and research recommendations are not alternative
requirements. A genuine discrepancy must be reported/corrected using the latest
explicit accepted revision, not silently resolved by inventing policy.

| Coverage | Accepted sources |
| --- | --- |
| Scope/language | [Wayfinder map](map.md), [Relay glossary](../../CONTEXT.md), [Clipp glossary](/Users/invine/src/js/clipp/CONTEXT.md) |
| I1/I5 topology/enforcement | [Topology](issues/06-define-distributed-quota-and-coordination-semantics.md), [auth prototype](issues/04-prototype-connection-scoped-relay-authentication.md), revised Q337 |
| I2/I3/I6 accounts/credentials/privacy | [Q137–186](issues/09-define-account-credential-lifecycle-and-retention.md), persistence/recovery amendments, Q212/Q275 |
| I2 portal | [Accepted rough draft](issues/08-prototype-account-and-administration-portal.md), Q217/Q221/Q230 |
| I4 protocols | [Q72–111](issues/05-define-relay-control-protocols.md), Q124/Q321 discovery refinements |
| I5/I6 persistence | [Contract](persistence-contract.md), [Q215–241](issues/14-define-go-modules-and-postgresql-persistence.md), journal-first deletion amendment |
| I7 clients | [Q187–214](issues/10-define-clipp-runtime-integration.md), Android Q195, no import Q203/204, portal handoff Q212 |
| I8 lifecycle | [Q112–136](issues/07-define-relay-pool-lifecycle-and-assignment.md) |
| I9 deployment | [Contract](deployment-contract.md), [Q242–279](issues/11-define-helm-and-argo-cd-deployment-contract.md), recovery workload amendments |
| I10 recovery | [Contract](recovery-contract.md), [Q280–307](issues/15-define-postgresql-backup-restore-and-deletion-recovery.md), revised Q289 |
| I11 limits | [Baseline](issues/12-choose-initial-quota-and-safety-limit-defaults.md), [Q308–348](issues/16-define-remaining-operational-limit-defaults.md), [full scope matrices adopted by Q338](research/operational-resource-scope-profile.md) |
| I12 capacity caveats | [Prototype/revised Q337](issues/17-prototype-transport-capacity-and-memory-accounting.md), Q228/Q350/Q355 |
| Testing/release | [Q349–358 and original evidence ledger](issues/13-define-acceptance-release-and-operating-criteria.md), user-confirmed seams 2026-09-27 |

Respect Clipp [signed-record ADR](/Users/invine/src/js/clipp/docs/adr/0007-forward-libp2p-signed-peer-records.md),
[identity-key storage ADR](/Users/invine/src/js/clipp/docs/adr/0008-store-device-keys-in-runtime-application-storage.md)
and [history ADR](/Users/invine/src/js/clipp/docs/adr/0011-use-live-gossip-with-full-history-reconciliation.md).
Test prior art: [network](/Users/invine/src/js/clipp/tests/core/network/engine.test.ts),
[Rendezvous](/Users/invine/src/js/clipp/tests/core/network/rendezvous.test.ts),
[runtime conformance](/Users/invine/src/js/clipp/tests/core/runtime/historyReconciliationConformance.test.ts),
[extension bridge](/Users/invine/src/js/clipp/tests/extension/networkBridge.test.ts).

Implementation belongs in clipp-relay for service/chart (accepted chart target
`charts/clipp-relay`) and Clipp for shared core/UI and runtime-specific adapters.
Follow each repo's current instructions; this spec does not prescribe source
package/file layout. Bundled PostgreSQL's accepted deployment paths are volume
`/var/lib/postgresql` and `PGDATA=/var/lib/postgresql/18/docker`.

Qualified prototype references: auth harness `d95c6d0`, portal Selected mix
`2d21383`, quota lab `db69a81`, transport-capacity ticket 17. Historical wording,
demo policy and layout may predate accepted refinements; do not copy prototypes
unreviewed as production foundations. The [previous handoff index](implementation-handoff.md)
is retained as a historical reading aid, not competing normative text.

Operator-specific hostnames/ports, exact client release callback identities,
Secret versions, image/dependency pins, namespace/network/storage trust,
repository/IAM access and monitoring destination are deployment inputs with
preflight/release gates, not reopened design questions. No accepted limits/goals
were changed by consolidation. Map remains resolved; ready-for-agent means
ready for implementation work, not production accepted or auto-authorized deployment.
