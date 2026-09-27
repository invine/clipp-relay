# 27: Qualify authentication and privacy

**What to build:** Qualify real account authorization and credential privacy across Electron, Android and Chrome, including recovery and hostile-provider cases.

**Blocked by:** [13: Use managed relays from Electron](13-use-managed-relays-from-electron.md); [14: Use managed relays from Android](14-use-managed-relays-from-android.md); [15: Use managed relays from Chrome](15-use-managed-relays-from-chrome.md); [22: Restore accounts without resurrection](22-restore-accounts-without-resurrection.md); [26: Observe failures and expose alert signals](26-observe-failures-and-expose-alert-signals.md).

**Status:** ready-for-agent

Repository scope: Both repositories.
Source: [Accepted specification](../../managed-relay-service/spec.md), I2–I7, I9, I10; Testing.

## Contract and scope

Build on tests shipped with each slice and run an integrated authentication/privacy qualification matrix on a recorded candidate. Primary observations are HTTP/libp2p outcomes, portal state and authorized durable effects; complete Account/Quota/Relay seams supply deterministic concurrency/clock/commit faults. Adapter doubles are not evidence for native storage, real Google or cross-runtime behavior.

Use real Google test-account flows in all three actual runtimes before first serving and after material authorization changes, with explicitly supplied client IDs/redirect registrations/credentials and authorized accounts. Missing devices/provider credentials mean not run and block this ticket, not a fabricated pass or relaxed validation. Separately run hostile Google doubles for signature/issuer/audience/expiry/nonce/state, PKCE/callback/replay, key rotation/cache/cooldown/oversize/outage and ambiguous one-use exchange.

Verify canonical External Identity, concurrent registration, every account state, current authoritative admin email/allowlist/revision/recent-auth and Origin/CSRF/logout behavior. Account revocation and sign-out-everywhere must not grow individual grant/device-ownership interfaces. Exercise code/refresh races/replay/caps/lifetimes, generation/fence exhaustion and browser/process loss. Keep device network trust and account authorization distinct.

Test actual Electron OS-provider protection/insecure-provider RAM fallback, Android native Keystore and backup/transfer exclusions, Chrome trusted-context access and background/offscreen boundary. Access Tokens/PKCE/discovered relay identities remain transient; rotation save failures never replay old credentials. Run synthetic secret canaries through callbacks, errors, app/infrastructure diagnostics, metrics and unauthorized storage/bridges. Infrastructure operator-only 7d retention is no permission to log credential headers/query/body/SQL parameters.

Include delete/re-register, pepper/retention and offline-restore consequences: no resurrected credentials/deleted accounts, current authority and explicit account review holds. Account and audit privacy must match the typed schema allowlist; redacted output is not permission for forbidden durable columns.

## Acceptance criteria

- [ ] Record actual successful Google authorization/renewal and relevant cancellation/refusal flows on Electron, Android and Chrome with exact OS/runtime/client-registration versions.
- [ ] Pass hostile-provider and replay/callback/PKCE/key-cache boundary cases with bounded work, no unverified-claim fallback and no ambiguous exchange retry.
- [ ] Pass all account/admin/Origin/CSRF/logout transitions and races, including malformed live allowlist, current revision enforcement and stale browser/unknown-identity fences.
- [ ] Prove code/refresh single-use and family replay behavior, caps/lifetimes, account-generation revocation and no individual grant-management or durable device-account surface.
- [ ] Inspect native/runtime storage and communication boundaries for each client, including restart/corruption/save-failure/backup cases; mocks alone cannot satisfy these checks.
- [ ] Run canaries through app and scoped infrastructure diagnostics/metrics/callbacks/unauthorized storage; forbidden credential or identifier leakage is a defect, not an accepted exception.
- [ ] Demonstrate deletion/re-registration and restored-account cases retain quota/privacy semantics and current authority; every executed/not-run result links to candidate-applicable evidence.

## Demonstration

On the same candidate, perform a real Google login from each runtime, revoke account access in the portal and observe all clients close. Follow with secret-canary, replay and restore-review scenarios, producing a privacy-safe qualification ledger.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

