# 18: Install with external PostgreSQL

**What to build:** Install an isolated single-process relay with an external PostgreSQL database using a validated Helm chart and manual Argo CD example.

**Blocked by:** [11: Rediscover and drain an ephemeral relay](11-rediscover-and-drain-an-ephemeral-relay.md).

**Status:** claimed

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I9, I11.

## Contract and scope

Deliver schema-validated values, external-database example and a pinned-Git-revision Argo Application consuming existing namespace, Secrets, F5 NGINX, issuer and OCI inputs. Chart owns only scoped workloads/network/configuration/RBAC. No tenancy, cluster, IAM, DNS, registry, controller or secret-infrastructure provisioning. Use one versioned non-secret configuration and file/key references; reject unknown/missing/conflicting settings. No Helm lookup, generated persistent secret, secret checksum/annotation or Secret-read API permission. Live projected files use normal volumes, not subPath.

One singleton StatefulSet serves only in serving phase, zero during maintenance; no relay PVC/HPA/blocking PDB. Preserve network-slot Services across maintenance. Exact HTTPS portal/discovery/callback host and distinct exact WSS host terminate through existing F5 with separate ClusterIP ports/backends. WSS never exposes portal routes. Optional Certificates reference existing issuers; no wildcard required. Separate TCP and UDP OCI NLB Services use direct Pod backends without NodePorts, explicit NSGs, instant failover disabled and private readyz HTTP-health JSON configuration. Namespace-scoped read-only watches cover only named address Services; overrides eliminate that dependency.

Private operations has no public frontend: 32 connections, 16 requests, at most two scrapes; health 1s, metrics 5s, header 2s, idle 30s. Startup readyz every 5s/2s timeout/120 failures; readiness 5s/2s/1 failure/1 success; liveness 10s/2s/3 failures. No public-self-dial/readiness cycle or synchronous DB probe dependency.

External PostgreSQL 17/18 uses explicit endpoint/database/trust, full hostname/chain TLS verification and SCRAM; distinct serving/migration Secrets, no serving DDL or implicit fallback. Enforce default-deny NetworkPolicy with actual engine prerequisites: public data ports, ingress-only portal/WSS, approved private health/scrapes, permitted DB/API/DNS/storage/public-HTTPS egress. HTTPS egress is not a Google-domain firewall; forbid arbitrary new peer dials and trust proxy metadata only from explicitly configured ingress paths.

Non-root/read-only root/capabilities dropped/no privilege escalation/runtime-default seccomp; no host mounts/ports/network or root repair. Relay CPU request/limit 1/2, memory 1/2GiB, ephemeral 64/128MiB, Go soft memory 1,536MiB and verified nofile ≥16,384. Immutable Linux ARM64/AMD64 Go-only image, embedded portal, no startup build/latest. This is isolated installation scaffolding: full recovery and public-serving gates arrive in dependent operational slices, never permission for early public rollout.

## Acceptance criteria

- [x] Helm lint/render/schema tests cover explicit external mode, invalid/conflicting values, Secret references, exact hosts, listener conflicts, transport overrides and maintenance rendering.
- [ ] An isolated authorized installation starts only against the expected exact schema using verified TLS/SCRAM on PostgreSQL 17 and 18; wrong CA/hostname/role/schema fails closed.
- [x] Render and inspect distinct portal/WSS/TCP/UDP/private surfaces, stable network resources, singleton nonoverlap and narrowly scoped watch RBAC.
- [ ] Prove read-only/non-root security and configured resource/nofile envelope; no Secret contents, secret-derived annotations, runtime DDL or privileged credentials reach serving.
- [x] Provide a non-mutating preflight distinguishing rendered facts, existing cluster prerequisites and required live checks; absent inputs stay explicit operator tasks.
- [ ] Test private operations budgets/probes under public pressure and verify readiness does not depend on DB availability or self-dial.
- [x] Document exact F5/OCI/network-policy prerequisites and an isolated install/uninstall procedure preserving operator-owned resources; no production deployment is performed by this ticket.

## Demonstration

Render the external PostgreSQL example and run the scoped preflight, then demonstrate an authorized isolated installation with HTTPS discovery and a relayed connection. Show rejection of an intentionally incorrect DB hostname/CA.

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

- Claimed centrally on 2026-09-27 after ticket 11 resolution (`a9e941a`); assigned to a fresh isolated Go/Helm implementation agent. Local chart, schema, rendering and disposable database work can proceed without an external cluster. Authorized isolated installation and live ingress/network checks require operator-provided prerequisites; resolution awaits those and integrated review/evidence.

- Reviewed implementation commits `87c6224`, `2a121ab`, `c372f4e`, and `87606c6` were merged centrally as `3270c3d`. Independent Spec and Standards reviews reproduced malformed-host, unused-infrastructure, YAML-injection, and preflight-target defects; fixes and adversarial tests passed focused independent rechecks. On the merged tree with Helm 4.3.0, Go 1.27.1 darwin/arm64 and Docker 29.8.0: `bash scripts/test-chart.sh`, explicit-target static `bash scripts/preflight-external.sh charts/clipp-relay/examples/external-values.yaml isolated clipp-isolated`, `GOPROXY=off GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./...`, `go vet ./...`, `go build ./...`, and `bash scripts/smoke-postgres.sh` passed. The smoke used disposable verified-TLS/SCRAM PostgreSQL 18.6 and 17.11, tested wrong CA/hostname/role/schema and ownership rejection, and rejected PostgreSQL 16.15. Render and preflight checks did not contact a cluster. **Not run:** authorized isolated installation, actual F5/OCI/CNI/NSG/TLS/Secret/image/nofile verification, live HTTPS discovery/TCP/WSS/UDP and private-pressure/drain checks. The user forbids deployment; no cluster context or authorization was supplied. The ticket remains claimed and downstream tickets remain blocked. No push, publication, deployment or external load occurred.
