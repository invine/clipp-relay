# Relay image build

The relay image contains only the static Go executable and public CA roots. The
portal HTML and assets are compiled into the executable with `go:embed`.
`Dockerfile` runs it as UID/GID 10001, matching the Helm chart's non-root,
read-only root filesystem. It has no startup build, shell, package manager or
embedded credentials.

## Local build

From the repository root, with Docker Buildx available:

```sh
bash scripts/build-relay-image.sh --local arm64 local/clipp-relay:arm64-dev
bash scripts/smoke-relay-image.sh arm64 local/clipp-relay:arm64-dev

bash scripts/build-relay-image.sh --local amd64 local/clipp-relay:amd64-dev
bash scripts/smoke-relay-image.sh amd64 local/clipp-relay:amd64-dev
```

Each build loads one architecture into the local Docker image store. It does
not publish it. Run the matching smoke check on a native host; it verifies the
image's architecture and non-root UID, then starts the image read-only with no
network, capabilities or configuration and expects a fail-closed configuration
error. Full serving requires the external PostgreSQL and Secret configuration
described in the chart. Cross-architecture execution requires emulation. The
local command defaults to the `golang:1.27.1-bookworm` build stage; set
`GO_BUILDER_IMAGE` to a verified
`golang:1.27.1-bookworm@sha256:...` reference to reproduce a specific builder.

## Manual OCI Container Registry publication

Assumption: this source will be hosted in GitHub and the image will be stored in
OCI Container Registry (OCIR). The relay repository currently has no Git remote
or existing CI. `.github/workflows/relay-image.yml` is `workflow_dispatch`
only, accepts a manually selected protected ref, and uses the
`relay-image-publish` GitHub Environment. Configure required reviewers on that
Environment before adding credentials. Before registry login, a separate job
runs the image-script checks plus `go mod verify`, `go test ./...`, and
`go vet ./...`, then native `ubuntu-24.04` and `ubuntu-24.04-arm` jobs build and
smoke their respective images. A failure prevents the publish job. The publish
job builds one Linux AMD64/ARM64 image index. It never changes Helm values in
Git, deploys, runs migrations, or changes OCI infrastructure.

Provision the target OCIR repository and publisher identity separately. Set:

| GitHub setting | Value |
| --- | --- |
| Repository or Environment variable `RELAY_IMAGE_REPOSITORY` | Full lower-case OCIR repository path, such as `iad.ocir.io/namespace/clipp-relay`; no tag or digest. |
| Repository variable `RELAY_GO_BUILDER_IMAGE` | Verified digest-pinned `golang:1.27.1-bookworm@sha256:...` builder reference, available to every verification job. |
| Environment secret `OCIR_USERNAME` | OCIR login name for the publisher identity, including tenancy namespace and identity-domain component if required. |
| Environment secret `OCIR_AUTH_TOKEN` | OCI auth token for that identity. |

Use a publisher identity with only the repository permissions needed to push.
The chart's `image.pullSecrets` references an independently provisioned pull
Secret when the repository is private. No registry credentials enter the image
or Helm values.

Each run pushes a tag containing the source commit and GitHub run ID/attempt.
Buildx writes its post-push `containerimage.digest` to a metadata file. The
workflow validates that digest and confirms that the registry index contains
both Linux architectures. It uploads the exact `image.repository` /
`image.digest` YAML fragment as a 30-day workflow artifact, prints it in the
run summary, and exposes `image_repository` and `image_digest` as job outputs. Record
those exact values, the source commit, and the run as release evidence. The
digest is the registry manifest or index digest, not the binary hash or
`containerimage.config.digest`. Merge the fragment with the chart's other
values only after the separate deployment and release gates. The same pinned
digest must be used for serving and schema migration.

The native smoke checks cover image startup and failure isolation only. They
do not exercise a real database, portal, TLS, relayed traffic or an OCI node.
The accepted release specification also requires executed ARM64/AMD64 image
integration, dependency and image vulnerability scans, fault/operation tests,
and a release manifest with evidence. Those remain unverified release gates;
this workflow does not claim release qualification from a successful build.

Local checks that do not contact Docker or OCI:

```sh
bash scripts/test-image-build.sh
bash scripts/test-image-smoke.sh
bash scripts/test-image-index.sh
bash scripts/test-image-values.sh
```
