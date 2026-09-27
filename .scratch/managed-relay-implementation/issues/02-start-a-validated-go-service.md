# 02: Start a validated Go service

**What to build:** Start a real Go process against a supported PostgreSQL database and obtain honest health/configuration outcomes after an explicit migration.

**Blocked by:** None (can start immediately).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I1, I5, I9, I11.

## Contract and scope

Use one binary/module, net/http, embedded portal assets and concrete pgx v5 SQL.
Introduce only schema needed for this executable slice; later features add ordered
migrations rather than building all tables up front. Serving has no DDL permission.
Separate migration command verifies ordered checksums, serializes runners and
commits each transaction-compatible change with its revision. Every build checks
one exact expected revision and PostgreSQL 17/18 compatibility; never auto-migrate.

One versioned non-secret config, explicit Secret-file references, no unknown fields
or flag/env policy precedence. Validate canonical origin, listeners, static keyring
and DB verify-full TLS settings without printing secrets. Startup cannot claim
relay readiness before listeners/publication exist; private livez/readyz use plain
200 ok or 503 unavailable, independent of later DB outages.

Runtime pool max 8/min 0, 64 active+waiting DB units admitted before logical locks;
acquire 500ms, whole unit 3s, SQL statement 2s/lock 250ms/idle-in-txn 5s.
Idle 5m, lifetime 30m plus uniform 0–5m, health 60s, connect 3s parent-capped.
Private HTTP: 32 connections/16 requests/2 scrapes, health 1s/metrics 5s/header 2s/
idle 30s. Application logs stdout, structured/redacted, no stable identity labels.

## Acceptance criteria

- [ ] Fresh real PostgreSQL migrates explicitly and matching application starts; wrong major/schema, checksum changes, concurrent runners and failed transaction are tested.
- [ ] Serving DB role cannot DDL; TLS wrong hostname, untrusted CA and plaintext fallback fail closed.
- [ ] Unknown/missing/conflicting configuration and unreadable required secret material fail without secret disclosure.
- [ ] DB work above admission/pool budgets is bounded and temporary; deadlines cover waits and cancellation does not imply rollback.
- [ ] Public listener exposes no private operations/debug routes; health remains honest during startup and dependency loss.
- [ ] Go formatting/static checks, tests and reproducible local real-PostgreSQL smoke command pass.

## Demonstration

Initialize a disposable verified-TLS PostgreSQL, migrate, start the binary, query private health, then demonstrate incompatible schema and lost-DB behavior.

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

- Claimed centrally on 2026-09-27 for Go implementation agent; isolated starting commit `2136aa9`. Resolution awaits integration review and acceptance evidence.
