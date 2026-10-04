## Resume after the recorded image, Chrome callback and public-NLB upgrades — 2026-10-04

The original setup values and installation fingerprint describe the completed migration, while the current serving release is revision 12. Its reviewed values are pinned separately at `/Users/invine/src/go/clipp-relay/.local/managed-relay/artifacts/clipp-relay-reviewed-serving-values.json`. This paragraph records the prior operator verification; it is not a fresh cluster check. The current image is `ghcr.io/invine/clipp-relay@sha256:165f89029d4f84e79bbb40dbb44a68e715da1b0494cb95edde3c9d69c2286612`, and the registered Chrome extension ID is `gkmcpbabbekiafkmkgjmdlmdlijhjdle`.

The wizard accepts those values only for the exact recorded serving target (`context-couryw6kqaa` / `relay-portal-test` / `clipp-relay-test`), after checking the pinned file hash and all installed Helm values. Original private values and migration fingerprints remain intact. Completed migration evidence is checked against its original image; live serving checks use the recorded current image, callback and public NLBs. Other drift still stops the wizard.

The matching deployment receipts are [Android callback image deployment](/Users/invine/src/js/clipp/.local/managed-relay/artifacts/android-callback-deploy/receipt.json) and [Chrome callback redeployment](/Users/invine/src/js/clipp/.local/managed-relay/artifacts/chrome-callback-deploy/receipt.md).

Resume client acceptance checks with:

```bash
bash /Users/invine/src/go/clipp-relay/scripts/operator/google-oauth-test-wizard.sh --start-at 16
```

Editing the wizard made no cluster changes. Live release, completed Job, current image and readiness checks were run separately as read-only verification.

> Current source lives on local `main` in `/Users/invine/src/go/clipp-relay` and `/Users/invine/src/js/clipp`. The wizard and chart-preparation helper are versioned under `scripts/`; diagnostics, reviewed serving values and recovery remain in the ignored `.local/managed-relay/` folders. The saved installation/migration baseline remains a5504aa. Later public-NLB changes mean the current chart is newer than that baseline; never rewrite historical fingerprints or rerun a completed migration to accommodate newer source. Secrets remain in ~/.config/clipp-relay.

# Clipp relay test wizard: where each value comes from

This is a test-only setup guide. Do not use production accounts, a production namespace, or placeholder values. The wizard stores non-secret answers and file paths in `~/.config/clipp-relay/google-relay-test.env`. Google and PostgreSQL credentials and the test database CA private key stay in mode-600 files outside the repositories.

**Current path (2026-10-03):** you approved an isolated test profile with **300m CPU requests for both the application migration Job and serving relay**. PostgreSQL remains at 400m. The wizard prepares a chart copy with that serving-request override, the PostgreSQL restart repair, and scoped ACME routing, and checks current CPU reservations before migration and serving. CPU limits and memory budgets retain their original values. The installed release, PVC, original chart and private values are preserved. The older capacity-increase proposal below is superseded for this path; do not apply it to run this test. Capacity qualification remains deferred.

## Resume directly at step 12

```bash
/Users/invine/src/go/clipp-relay/scripts/operator/google-oauth-test-wizard.sh --start-at 12
```

This reloads the saved setup without sourcing the env file as shell code. It verifies saved paths/hosts, the cluster context and namespace, and the nine required Secret names without reading their contents. It skips steps 1–11, starts the display at **12/19**, and retains chart fingerprint, capacity, release-resume, bootstrap, migration and serving checks. If saved values or prerequisites are missing, it stops with guidance to complete setup. Running without arguments still starts at step 1. `--help` prints usage.

## Public TLS issuance repair — 2026-10-03

Both `portal-tls-v1` and `wss-tls-v1` are absent. Their cert-manager HTTP-01 Challenges are Pending: requests to the ACME paths redirect to HTTPS, then fail with `tls: unrecognized name`. A public HTTP check reproduced the 301 on both hosts. F5 NGINX also selects the oldest ordinary Ingress when application and solver Ingresses share a host.

