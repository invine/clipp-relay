# External installation qualification

Ticket 18 remains incomplete. A Ready bundled test installation is useful test
infrastructure, but cannot satisfy an external PostgreSQL 17/18 installation gate.
The operator's decision to skip NetworkPolicy validation in the setup wizard
applies to that test procedure. Actual enforcement remains required by ticket 18
and the accepted deployment contract.

## Repeatable read-only inspection

Run this only against an already authorized isolated installation:

```sh
python3 scripts/inspect-install-metadata.py \
  --context <approved-context> --namespace <existing-namespace> \
  --release <existing-release>
python3 scripts/test-install-metadata.py
```

The inspector reads exactly the named relay StatefulSet, relay Pod and non-secret
configuration ConfigMap. It does not read Secrets or logs, execute commands in
Pods, change resources, or send traffic to the public relay. Kubernetes API read
permission is required. Each API read has a 30-second deadline. Missing resources,
wrong snapshot targets and invalid configuration return exit 2 with a generic
error; inspect failures privately.

To inspect previously captured API responses, pass `--snapshot snapshot.json`
instead of `--context`. The snapshot object has `statefulset`, `configmap` and
`pod` keys, each containing its original Kubernetes API object. Keep captures
private and outside Git. Reports identify whether they used a live API read or a
provided snapshot. Only selected non-secret facts appear in the report.

Exit 1 means metadata differs from the external serving contract. Exit 0 means
the checked declarations match, **not** that the installation is accepted.
`acceptance_complete` is always false: all runtime gates remain `NOT RUN` by this
inspector. Record independent runtime evidence separately. In particular, a
configured non-root/read-only Pod is not proof of its effective runtime controls;
an image digest reference is not image provenance; a Ready condition is not
public transport reachability or the private operations pressure test. The
inspector compares Pod image, resources, security, host access, container count,
ports, command/arguments, probes, volumes and mounts with the workload
template, verifies the Pod controller UID and intended StatefulSet revision when
available, and reports both template and Pod CPU requests so stale/mismatched Pods
cannot inherit a rendered resource claim. Snapshots without StatefulSet UID or
intended revision report ownership/revision as `NOT RUN`; a mismatched owner or
revision is a failure. Invalid metadata types and CPU quantities fail with a
generic error without echoing private input. Boolean values are not accepted as
integer fields. Kubernetes probe defaults, Secret/ConfigMap volume defaults and
the exact ordinary service-account token projection are normalized for comparison;
other projection or mount changes remain visible.

## Observed test installation, 2026-10-04

Read-only exact-target API snapshots were taken from `context-couryw6kqaa`,
namespace `relay-portal-test`, release `clipp-relay-test`. Private captures and the
JSON report are under `.local/managed-relay/artifacts/frontier-20261004/install`
in the primary checkout. No Secret contents were inspected and no cluster
resources were changed for this qualification.

The inspector returned exit 1 with these facts:

| Check | Observation | Ticket 18 interpretation |
| --- | --- | --- |
| Database mode | `bundled` | External installation gate remains unmet. |
| CPU request, StatefulSet and Pod | `300m` | Approved disposable test profile; canonical `1` CPU request is not demonstrated. |
| Memory/CPU limits and other resources | Declarative test settings only | Actual envelope and process nofile still require live evidence. |
| StatefulSet | One serving replica, OnDelete | Declared singleton behavior matches. |
| Pod | Ready; reviewed runtime declarations match template; owner UID and intended revision match | Momentary metadata observation. |
| Security | Non-root, read-only root, no escalation, dropped capabilities, RuntimeDefault seccomp; no host mounts/network/ports | Declared controls match; effective runtime controls remain unverified. |
| Probes | Private readyz startup/readiness and livez liveness use the specified timings | Probe declarations match. |
| Image | Immutable digest reference | Current image identity captured; multiarch provenance is a separate gate. |

The current image reference in that snapshot is
`ghcr.io/invine/clipp-relay@sha256:165f89029d4f84e79bbb40dbb44a68e715da1b0494cb95edde3c9d69c2286612`.
An earlier image-upgrade receipt for a different digest is historical evidence,
not an identity attestation for this current Pod.

## Remaining evidence

The existing `scripts/smoke-postgres.sh` and ticket 18's recorded local evidence
already cover disposable verified-TLS/SCRAM PostgreSQL 17/18 startup, exact schema,
wrong CA/hostname/role/schema and ownership rejection, and database-outage health.
That evidence is local fixture qualification. This continuation did not rerun
those already qualified cases because it changes no database or relay code.

The authorized external database installation on both supported majors is still
**NOT RUN**. Effective runtime security, serving privilege separation on that
target, resource/nofile/Go memory limits, actual F5/OCI/NSG/CNI behavior, default-deny
enforcement, public HTTPS discovery with relayed TCP/WSS/UDP, and private
operations budgets under public pressure remain **NOT RUN for ticket 18**.
Prior bundled test/runtime receipts can support their specifically tested
behaviors but do not substitute for these remaining external installation gates.
An enforcing network engine and external database prerequisites must be supplied
through a separately authorized operator procedure. No production rollout,
cluster infrastructure provisioning, database-mode handoff or pressure injection
is authorized by the metadata inspection command.

## Continuation verification

On Go 1.27.1 darwin/arm64, Helm 4.3.0, kubectl client 1.36.1 and Python 3.14.7:

- `python3 scripts/test-install-metadata.py`: PASS, eleven tests at the render/CLI
  seam, including resource mismatch and private-input rejection. Failure cases
  were observed before their implementation fixes.
- `GOPROXY=off GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./...`: PASS.
  Local loopback was enabled. This run supplied no real database fixture
  configuration, so optional database integrations were skipped; it does not
  replace the previously recorded real PostgreSQL smoke.
- Exact-target API capture plus snapshot inspection: PASS capture, expected
  qualification exit 1 for bundled mode and the 300m resource profile.
- No schema, relay runtime or chart resource changes were made. Broad chart and
  database smoke checks were not repeated for this metadata-only continuation.
