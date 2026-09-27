# 03: Register and sign in

**What to build:** A user signs in with Google, receives one Pending Relay Account and can inspect status and log out of the portal.

**Blocked by:** [02: Start a validated Go service](02-start-a-validated-go-service.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I2, I3, I6, I11.

## Contract and scope

Use confidential Google web OIDC with existing Secret client ID/secret, only
openid email, no offline Google access. Validate signature/issuer/audience/expiry/
nonce/state; canonicalize documented issuer spellings to https://accounts.google.com.
Identity is issuer/sub behind a random UUID, email only latest presentation.
Store latest email/email_verified/hd/validation and portal-login times, no Google
tokens/name/avatar/history. Concurrent first login creates one account and atomic
audit. All four account states can use portal; no relay credential yet.

Portal Session: opaque __Host- Secure/HttpOnly/SameSite=Lax cookie, idle 30m/absolute
12h, HMAC-only DB storage using versioned purpose-separated peppers and independent
32-byte random values. Allowed Origin plus derived stable per-session synchronizer
CSRF on mutations. Log out affects current Portal Session only. Authorization
transaction 10m requires DB digest and process-local continuation/browser binding;
restart cancels unfinished login, not valid portal sessions.

Bound Google work to 8 concurrent/8s provider I/O within 15s HTTP parent. Responses:
16KiB headers/256KiB decoded body, 32 keys/256-byte kid, freshness provider-capped
with local 6h maximum, unknown-key shared 60s fetch cooldown except empty cache.
Deduplicate fetches, no expired-key or unverified-claim fallback. Login RAM:
1,024 continuations, 16KiB each. Public HTTP: 512 connections/60s idle, 128 requests,
16KiB headers/8KiB target/16KiB body, 5s header/10s body/15s handler; rates 200/s
burst 400 global and 10/s burst 20 resolved-account; no IP buckets.

## Acceptance criteria

- [ ] Real PostgreSQL-backed browser flow displays Pending status in the accepted light shell; repeated/concurrent issuer-equivalent logins resolve one account.
- [ ] Provider-double tests reject signature/claim/state/nonce/browser-binding errors, replay, oversize, key-cache expiry and unavailable verification.
- [ ] Session idle/absolute expiry, restart persistence, cookie attributes, Origin/CSRF and independent-tab behavior are verified.
- [ ] Logout invalidates only current portal session and clears its cookie; malformed/expired credentials reveal no sensitive identity detail.
- [ ] Provider/HTTP/continuation saturation rejects boundedly with no unbounded queue or token logging; no DB lock spans Google I/O.
- [ ] Secret canaries do not appear in logs, retained Google responses or unsafe cookies; tests prove DB outage cannot create fallback account/session state.

## Demonstration

Use a deterministic OIDC provider in tests to sign in twice, show one Pending account, restart the service and log out. Real Google evidence remains required by the qualification ticket.

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

- Claimed centrally on 2026-09-27 after ticket 02 resolution (`2b9a5f7`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.