The isolated wizard chart now annotates both explicit Certificates with `acme.cert-manager.io/http01-override-ingress-name` set to their corresponding application Ingress and `cert-manager.io/issue-temporary-certificate: "true"`. This routes HTTP-01 through the existing Ingress and provides temporary TLS during issuance. [cert-manager documents the temporary-certificate fix for HTTP-01 TLS handshake failures](https://cert-manager.io/docs/troubleshooting/acme/), and [F5 documents host collisions](https://docs.nginx.com/nginx-ingress-controller/configuration/host-and-listener-collisions/).

Run the wizard with `--start-at 16` once bootstrap and migration are complete. At stage 16 it validates the two Certificate hosts, Secret names, issuer and Helm ownership. If repair is needed, it asks before annotating only those Certificates and deleting only their validated old Pending CertificateRequests so cert-manager recreates issuance with the new solver settings. Requests that are already Ready, unrelated requests and issued TLS Secrets are retained. It then waits for both Certificates to become Ready and checks trusted HTTPS. The shared issuer and controller configuration are unchanged. No live TLS repair has been applied by the agent.

## StatefulSet readiness check repair — 2026-10-03

The wizard previously called `kubectl rollout status` for both StatefulSets. Both intentionally use `OnDelete`, and that command rejects the strategy even when Pods are healthy. The wizard now waits for one Ready replica and the named Pod's Ready condition, then verifies owner UID, observed generation and the Pod's controller revision against the current StatefulSet template.

The corrected read-only check passed for PostgreSQL and relay at 1/1 Ready, with both running Pods at zero restarts. Both public Certificates are Ready. Trusted HTTPS returned 200 at the portal root and 404 at the WSS root; the latter establishes TLS reachability only, not a WebSocket transfer. Resume with `--start-at 16` to continue independent-network and account/client checks. No workload restart or cluster mutation was needed for this wizard fix.

## Resume directly at step 16

```bash
/Users/invine/src/go/clipp-relay/scripts/operator/google-oauth-test-wizard.sh --start-at 16
```

This skips stages 1–15 and starts the display at **16/19**. It reloads saved setup, verifies the original chart/values fingerprint and installed release, checks completed bootstrap and application migration evidence, and requires PostgreSQL to be Ready with first initialization disabled. It then continues the serving transition if needed, scoped TLS repair, live checks and client testing. It does not rerun installation, bootstrap or migration. If these prerequisites are incomplete, use `--start-at 12` instead. Both `--start-at 16` and `--start-at=16` are accepted.

## PostgreSQL restart repair — 2026-10-03

The missing `clipp_serving` log at 01:21:36 Dubai time preceded successful bootstrap completion at 01:21:43. The completed bootstrap verifies both serving and migration credentials. The current failure is a separate PostgreSQL startup error: the existing data directory has invalid permissions after the Pod remount.

The isolated test chart copy now sets `fsGroupChangePolicy: OnRootMismatch` and restores PGDATA to `0700` in the existing-data init path, after verifying the claim ownership marker and PostgreSQL major version. [Kubernetes describes its default recursive fsGroup permission changes and OnRootMismatch behavior](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#configure-volume-permission-and-ownership-change-policy-for-pods). CSI behavior can differ; the init guard enforces the PostgreSQL requirement independently.

Run the wizard with `--start-at 12`. After verifying the completed bootstrap, it compares the installed restart guard and policy with the repaired test chart. If missing, it asks before a Helm upgrade in the same migrating phase/run ID and a restart of only the test PostgreSQL Pod. The same PVC and existing database are retained. It waits for initialization and serving-login readiness before starting the application migration. No live repair has been applied by the agent.

## Scheduling checkpoint — 2026-10-03

At the initial inspection, `clipp-relay-test-clipp-relay-postgres-0` was Pending, has no assigned node, and has `PodScheduled=False / Unschedulable`: `3 Insufficient cpu`. Its initialization container has not started. The EOF from the main PostgreSQL log stream is not a PostgreSQL crash report.

Every node has 840m allocatable CPU. At inspection, their already reserved CPU and remaining unreserved CPU were:

| Node | Reserved | Unreserved |
| --- | ---: | ---: |
| `10.0.6.191` | 651m | 189m |
| `10.0.7.40` | 601m | 239m |
| `10.0.7.83` | 781m | 59m |

The database Pod requests 400m. The relay and its application migration Job each request 1000m, so neither can fit even an otherwise empty node of the current size. Kubernetes schedules requests against each node separately; the three nodes' CPU cannot be pooled into one Pod allocation. You selected increased cluster capacity while keeping these reviewed Pod budgets. The prepared procedure below adds two larger workers. These observations do not resolve the deferred capacity qualification ticket. The existing Helm release, Secrets and PVC are preserved.

### After Medusa removal — latest read-only check

No Medusa-named Pods remain. PostgreSQL is Running on `10.0.7.83`; its initialization container completed with exit code 0 and no restarts. PostgreSQL readiness is still pending. Each node still has **840m allocatable CPU**. Current reservations are 501m, 501m and 781m, leaving 339m, 339m and 59m respectively; PostgreSQL's 400m request is already included. The relay and application migration each still request **1000m**, exceeding every node's total allocatable CPU. Removing workloads cannot increase that per-node ceiling. The capacity plan has not been applied.

### Approved 300m isolated test profile

Both the generated application migration Job and serving relay now request **300m CPU**. CPU limits remain 2 CPU and memory remains 1 GiB requested / 2 GiB limited. PostgreSQL remains unchanged. The last read-only inspection found 339m unreserved on two nodes, enough for one 300m Pod by CPU reservation; migration completes before serving starts. This is a scheduling check, not a throughput measurement. Live performance still needs verification.

The wizard copies the canonical chart into a fresh temporary directory, applies the relay CPU request and the PostgreSQL restart repair. Its normal Helm transitions use that copy, and the generated migration Job requests 300m. The baseline install fingerprint continues protecting the original chart/values identity; explicit Job and StatefulSet resource checks enforce the approved override. No cluster mutation is performed by editing this wizard. Any existing migration Job with a different budget is refused rather than silently replaced.

## Superseded worker capacity proposal — not applied

The existing Infrastructure baseline plan on 2026-10-03 reports **No changes**. The saved full capacity plan reports **0 to add, 1 to change, 0 to destroy**: an in-place update of `oci_containerengine_node_pool.worker_pool["v1.36.1"]` only. Its only changed fields are pool size **3 → 5** and new-worker OCPUs **1 → 2**; memory stays **6 GiB**. Machine comparison confirmed all other fields and outputs are unchanged.

This adds two new ARM64 workers and retains the existing three. The pool is in `prod-cluster`, region `uk-london-1`. Existing workers remain 1 OCPU. This is a Basic cluster; no node cycling, cordon, drain or deletion is part of this procedure. [OCI documents that worker property changes apply to new workers and that placement changes can replace existing workers](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengscalingnodepools.htm); this plan keeps all placement settings unchanged.

### Review and apply in a second terminal

Only proceed if you accept the additional compute/boot-volume cost and OCI can provision the workers. The stopped test release may automatically start its PostgreSQL initialization as capacity appears. Relay replicas remain zero until the later serving transition.

```bash
cd /Users/invine/src/oracle-cluster/local-oci-cluster-infra
umask 077
# Review the saved full plan locally. Do not share raw state or plan JSON.
./scripts/terraform-local -chdir=infra show -no-color /private/tmp/clipp-relay-worker-capacity.tfplan
# Must print exactly this SHA-256 before applying:
shasum -a 256 /private/tmp/clipp-relay-worker-capacity.tfplan
# 00a2900686f1f6fd89e1671605447010ff67302a83d47253533f90fad9f42a3e

# Preserve the authoritative state and both current input files outside Git.
capacity_backup=$(mktemp -d "$HOME/.config/clipp-relay/capacity-before.XXXXXX")
cp terraform.tfstate infra.tfvars gate1-worker.tfvars "$capacity_backup/"
# Save the desired inputs BEFORE apply, including recovery after partial failure.
cp /private/tmp/clipp-relay-worker-capacity.tfvars "$capacity_backup/desired-worker.tfvars"

# This is the shared-cluster mutation. Execute only after reviewing the plan above.
./scripts/terraform-local -chdir=infra apply /private/tmp/clipp-relay-worker-capacity.tfplan
```

If apply succeeds, preserve the accepted sizing in the existing private worker overlay so a future plan cannot silently shrink the pool. The backup above contains its prior contents:

```bash
cp /private/tmp/clipp-relay-worker-capacity.tfvars gate1-worker.tfvars
chmod 600 gate1-worker.tfvars
./scripts/terraform-local -chdir=infra plan -input=false -var-file=../infra.tfvars -var-file=../gate1-worker.tfvars
# Require No changes before proceeding.
kubectl --context context-couryw6kqaa get nodes
```

Future Infrastructure plans/applies must include **both** `infra.tfvars` and `gate1-worker.tfvars`. The worker overlay now records size 5, 2 OCPUs and 6 GiB for future workers. No tracked Infrastructure file needs to change.

If apply fails or Terraform says the saved plan is stale, stop and inspect the failure privately. Keep the backup and desired overlay; a partially successful OCI operation can still have changed the pool. Do not apply the old size-3 baseline as rollback or rerun unrelated Terraform changes. Prepare a fresh full plan with `-var-file=../infra.tfvars -var-file=/private/tmp/clipp-relay-worker-capacity.tfvars`; require only the remaining expected pool update, then review before retrying. If repository/input files changed after this plan was prepared, replan before any apply.

Wait for all five nodes to become Ready. The wizard verifies at least two schedulable ARM64 nodes have >=1000m actual allocatable CPU, then resumes the same test release/PVC. This proves the required minimum node size; successful Pod scheduling remains the check for available CPU, memory, storage and other constraints. Keep the deferred capacity qualification ticket open.


## Google OAuth fields (stages 2–4)

| Wizard field | Where to get it | What to enter |
| --- | --- | --- |
| Google Cloud project ID | In [Google Cloud Console](https://console.cloud.google.com/), select or create the test project with the project picker at the top. The picker shows **Project ID**. | The exact ID, such as `clipp-relay-test-123`. Do not use the project display name or numeric project number. |
| Administrator test email | The Google account you will use to administer the test relay. Add this identity to the Google Auth Platform test audience. | Its real sign-in email. The wizard puts it in the local administrator allowlist. |
| Ordinary test account email | A second Google account that you control. Add it to the same test audience. | Its real sign-in email. This account starts Pending in the relay and is approved later. |
| Google Web client ID | [Google Auth Platform → Clients](https://console.cloud.google.com/auth/clients) → **Create client** → **Web application**. | The new client ID ending in `.apps.googleusercontent.com`. |
| Google Web client secret | The same new Web client, immediately after creation. | Paste into the wizard's hidden prompt. Google shows this secret only at creation. Do not paste it into chat or a repository. |

### Exact order on the Google screens

1. Select the **test project** in the project picker. If you must create one, use a distinct project and copy its **Project ID** after creation.
2. Open [Branding](https://console.cloud.google.com/auth/branding). Register the app name and required support/contact details. Add the registrable domain of your chosen portal host to **Authorized domains** before you add its redirect URI.
3. Open [Audience](https://console.cloud.google.com/auth/audience). Use **External / Testing** for two ordinary Google accounts, or **Internal** only if both accounts belong to the authorized Workspace organization. Add both sign-in emails as test users when using External / Testing.
4. Open [Clients](https://console.cloud.google.com/auth/clients). Create one **Web application** client for the relay server. Under **Authorized redirect URIs**, add the exact URI printed by the wizard: `<portal origin>/auth/callback`. For example, if the portal origin is `https://relay-portal-test.example.com`, the redirect is `https://relay-portal-test.example.com/auth/callback`. Replace the example domain with your real one. The scheme, host, path, and trailing slash must match exactly.
5. Keep the creation dialog open. Copy its client ID and client secret into the wizard. If the secret is lost before it reaches the local file, create or reset a client secret in Google and use the new matching value; the old value cannot be viewed again.

The portal host must be a real public DNS name under a domain you control. Google does not provide the host. The relay's Google callback cannot be registered against a made-up example host for this external test.

## Other wizard fields

| Field | Source |
| --- | --- |
| Portal origin and WSS hostname | Reserve two distinct names under a public domain you control. Your DNS or cluster operator points both to the isolated F5 ingress and prepares matching TLS. Enter `https://` plus the portal host for the origin; enter only the bare second host for WSS. |
| Kubernetes context | `kubectl config get-contexts -o name` lists saved local names. Choose your existing OCI cluster; this test uses a dedicated namespace on it. |
| Kubernetes namespace | The operator's existing test-only namespace. `kubectl --context <context> get namespaces` lists names if you have read access. |
| Helm release name | A name you choose for this test installation, such as `clipp-relay-test`. Check `helm --kube-context <context> -n <namespace> list` for conflicts. |
| Chrome extension ID | Build and load the isolated unpacked extension, then copy the 32-letter ID shown on `chrome://extensions`. The wizard derives its relay callback. |
| OCI journal env and config | The completed journal wizard wrote `~/.config/clipp-relay/oci-test-journal.env` and `oci-test-journal-config.json`. The relay wizard reads the journal configuration and fills its chart values. |
| Four application Kubernetes Secrets | The wizard proposes `clipp-google-v1`, `clipp-admin-v1`, `clipp-peppers-v1`, and `clipp-journal-v1`. It leaves existing objects untouched and offers to create only missing ones after you confirm the context and namespace. |
| Clipp and Go checkout paths | Existing isolated integration worktrees. The wizard offers known paths and checks that they exist. |

## Chart values that an operator must supply (stage 7)

The wizard fills the hosts, callbacks, four Secret names, and journal identity/floor when it first copies the example. It leaves the following values for the isolated cluster operator. The chart example is **not deployable** as written.

| Values group | Source |
| --- | --- |
| `image.repository`, `image.digest` | GitHub Actions + GitHub Container Registry are the selected CI path. The manual `relay-image.yml` workflow targets `ghcr.io/<owner>/clipp-relay` by default and emits a `relay-image-values.yaml` artifact. The local target is `ghcr.io/invine/clipp-relay`; a published relay digest remains required. |
| `public.portalTLSSecret`, `public.wssTLSSecret`, `public.ingressClass` | Use two distinct Secret names reserved in the test namespace; the chart's Let’s Encrypt overlay now requests these Secrets through cert-manager. The installed F5 NGINX IngressClass is `nginx`. Verify both DNS hosts reach it. |
| `database.*` | Bundled PostgreSQL 18 in `relay-portal-test`, with an explicit image digest, storage choice, database name, internal DNS domain, and five credential/TLS/CA Secret references. The wizard reads the five exact names, creates separate local credentials and an internal TLS certificate, then offers to create only missing Secret objects. The user accepts existing Longhorn/Delete storage for this disposable test. No passwords go in values. |
| `oci.*` | The private subnet OCID comes from the local infrastructure state. This test uses `internal: true`, `securityRuleManagementMode: None`, and empty NSG OCIDs, as requested. Existing security lists remain operator-managed. |
| `networkPolicy.*` | Leave the reviewed test values as configured. This wizard skips NetworkPolicy enforcement validation because the test does not use it. The chart still renders policy objects, so do not rely on them for isolation. |

Use the existing cluster's dedicated `relay-portal-test` namespace. The wizard can create missing Secret objects and install the reviewed test chart after explicit confirmations. It does not create cloud network resources. Existing Delete storage and skipping NSGs are approved test choices, not blockers to local chart validation. The wizard skips NetworkPolicy enforcement validation as requested; this does not prove Pod isolation. Live connectivity remains a separate check after installation.

## PostgreSQL Secret setup in the updated wizard

After you review the bundled values file, the wizard renders the chart to get the exact internal PostgreSQL Service name. It then generates three independent random passwords for `postgres`, `clipp_serving`, and `clipp_migration`. The files are under `~/.config/clipp-relay/managed-relay-test/postgres/`. A rerun reuses existing passwords; it does not rotate them silently.

The wizard creates a test-only certificate authority and a PostgreSQL server certificate with that Service name in the certificate's DNS Subject Alternative Name. It verifies the name, trust chain, key match and remaining validity. The CA private key stays local. The server Secret contains `tls.crt` and `tls.key`; the trust Secret contains only `ca.crt`. This internal database certificate is separate from the public Let’s Encrypt portal and WSS certificates.

After you confirm the exact Kubernetes context and `relay-portal-test` namespace, the wizard creates missing `pg-admin-v1`, `clipp-db-serving-v1`, `clipp-db-migration-v1`, `pg-server-tls-v1`, and `clipp-db-ca-v1` objects. It never replaces an existing Secret. If one already exists, it asks you to confirm that it matches the reviewed local files. The read-only chart preflight follows this step. The wizard does not read Secret contents back from Kubernetes.

The wizard now runs the chart sequence itself. It fingerprints the chart commit and private values, checks for an old relay Pod, confirms all cluster nodes are Ready, installs the stopped phase with a new database claim, and waits for initialization. It upgrades to migrating with `initialize=false`, waits for the chart's bootstrap Job, then restarts only the test PostgreSQL Pod to adopt that flag. It builds an isolated application migration Job from the rendered chart using the pinned relay image, ConfigMap, migration credential and database CA; verifies its completion record at schema revision 10; and upgrades to serving. Existing matching phases resume on rerun, while changed values, failed releases, mismatched Jobs and uncertain old Pods stop for review. The wizard asks before the initial install, schema migration Job and serving transition.

After installation, the wizard checks both public cert-manager Certificates are Ready, the installed database PVC is Bound, the PostgreSQL and relay StatefulSets are each 1/1 Ready, and both hosts answer with trusted HTTPS. Then it guides the ordinary account approval and paired-client transfer. An installed release bypasses the first-install PVC collision check on reruns. These checks do not prove NetworkPolicy enforcement or backup readiness.

### Current checkpoint (read-only inspection, 2026-09-30)

The saved Go checkout and earlier external-mode private values file exist. A separate mode-600 bundled-test values file was prepared at `/Users/invine/.config/clipp-relay/managed-relay-test/bundled-test-values.yaml`; the original external file was not changed. The updated wizard offers this bundled path. It carries the earlier portal, callback, application Secret-name, and OCI journal answers; image digests, PostgreSQL Secret names/storage, and OCI network IDs still require real values.

The read-only bundled preflight renders all three relay phases and finds the existing `nginx` IngressClass and `letsencrypt-prod` ClusterIssuer. It stops at the first missing database Secret, `clipp-db-ca-v1`. This is an expected prerequisite, not evidence that the other proposed database Secrets or storage are ready.

The selected `relay-portal-test` namespace exists, and the `nginx` IngressClass reports `nginx.org/ingress-controller`. The namespace currently contains only the four wizard-created application Secret names: `clipp-admin-v1`, `clipp-google-v1`, `clipp-journal-v1`, and `clipp-peppers-v1`. No TLS, PostgreSQL serving, PostgreSQL migration, or database CA Secret name was listed. No Service or Ingress was listed in that namespace. This inspection read resource names and types only; it did not read Secret contents or change the cluster.

The example `portal-tls-v1` and `wss-tls-v1` entries are proposed names for cert-manager to create after a later authorized installation; they must be distinct. The `clipp-db-serving-v1`, `clipp-db-migration-v1`, `clipp-db-ca-v1`, `pg-admin-v1`, and `pg-server-tls-v1` entries were proposed names at this checkpoint, not evidence of Secrets. Do not advance past the Stage 7 pause with example image, storage, database, or network IDs.

The saved context points to an active OKE cluster named `prod-cluster`, with three ARM64 nodes. It has F5 NGINX Ingress, cert-manager, a Ready `letsencrypt-prod` ClusterIssuer using HTTP-01 through the `nginx` IngressClass, and OCI VCN-native pod networking. The existing test namespace can host separate relay resources, but it does not provide a separate cluster failure or security boundary.

No Calico, Cilium, Antrea, or other NetworkPolicy engine was found in the cluster workload or CRD inventory. Oracle's OKE documentation states that OCI VCN-native pod networking needs an additional network-policy engine for Kubernetes NetworkPolicy enforcement. Do not treat the rendered relay NetworkPolicy as enforced until this is installed and tested. OCI lists a public and a private subnet in the cluster VCN; no suitable existing NSGs were returned for the relay NLB. The chart's TCP/UDP Services require explicit subnet and frontend/backend NSGs, so those inputs cannot currently be copied from an existing relay resource. Read-only StorageClass inspection found `longhorn`, `longhorn-static`, `oci`, and `oci-bv` all have `Delete` reclaim, so none satisfies the retained test database requirement.

The Go relay checkout has no configured Git remote. The canonical local integration branch now contains the manual GitHub Actions + OCIR image workflow, an opt-in chart-managed PostgreSQL test mode alongside external mode, and cert-manager Certificate support. The wizard uses the bundled-test example plus Let’s Encrypt overlay. The PostgreSQL test path still lacks backups and a certificate reload watcher, and it must not be treated as a completed public-serving installation. No image was published, and no cluster resource was changed during this inspection.

### Updated checkpoint — 2026-10-01

This checkpoint supersedes the older Retain/NSG requirements for this operator-approved disposable test.

- **TLS:** `portal-tls-v1` and `wss-tls-v1` are distinct. Read-only checks found `nginx.org/ingress-controller`, a Ready `letsencrypt-prod`, and both configured DNS hosts resolving to the existing ingress IP `130.162.161.246`. Missing certificate Secrets before chart installation are not a name conflict; cert-manager will request them when the Certificates exist. Certificate issuance and HTTPS still need verification after installation.
- **Relay image:** the canonical integration checkout's workflow now targets GHCR, defaulting to `ghcr.io/<owner>/clipp-relay`, using the job's `GITHUB_TOKEN`. The private values target `ghcr.io/invine/clipp-relay`. The queried `latest` tag was not found, and no publish artifact is available locally. `image.digest` is deliberately empty rather than retaining the example hash. A real workflow-produced digest remains required; the relay checkout has no configured Git remote, so publication has not been performed here.
- **PostgreSQL:** the private values now pin Docker Official Image `postgres:18-bookworm`, reported as `18.6-bookworm`, at multi-platform index digest `sha256:3725f4e2499eef5134592b3b4ab79a543ed7f8e533b05b5b637af926630f6650`. The inspected index includes `linux/arm64/v8` manifest `sha256:4c6516b5d6dfd96a6888541396f76545a63290be0aec2542547d7bcdd7515e28`. Registry metadata was inspected; the image was not run or cryptographic provenance verified.
- **Storage:** the namespace has no PVC. Values keep the chart's 10 GiB new-claim configuration but use the installed `longhorn` class with its existing `Delete` reclaim policy, which the user explicitly accepted. No StorageClass or PVC was created, replaced, or deleted.
- **OCI:** values use `prod-private-subnet` (`10.0.4.0/22`), read from `~/src/oracle-cluster/local-oci-cluster-infra/terraform.tfstate`. `oci.internal: true` requests private TCP/UDP NLBs. `oci.securityRuleManagementMode: None` and empty NSG values omit NSG configuration and automatic security-rule management. Portal/WSS keep their existing public ingress path. TCP/UDP require private-network reachability.
- **Initialization:** values now use `deployment.phase: stopped` with the existing first-initialization flag. This fixes the actual template error caused by `serving` plus `initialize: true`; it does not initialize anything by itself.
- **Still missing:** the five database Secret resources `pg-admin-v1`, `clipp-db-serving-v1`, `clipp-db-migration-v1`, `pg-server-tls-v1`, and `clipp-db-ca-v1`. Metadata-only inspection found only the four application Secrets. NetworkPolicy enforcement remains unverified; skipping OCI NSGs does not resolve that separate issue.
- **Validation:** existing chart tests, bundled PostgreSQL tests, the new private/no-NSG render test, image helper checks, and workflow structure checks pass locally. The real values correctly fail direct rendering until an actual relay digest is supplied. No chart was installed and no cluster resources were changed.
- **Wizard preflight correction:** removed the duplicate standalone `helm lint` invocation that omitted `relay-portal-test` and misleadingly reported zero failed charts despite template errors. Preflight now checks the relay image repository/digest before Helm, respects values-overlay precedence, and runs its lint/render calls with the test namespace. Regression checks pass. With the current private values it stops at the missing relay digest before any cluster checks. The GitHub repository hosting the Go relay workflow still needs to be identified; a GHCR path alone does not establish that repository or publish an image.

### GitHub repository checkpoint — 2026-10-01

The user created `https://github.com/invine/clipp-relay` and explicitly approved publication of the full checkout and history, including internal design/implementation documents. The public repository now contains the integration checkout on `main`; the local checkout uses that URL as `origin`. Initial setup commit: `c5d781c53afa7f10422882a4b2ac3ce39438d2c3`. A follow-up CI fixture fix, `a5504aaa7a8456e8d75fc13a8aaa29ed1c2cff01`, clears the inherited builder variable for the negative missing-pin test.

Registry inspection resolved the Go builder to `golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195`, including AMD64 and ARM64 manifests. GitHub Actions is enabled and the repository builder variable is configured. Main is protected against force pushes and deletion, including for administrators. The `relay-image-publish` environment permits protected branches and requires reviewer `invine`. The user's explicit publication approval was applied to the environment gate after verification and both native smoke tests passed.

### Successful image publication and current preflight — 2026-10-01

This checkpoint supersedes the earlier missing-remote and missing-relay-digest blockers.

- [Workflow run 36817882167](https://github.com/invine/clipp-relay/actions/runs/36817882167) completed successfully for source commit `a5504aaa7a8456e8d75fc13a8aaa29ed1c2cff01`. Go dependency verification, unit tests, vet, native AMD64/ARM64 image smoke checks, publication, and registry-index verification passed.
- Artifact `relay-image-values-36817882167-1` (ID `11142696197`) supplied `ghcr.io/invine/clipp-relay@sha256:6ca0bb334ec42ee3cf5618eee6a994449fcf7d6fb3d6e9ae9b023c7be48f9fdd`. Its values fragment is saved at `/private/tmp/clipp-relay-published-image-values-36817882167.yaml`.
- Anonymous GHCR manifest retrieval independently confirmed that exact digest and Linux AMD64/ARM64 entries. Cluster-node image startup remains a later live check.
- Only `image.repository` and `image.digest` were updated in the private bundled values. A mode-600 backup is saved alongside them with suffix `.before-relay-image-36817882167`.
- The real values now pass chart/schema rendering for initial/stopped/migrating/serving. Live read-only preflight confirms the existing ingress and issuer names, then stops at missing `clipp-db-ca-v1`.
- Metadata-only inspection reconfirmed that all five database Secrets remain absent: `pg-admin-v1`, `clipp-db-serving-v1`, `clipp-db-migration-v1`, `pg-server-tls-v1`, and `clipp-db-ca-v1`. NetworkPolicy enforcement remains unverified. No chart was installed and no cluster resources were created or changed.

The original private values are preserved at `~/.config/clipp-relay/managed-relay-test/bundled-test-values.yaml.before-20261001`. Both files have mode 600. The earlier external values file remains untouched.

## Sources

- [Google Cloud project IDs](https://docs.cloud.google.com/resource-manager/docs/creating-managing-projects)
- [Google OAuth client creation and one-time secret display](https://support.google.com/cloud/answer/15549257)
- [Google OAuth branding and authorized domains](https://support.google.com/cloud/answer/15549049)
- [Google OAuth audience and test users](https://support.google.com/cloud/answer/15549945)
- [Google web-server OAuth redirect matching](https://developers.google.com/identity/protocols/oauth2/web-server)
- [OKE: Calico and Kubernetes NetworkPolicy enforcement](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengsettingupcalico.htm)
- [OKE: NLB subnet and NSG annotations](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingnetworkloadbalancers.htm)
- Relay chart: `/Users/invine/src/go/clipp-relay/charts/clipp-relay/README.md`
- [Docker Official PostgreSQL image](https://hub.docker.com/_/postgres) — digest/architecture inspection used Docker Registry via `docker buildx imagetools inspect`.
- [GitHub Container Registry authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [OKE internal NLB and security-rule management annotations](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm)
