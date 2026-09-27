# 08: Register and find peers

**What to build:** Authenticated peers register verified reachability and find one another by exact Peer ID without an enumerable device directory.

**Blocked by:** [07: Relay authenticated TCP traffic](07-relay-authenticated-tcp-traffic.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I4, I11.

## Contract and scope

Serve unchanged unframed /clipp/rendezvous/1.0.0 and framed
/clipp/rendezvous/2.0.0 with one shared lease store and exact-connection gate.
V2 uses unsigned-varint-length JSON, fixed topic clipp, register/lookup/unregister,
base64url unchanged Signed Peer Record. Success registration/found response has
leaseExpiresAt; absent/expired/unregistered returns ok=true without record.
Active-session lookup crosses accounts, no list/batch. Register also requires
live reservation, valid signature/subject matching remote peer and at least one
current-relay circuit route. Newest valid owner replaces atomically; stale cleanup
cannot remove replacement.

Lease=min(session,reservation,5m TTL); remove owner loss/unregister. All operation
work ≤12s; 16KiB raw signed envelope, v2 JSON32KiB, v1 JSON128KiB numeric bytes,
bound decoded/address parsing before allocation. Strict request unique keys/
types/no unknowns or trailing data, omitted optionals, Z timestamps, no compression.
Invalid frames reset, semantic errors one frame then orderly close. Preserve
envelope unchanged. Errors include reservation_required/invalid_peer_record/
authentication_failed/invalid_request/rate_limited/temporarily_unavailable.
Buckets global500/s burst1000, authenticated connection4/s burst8, ≥5s temporary hint.

RM custom service clipp.rendezvous: streams2048/2048/0,64MiB; service-peer8/8/0,2MiB.
V1 protocol1024/1024/0,32MiB; peer4/4/0,1MiB. V2 protocol2048/2048/0,64MiB;
peer8/8/0,2MiB. All these connection/FD fields zero-block. Attach service and
reserve/release bounded handler buffers. Metrics distinguish v1/v2 only with
bounded labels, no peer/account. This slice retains v1; no protocol retirement.

## Acceptance criteria

- [ ] Real wire clients register/lookup/unregister using each version and receive byte-identical verified envelopes.
- [ ] Unreserved, unauthenticated exact connection, signature/subject mismatch and no-current-relay-route registration fail without retaining record.
- [ ] Cross-account exact lookup works; misses/expired/unregistered are indistinguishable and no enumeration route exists.
- [ ] Lease expiry follows earliest bound; loss/replacement/late timer races cannot delete a newer owner.
- [ ] Input/output encoded/raw bounds, invalid numeric bytes, duplicate keys, extra frames and slow read/write are tested with bounded allocation/work.
- [ ] Protocol negotiation test falls back v2→v1 only for unsupported multistream, never auth/quota/timeout/server errors; version metrics contain no identity.

## Demonstration

Register one peer with v2 and find it through v1 from another authenticated account; expire or replace its lease and verify exact lookup/old-owner cleanup.

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

- Claimed centrally on 2026-09-27 after ticket 07 resolution (`49eed87`); assigned to a fresh isolated Go implementation agent. Resolution awaits integration review and acceptance evidence.
