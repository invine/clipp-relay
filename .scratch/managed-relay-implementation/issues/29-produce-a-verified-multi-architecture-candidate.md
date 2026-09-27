# 29: Produce a verified multi-architecture candidate

**What to build:** Build a reproducible, verified Linux ARM64/AMD64 release candidate and bind all automated evidence to its immutable artifacts.

**Blocked by:** [24: Operate safe Argo upgrades and cutovers](24-operate-safe-argo-upgrades-and-cutovers.md); [27: Qualify authentication and privacy](27-qualify-authentication-and-privacy.md); [28: Qualify clip and history interoperability](28-qualify-clip-and-history-interoperability.md).

**Status:** ready-for-agent

Repository scope: Both repositories.
Source: [Accepted specification](../../managed-relay-service/spec.md), Testing; I9.

## Contract and scope

Produce a candidate, not a published release. Record source revisions and dependency locks, immutable image digests, chart/schema revisions, client/protocol versions, selected configuration and evidence in a release manifest. Pin relay/runtime, database/pgBackRest and watcher artifacts independently as specified; use the same intended relay image for applicable maintenance. No floating latest, runtime Node/build toolchain or startup build.

Run applicable formatting, static/type/lint, unit/contract/integration, E2E, concurrency race and fault/operation suites, plus Clipp npm run check and relevant runtime builds. SQL correctness executes against PostgreSQL 17 and 18, with both deployment modes and all chart phases covered. Reuse only evidence demonstrably applicable to the candidate; changes to protocol/dependencies/images/schema/chart/auth/backup rerun affected checks.

Execute smoke/integration in the actual Linux ARM64 and AMD64 images; cross-compilation alone is not execution. Check non-root/read-only container startup, role/schema/config failures and pinned helper/runtime behavior. Missing architecture runner/device/credential is not run, never green. Scan dependency and container inventories; reachable serious vulnerabilities block. False-positive/irrelevant findings need rationale, owner and review date, not blanket waiver.

The resulting manifest is the input to isolated OCI and endurance qualification. It is not proof those later environment gates passed and gives no authority to publish artifacts, push a registry, mutate production or promote via CI.

## Acceptance criteria

- [ ] Create reproducible pinned relay/DB-helper/watcher builds and a manifest containing all immutable artifact/source/config/schema/client/protocol identities without Secrets.
- [ ] Run applicable formatting/static/type/lint/unit/contract/integration/E2E checks, Go race tests and Clipp npm run check/runtime builds; attach actual commands and results.
- [ ] Execute required database tests on PostgreSQL 17 and 18 and chart schema/lint/render across both modes/all phases, including expected-failure configuration/schema cases.
- [ ] Execute Linux ARM64 and AMD64 image smoke/integration tests, demonstrating intended non-root/read-only runtime and helper behavior rather than only successful compilation.
- [ ] Run dependency/image scans and resolve reachable serious findings; any dismissed finding carries specific rationale, owner and review date.
- [ ] Audit evidence applicability to exact candidate identities; stale or missing mandatory results remain failed/not-run and prevent candidate completion.
- [ ] Provide an immutable local candidate handoff for OCI qualification, with no release publication, registry push or production deployment performed under this ticket.

## Demonstration

Build the candidate, execute its images on both architectures and generate a manifest linking every required automated result and scan to exact digests. Demonstrate that a missing runner or changed dependency invalidates the relevant evidence rather than silently reusing it.

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

