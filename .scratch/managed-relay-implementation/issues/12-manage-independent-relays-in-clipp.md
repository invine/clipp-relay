# 12: Manage independent relays in Clipp

**What to build:** Configure multiple independent relays through a shared Clipp controller and UI contract, without restarting the host or disrupting direct peers.

**Blocked by:** [01: Isolate Clipp relay lifecycle](01-isolate-clipp-relay-lifecycle.md); [08: Register and find peers](08-register-and-find-peers.md).

**Status:** resolved

Repository scope: Clipp.
Source: [Accepted specification](../../managed-relay-service/spec.md), I4, I7, I11.

## Contract and scope

A relay configuration is a strict union: a canonical HTTPS Discovery URL for an authenticated relay, or a nonempty list of complete multiaddrs for one persistent unauthenticated Peer ID. Local key/name are presentation; credentials are separate and scoped to the exact endpoint/audience. Reject duplicate canonical URLs and duplicate unauthenticated Peer IDs. If different discovery URLs publish the same Peer ID, preserve the established connection and flag conflict; never exchange credentials or switch accounts.

The shared core owns discovery → verified dial → authentication → reservation → Rendezvous, renewal, retry and independent state for every configured relay. Keep one host and the existing Device Identity. Runtime adapters supply browser/HTTP/storage/lifecycle. No automatic circuit-relay listener may bypass authentication. Metadata edits stay in place; endpoint/Peer ID replacement resets credentials and retries, while explicit addresses for the same peer may update in place. Removing an entry closes only its resources and erases local credentials, not server grants.

Use bounded staggered dialing: four configurations concurrently, two address candidates per configuration; first Noise-verified expected-peer winner receives credentials and losers close. Discovery validity is at most 60s; whole-operation deadlines are discovery 10s, address dial 20s, authentication 10s, reservation 15s and Rendezvous 12s, with any earlier server initial-auth deadline taking precedence. Retry uses half-to-full jitter over 1/2/4/8/16/30s; known quota/session refusals use 5m ±20%, honoring longer server hints even on manual retry. Single-flight refresh per configuration. Invalid credentials require explicit login; never start browser loops.

Ready requires authentication, reservation and Rendezvous; Rendezvous-only failure is degraded and preserves circuits. Fall back from Rendezvous v2 only on explicit unsupported-protocol negotiation. Keep Signed Peer Records intact, skip unconfigured relay routes and continue direct/eligible routes without auto-configuration/login. New installs and first upgrades adopting this model start empty with no legacy import; later new-model settings persist. This ticket ships the shared action/state contract and adapter-conformance harness; runtime wiring follows in dependent tickets.

## Acceptance criteria

- [x] Reject invalid/mixed configurations and both duplicate forms; test canonicalization and same-Peer-ID discovery conflict without token leakage.
- [x] Show independent connecting, login-needed, ready, degraded and refusal states through the shared UI contract; account-management opens the portal without claiming account revocation or login success.
- [x] Exercise all configured relays concurrently within dial bounds; verify loser cleanup, expected Peer ID verification before credentials and no automatic reservation before authentication.
- [x] Prove metadata edits, identity-changing replacements, address edits and removal obey ownership and preserve host identity, direct connections and other relays.
- [x] Use deterministic time to verify deadline, discovery expiry, backoff, longer server hints, single-flight renewal, explicit login and Rendezvous repair/fallback rules.
- [x] Test empty/new-model configuration, no implicit built-ins/legacy migration, unmodified Signed Peer Records and direct connectivity with no relays.
- [x] Keep unmigrated runtime callers green through compatibility adapters; existing Device Identity and history storage/protocol behavior stay unchanged. Run shared tests and npm run check.

## Demonstration

Run an adapter-conformance scenario with two authenticated relays, one explicit unauthenticated relay and a direct peer. Fail and edit one relay while the others continue; inspect shared UI state and bounded operation counts.

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

- Claimed centrally on 2026-09-27 after ticket 01 resolved (`6cf8c3b`) and ticket 08 remained resolved. The approved Clipp integration baseline is `64c87b6`; implementation will use a fresh agent and isolated worktree. Review, integration checks and acceptance evidence are required before resolution.

- Resolved centrally on 2026-09-27 after fresh-context TDD in isolated Clipp worktree, independent specification and standards reviews, review fixes through `609ccdd`, and integration merge `ab56937`. The three-relay adapter scenario exercises two authenticated relays, an explicit relay and a direct peer through failure and edit. Integrated checks on Node 26.10.0/npm 11.19.1: `npm run check` (58 suites, 502 tests), `npm run lint` (0 errors), Electron/Android/extension builds, `node --import tsx tests/harness/managedRelayAddressConformance.ts`, and localhost `npx tsx tests/harness/pairing-harness.ts` all passed. `git diff --check` passed; integration worktree clean. The initial `npx tsx` conformance invocation hit sandbox pipe `EPERM`; the equivalent `node --import tsx` invocation passed. Runtime wiring and native platform checks belong to tickets 13–15.
