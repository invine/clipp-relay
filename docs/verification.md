# Relay verification

Run the same entry point used by ordinary pull-request and main-branch CI:

```sh
bash scripts/verify.sh
# Equivalent explicit profile:
bash scripts/verify.sh routine
# Separate race result:
CGO_ENABLED=1 bash scripts/verify.sh race
```

Run from any directory using the script's path. A missing tool, formatting diff
or failed constituent check exits unsuccessfully and names the failing check.
Formatting is checked without rewriting files. Go commands use `-mod=readonly`;
setup may fetch dependencies, but verification cannot silently change go.mod.
The command prints the source revision, whether the checkout is dirty, tool
versions and separate checks it did not run. Record those with the profile and
exit status when retaining evidence.

## Tools and deterministic coverage

Use Go **1.27.1 or newer**, matching `go.mod`, with its matching gofmt. Use Bash
3.2 or newer, Git, Helm **4.3.0**, Python **3.12 or newer**, Ruby **2.6 or newer**
with the standard `yaml` and `json` libraries, jq **1.6 or newer**, and standard
POSIX shell utilities (sh, grep, sed, awk, cat, cp, mkdir, mktemp, chmod, rm, wc,
sort). These are local tools; no Docker daemon, PostgreSQL server, kubectl,
kubeconfig or service account is required. The routine workflow provisions Go
from go.mod, Helm 4.3.0 from its checksum-verified distribution, and Python, Ruby
and jq from Ubuntu 24.04. The command's output records the installed versions.

The routine profile runs these checks in order and stops at the first failure:

| Check | Coverage / prerequisites beyond Bash and shell utilities |
| --- | --- |
| Go formatting | Git lists tracked and nonignored new Go files; gofmt reports differences, including filenames with spaces. |
| `go mod verify` | Go module cache checksums. Dependency fetching during setup needs network access. |
| `go test -count=1 ./...` | Existing Go suites; uncached execution. Optional database integrations retain their fixture prerequisites. |
| `go vet ./...` | Existing Go static checks. |
| `go build ./...` | Existing Go packages and commands. |
| `scripts/test-image-build.sh` | Docker subprocess fixture; no real image build. |
| `scripts/test-image-smoke.sh` | Docker subprocess fixture; no real container execution. |
| `scripts/test-image-index.sh` | Docker subprocess fixture and jq; no registry inspection. |
| `scripts/test-image-values.sh` | jq; exact manifest-digest values contract. |
| `scripts/test-chart.sh` | Helm and Ruby; external render, rejection and preflight fixtures. Also invokes the bundled chart suite. |
| `scripts/test-chart-bundled.sh` | Helm and Ruby; bundled render and kubectl preflight fixtures. Explicit invocation keeps the inventory complete. |
| `scripts/test-chart-test-network.sh` | Helm and Ruby; network annotation and rejection fixtures. |
| `scripts/test-postgres-init-guard.sh` | PostgreSQL/initdb subprocess fixtures; no database server. |
| `scripts/test-preflight-image-inputs.sh` | Ruby and a Helm subprocess fixture; checks image-input diagnostics. |
| `scripts/test-isolated-test-chart.py` | Python, Helm and Ruby; temporary chart preparation and initialization fixtures. |
| `scripts/test-install-metadata.py` | Python, Helm and Ruby; rendered installation metadata / inspection CLI fixtures. |
| `scripts/test-verify.py` | Python, Git, Ruby; public command failure propagation, prerequisite and workflow configuration checks. |

The **race** profile runs `go test -race -count=1 ./...` separately. It requires
`CGO_ENABLED=1`, a working C compiler selected by `go env CC`, and a platform
supported by Go's race detector. CI uses Linux amd64 on Ubuntu 24.04 with its
C compiler. A routine pass does not imply a race pass. Race compilation may need
a writable Go cache, dependency access and local loopback for the existing tests.

## Acceptance and publication boundaries

Both profiles are local verification. Real PostgreSQL 17/18 TLS/SCRAM smokes,
real native AMD64/ARM64 image builds and execution, dependency/image vulnerability
scans, OCI journal/provider qualification, live cluster/network/security checks,
OAuth accounts, device instrumentation and forced live relay transfers remain
**not run** by this entry point. Follow [image build and publication](image-build.md),
[installation qualification](install-qualification.md), and the
[operator procedure](operator/README.md) for their separate environments and gates.

`.github/workflows/verify.yml` uses pull-request and main push events with only
`contents: read` and checkout credential persistence disabled. It has separate
routine and race jobs invoking the public command above. It has no publication
Environment, registry login, signing key, kubeconfig or live OAuth credentials.
The manual `.github/workflows/relay-image.yml` publication workflow retains its
independent protected-ref / Environment authorization. Hosted verification is
**not run** merely because the workflow has been prepared and checked locally;
it requires a separately authorized push to trigger a hosted run.
