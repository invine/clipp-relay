# 20: Rotate database credentials and certificates

**What to build:** Rotate database passwords and TLS certificates with verified convergence, no TLS downgrade and no surprise database restart.

**Blocked by:** [19: Install bundled PostgreSQL](19-install-bundled-postgresql.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I9, I11.

## Contract and scope

The same-CA leaf watcher is an independently pinned non-root sidecar with projected public certificate/trust and only the restricted reload credential; it never receives the server private key, bootstrap credentials, root/shared-PID authority or API write access. Reload role has connection and EXECUTE pg_reload_conf only, including PUBLIC/default privilege hygiene, no table/schema-create/role/superuser rights. Wait for role provisioning without blocking initial database readiness.

Observe a complete projected generation every 30s, perform one reload+fresh verification operation bounded to 10s and prove the expected certificate is served with hostname/chain validation. Retry 5/15/30/60s with ±20% at cap; independently recheck every 5m. Incomplete files/failed reload/old served leaf are not success. Warn after 5m nonconvergence or ≤7d to expiry, escalate at ≤24h, expired immediately; document explicit adjustment for short-lived certs. Failure never restarts the DB or disables TLS. Expired-cert recovery is operator local reload, not insecure remote fallback.

Password rotation is planned maintenance: stop all affected clients including maintenance, perform authenticated role change, fresh verify-full connection with replacement, select non-reused versioned Secret and restart clients. Partial failure remains stopped; compromise additionally terminates old database sessions. Never silently reconcile passwords at every startup. CA rotation uses staged trust coordination during maintenance, verifies new service and explicitly verifies removal of old trust. Respect the database/sidecar budgets from the bundled install slice.

## Acceptance criteria

- [ ] Exercise a complete same-CA leaf renewal and prove a fresh connection sees the expected certificate without DB restart or lost durability.
- [ ] Reject incomplete projections, wrong CA/hostname, unauthorized role and stale served leaf; enforce single operation, poll/retry/timeout/recheck bounds.
- [ ] Through SQL permission tests prove the reload role cannot read application tables, create objects/roles, become superuser or inherit an equivalent PUBLIC privilege.
- [ ] Demonstrate initial role absence does not block DB readiness, while final serving validation still requires intended role/trust configuration.
- [ ] Rotate a password using the stopped sequence; inject failures after role change and after Secret selection and show clients remain stopped until verified recovery.
- [ ] Test staged CA rotation, old-trust removal and compromise-session termination with no plaintext/encryption-only connection or automatic password reset.
- [ ] Expose nonidentifying convergence/expiry state and operator instructions for expired certificates; verify watcher budgets and absence of private keys, generic exec/API-write/root authority.

## Demonstration

Rotate a test certificate and show expected-leaf convergence; then perform a stopped password rotation. Inject a wrong replacement CA and show the system stays safely stopped without TLS relaxation.

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

