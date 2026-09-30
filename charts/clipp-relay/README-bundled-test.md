# Test-namespace PostgreSQL mode

`examples/bundled-test-values.yaml` illustrates an opt-in PostgreSQL 18 database in the **existing** `relay-portal-test` namespace. The chart refuses bundled rendering elsewhere. This test path keeps the external mode intact and is not a production database profile: it has no backup scheduler, certificate reload watcher, completed recovery procedure, or live OCI/CNI qualification. The example requests portal and WSS Certificates from the existing `letsencrypt-prod` ClusterIssuer. Its image digests, DNS names, StorageClass, and Secret names are placeholders. It creates no namespace, database Secret, issuer, image, or OCI resource.

## Required operator inputs

- Replace the PostgreSQL image example with an independently pinned, inspected Debian PostgreSQL 18 digest for the cluster architecture. The guard checks the binary major and stored `PG_VERSION`; minor and major image changes are deliberate maintenance, never a relay upgrade side effect.
- Supply an existing `adminSecret` (`username`, `password`), distinct serving and migration Secrets (same keys), a server TLS Secret (`tls.crt`, `tls.key`) and a CA Secret (`ca.crt` by default). The certificate must cover `<release>-clipp-relay-postgres.relay-portal-test.svc.<clusterDomain>`. The CA and durable passwords stay in Secrets, not values. The private key must be readable by PostgreSQL UID/GID 999 under the selected cluster's Secret volume behavior; verify this before install.
- Choose exactly one claim source. A chart-created claim needs an explicit StorageClass and size. Its `helm.sh/resource-policy: keep` and Argo `Prune=false,Delete=false` annotations protect normal chart/Argo removal, subject to controller behavior. An existing claim is only referenced. Verify the actual CSI reclaim policy, permissions, capacity, reattachment and deletion behavior. The read-only 2026-09-30 inspection found that `longhorn`, `longhorn-static`, `oci`, and `oci-bv` all use `Delete` reclaim; none is a safe substitute for the illustrative `longhorn-retain` value. A direct PVC or namespace deletion remains possible and is not a backup.
  A populated existing claim needs the release marker written during this chart's authorized initialization; the guard refuses arbitrary existing PostgreSQL data. A fresh existing claim may be initialized only with explicit first-install permission while the relay is stopped.
- Ensure a CNI actually enforces NetworkPolicy with this cluster's VCN-native Pods. PostgreSQL has only an internal ClusterIP Service and accepts only TLS/SCRAM host connections. Every network client uses hostname and CA verification. The bootstrap Job uses an existing admin Secret; the relay Pod never receives that credential.

## Deliberate first initialization and migration

1. Inspect the empty claim and all prerequisites, render `stopped` with `database.bundled.initialize=true`, and confirm the relay is stopped. Read-only preflight rejects a same-name existing PVC when first initialization would create a claim. The init container writes a release marker before `initdb`. Interrupted initialization, a missing marker, an empty replacement claim after initialization, or a wrong major fails closed for manual recovery. No chart operation erases or reinitializes data.
2. Set `initialize=false` and render `migrating` with a fresh `deployment.runId`. Inspect the ordinary named bootstrap Job and its result. It creates or verifies the database and distinct roles without resetting an existing password; mismatched credentials, serving DDL grants/ownership, or serving role membership fail. Do not reuse a failed Job name without inspecting the cause. Bootstrap does not run on serving restarts.
   The Job has Argo prune protection so its status remains available for inspection; remove old Jobs only after an explicit review.
3. Verify the old relay Pod is absent or fenced, then run the separate application migration with the migration Secret and the intended immutable relay image. Label that Job's Pods `app.kubernetes.io/name: clipp-relay-migration` and `app.kubernetes.io/instance: <release>`; the PostgreSQL NetworkPolicy allows that exact same-namespace identity on port 5432. The chart does **not** run the migration automatically. Verify schema revision 10 and the serving role's inability to create objects.
4. Render `serving` with `initialize=false` and an empty run ID. The relay process verifies the intended database, exact schema, TLS trust, supported major and serving privileges. The PostgreSQL StatefulSet stays at one replica through all relay phases and has an `OnDelete` update strategy.

Both a new and an existing claim require a deliberate data handoff for a mode or release change. Do not change values to point at empty storage as an upgrade or restore path. Password Secret replacement alone does not rotate an existing PostgreSQL role. Plan credential and certificate rotation as an outage with explicit database changes and verification.

## Local checks

```sh
bash scripts/test-chart.sh
bash scripts/test-chart-bundled.sh
bash scripts/preflight-bundled-test.sh charts/clipp-relay/examples/bundled-test-values.yaml isolated relay-portal-test
```

The preflight only renders locally unless an explicit context is passed. With a context it reads resource names and metadata; it never reads Secret contents or modifies the cluster. It leaves certificate authority, StorageClass retention, data contents, privileges, image provenance, network enforcement, and live startup as pending gates. The example values are deliberately not a deployable environment.
