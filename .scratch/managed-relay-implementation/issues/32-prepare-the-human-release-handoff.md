# 32: Prepare the human release handoff

**What to build:** Prepare an evidence-backed release package for a human to promote through the documented manual Argo phases.

**Blocked by:** [31: Qualify initial endurance and operator readiness](31-qualify-initial-endurance-and-operator-readiness.md).

**Status:** ready-for-agent

Repository scope: Both repositories.
Source: [Accepted specification](../../managed-relay-service/spec.md), Testing; release gates.

## Contract and scope

Assemble the candidate manifest, completed qualification evidence and exact manual promotion/reopening procedure without executing production promotion. Verify source/dependency locks, image/helper digests, chart/schema, client/protocol versions and configuration still match. Evidence reuse requires candidate applicability; rerun affected protocol/dependency/image/schema/chart/auth/backup checks after material changes.

Maintain an explicit passed/failed/not-run ledger for real clients/transports/Google, account/privacy, real PostgreSQL/quota/faults, automated/image checks, Helm/Argo/OCI/TLS/network/storage, timed off-cluster recovery/journal/baseline, four-hour endurance/overload, bounded telemetry, real operator notifications and tested runbooks. Missing required devices/credentials/runners/operator inputs are not run and block promotion. Prototype outcomes are not production evidence.

Known security/privacy/quota-integrity/schema/recovery defects block release. Cosmetic UI and explicitly deferred target-scale work may remain documented; other omissions/failures need an explicit decision, not an invented waiver. The accepted limits and 5,000-session goal remain unchanged and unproven unless separately measured.

Hand the human current nonsecret operator inputs/references and responsibilities, first-serving or pre-change baseline checks, current journal identity/coverage and authority, intended run/image/schema, account-review obligations, monitor destination and supported failure/rollback paths. Keep credentials outside artifacts. CI/build success grants no authority to mutate production. Completing this ticket means the release handoff is ready, not that the human has promoted it.

## Acceptance criteria

- [ ] Audit all mandatory earlier tickets and candidate-bound evidence; list each gate as passed/failed/not-run with commands/results/artifact references rather than inferred completion.
- [ ] Reconcile immutable source/image/chart/schema/client/config identity after qualification and rerun affected checks for every material change.
- [ ] Confirm no known blocking security/privacy/quota/schema/recovery defect or missing mandatory result is hidden by a waiver; document only permitted cosmetic/deferred capacity follow-ups.
- [ ] Verify operator prerequisites, exact manual stopped/migrate/serve sequence, recovery baseline/journal coverage and real monitoring/notification evidence are present and current.
- [ ] Supply a privacy-safe release packet with current authority/Secret-version references, rollback or forward-fix boundaries, account-review duties and explicit reopening verification.
- [ ] State the measured initial endurance scope and unproven eventual capacity goal without changing production limits or requiring the deferred capacity ticket to close.
- [ ] Deliver the handoff for explicit human promotion; do not deploy, publish, auto-sync, modify the resolved decision map or claim production promotion was executed.

## Demonstration

Walk the release packet from immutable candidate to each required evidence item and the exact manual Argo plan. Demonstrate that a stale digest, missing recovery check or unknown monitoring destination blocks the handoff rather than being marked green.

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

