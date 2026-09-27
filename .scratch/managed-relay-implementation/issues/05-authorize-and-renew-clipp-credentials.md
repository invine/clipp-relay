# 05: Authorize and renew Clipp credentials

**What to build:** An approved account authorizes a registered Clipp public client and safely renews its relay-only credentials.

**Blocked by:** [04: Approve accounts and assign plans](04-approve-accounts-and-assign-plans.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I3, I5, I6, I11.

## Contract and scope

Separate Clipp OAuth hop from Google. Public clients have no shared secret.
Require S256 PKCE and independent client state; exact registered redirect, only
Electron IPv4 127.0.0.1 callback port may vary. Android fixed private URI, extension
exact chromiumapp callback. Reject arbitrary return targets, fragments/userinfo,
localhost aliases and downgrade. Code binds account/generation/client/redirect/
challenge; HTTPS form token POST, no-store body credentials only. Google credentials
never reach app callback.

Code 5m, access 15m, transaction 10m, Login Grant inactivity 30d/absolute 180d,
20 active grants/account, no eviction. Ceilings may shorten, not extend. Only
Active eligible account at every issuance/exchange/refresh/access check. Tokens
opaque independent 32-byte values, purpose-separated HMAC/version only in DB;
fixed local Discovery/Auth audience, never admin/portal authority.

Refresh strictly single-use, atomic rotation; inactivity slides but absolute
does not. Old Access Tokens remain until expiry. Consumed-generation reuse
revokes owning grant/derived access, not other grants; consumed hashes retained
while grant active. Ambiguous committed rotation requires fresh login, no overlap
or generic retry. Add credential generation and account-row serialization now.
Unknown-identity flow fences: process-random HMAC keys, 4,096 entries; overflow
atomically cancels all unfinished flows before safe reclaim. No grant/device
binding or individual revoke/list API. Grant-specific replay cannot later close
live sessions by grant identity because sessions retain account only.

## Acceptance criteria

- [ ] Public-client contract harness completes each registered callback/PKCE flow against real DB and gets only local relay credentials; Pending/Suspended/Denied receive no code.
- [ ] Wrong verifier/client/redirect/state, code replay/expiry and generation change fail without issuing credentials; parallel code redemption succeeds once.
- [ ] Concurrent refresh and consumed-generation replay enforce single-use and grant-local invalidation; lost response never causes silent replay grace.
- [ ] Grant cap and idle/absolute/access deadlines use authoritative after-lock time; refresh does not revoke older valid Access Tokens.
- [ ] Old account-bound and initially unknown login flows cannot survive revocation fences; overflow/restart cancels unfinished flows without touching unrelated established credentials.
- [ ] Raw credentials/Google tokens/PKCE/state are absent from persistence/logs; attributable blocked/grant-cap/replay audit follows privacy policy.

## Demonstration

Run registered-client authorization and refresh harness, replay the consumed token, and verify that only its grant is invalid while a separately authorized grant still works.

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

