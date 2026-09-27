# 26: Observe failures and expose alert signals

**What to build:** Expose bounded, privacy-safe operational signals and an external monitoring contract without requiring Prometheus in the cluster.

**Blocked by:** [20: Rotate database credentials and certificates](20-rotate-database-credentials-and-certificates.md); [23: Expire recovery material safely](23-expire-recovery-material-safely.md); [25: Survive dependency loss and overload](25-survive-dependency-loss-and-overload.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I6, I11, I12.

## Contract and scope

Use one structured application logging path to stdout and a private Prometheus-compatible metrics endpoint. Logs contain only ephemeral request/session IDs and safe enumerated outcomes; no stable account/External Identity/email/peer/public address/credential/content. Metrics labels are bounded enums, never identifiers, arbitrary URLs/errors or attacker-selected keys. Durable accountability remains restricted append-only audit, not a second traffic log. Diagnostic queues/buffers must be bounded.

Cover lifecycle/publication, pending connections/handshakes/sessions/reservations/circuits/streams, admission/auth/denial, conservative charged and unattributed bytes, allocation/stranding, retry, DB/provider latency/failure, restart/drain, memory/CPU/disk, certificate convergence, cleanup/pending deletion and proven recovery freshness. Do not imply exact per-circuit payload/overshoot or expose account-level series.

Private HTTP stays independent of public pressure: 32 connections, 16 requests, ≤2 concurrent scrapes, health 1s, metrics 5s, headers 2s, idle 30s; metrics cannot consume every health slot. Health and scrape use bounded local snapshots, not synchronous dependency checks. Only livez/readyz/metrics; no debug/export endpoints or public ingress.

Thresholds: app cleanup >1h warning, 12h escalation, explicit 24h purge breach; pending deletion 15m warning, 1h or 80% of 1,024 critical; repository pass overdue 1h warning/6h escalation; cert nonconvergence 5m or ≤7d expiry warning, ≤24h escalation/expired immediate. Observe recovery/disk every 60s: recoverability lag >5m warning, >15m or unknown breached; full backup age >26h warning/>48h escalation; filesystem 80/90/95% warning/escalation/operator protective stop. Unknown external/provider measurements are unknown, never green.

Before public serving an operator-owned monitor and notification destination outside the process must detect critical conditions and lost observations/availability, deliver an already-detected critical condition within 5m and route warnings. This slice supplies the contract/fixtures and tested integration procedure, not an in-process email/webhook/paging service, Prometheus install or automatically created monitoring job. Actual operator readiness is a later release gate.

Restricted infrastructure logs may contain operational IP/DB error metadata under operator-only access for at most 7d including exports, but query/body/credential headers/SQL statements or parameters remain disabled. Credential-bearing route errors are suppressed at source. Scoped F5 configuration is an operator prerequisite, not permission for controller-wide snippets or a custom log supervisor.

## Acceptance criteria

- [ ] Verify metrics render correctly with bounded labels and stable semantics, and all application logs use redacted stdout with no second logger/file queue.
- [ ] Inject synthetic account/peer/email/URL/credential/content canaries into every relevant request/error; no forbidden values reach logs, metrics or unauthorized retained diagnostics.
- [ ] Exercise all listed lifecycle, admission, accounting, dependency, certificate, cleanup and recovery signals, including explicit unknown state and threshold transitions.
- [ ] Under public and scrape saturation prove bounded private-health completion and no dependency fetch during scrape/health; reject excess work rather than buffering indefinitely.
- [ ] Document and test restricted infrastructure logging/retention controls and identify required F5/operator inputs without changing global controller settings.
- [ ] Validate an external-monitor integration fixture for critical delivery within 5m after detection, warnings and loss of observations; distinguish simulation from an actual configured destination.
- [ ] Document safe protective-stop signals without automatically growing disk, deleting WAL, resetting quota, killing the DB or restoring backups; keep archival/recovery possible during maintenance.

## Demonstration

Inject a DB fault, stale backup signal, certificate mismatch and pending-deletion backlog. Inspect private metrics and stdout, then route fixture alerts through a test destination and prove redaction and private-health isolation under load.

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

