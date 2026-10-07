#!/usr/bin/env bash
# One public command for local and CI checks. This never publishes or deploys.
set -eEuo pipefail
cd "$(dirname "$0")/.."
profile=${1:-routine}
if [[ $# -gt 1 || "$profile" != routine && "$profile" != race ]]; then
  echo "usage: bash scripts/verify.sh [routine|race]" >&2
  exit 2
fi
check=prerequisites
trap 'result=$?; printf "FAIL %s: %s (exit %s)\n" "$profile" "$check" "$result" >&2; exit "$result"' ERR
require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required tool: %s; see docs/verification.md\n' "$1" >&2
    exit 2
  fi
}
for tool in bash git go gofmt mktemp rm; do require "$tool"; done
if [[ "$profile" == routine ]]; then
  for tool in helm python3 ruby jq sh grep sed awk cat cp mkdir chmod wc sort; do require "$tool"; done
else
  if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
    echo 'Race profile requires CGO_ENABLED=1 and a supported Go race platform; see docs/verification.md' >&2
    exit 2
  fi
  # CC can contain flags; Go itself validates the compiler invocation.
  read -r compiler _ <<< "$(go env CC)"
  require "$compiler"
fi
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
printf 'Verification profile: %s\nSource revision: %s\n' "$profile" "$(git rev-parse --verify HEAD 2>/dev/null || echo uncommitted)"
git --version
if [[ -n "$(git status --porcelain=v1)" ]]; then
  echo 'Working tree: dirty (revision alone does not identify these changes)'
else
  echo 'Working tree: clean'
fi
go version
if [[ "$profile" == routine ]]; then
  helm version --short
  python3 --version
  ruby --version
  jq --version
else
  printf 'Race platform: %s/%s; CGO_ENABLED=%s; CC=%s\n' "$(go env GOOS)" "$(go env GOARCH)" "$(go env CGO_ENABLED)" "$(go env CC)"
  "$compiler" --version
fi
printf "Bash %s\n" "$BASH_VERSION"
success() {
  printf '\nPASS %s\n' "$profile"
  if [[ "$profile" == routine ]]; then
    echo 'NOT RUN by routine: race (use bash scripts/verify.sh race)'
  else
    echo 'NOT RUN by race: routine profile (use bash scripts/verify.sh routine)'
  fi
  echo 'NOT RUN: real PostgreSQL smokes, native images, vulnerability scans, OCI/provider and live cluster qualification, live OAuth/device acceptance, forced live transfers; see docs/verification.md'
}
run() {
  check=$1
  shift
  printf '\nCHECK %s: %s\n' "$profile" "$check"
  "$@"
}
format() {
  local sources=() file unformatted
  local listing
  listing=$(mktemp)
  git ls-files --cached --others --exclude-standard -z -- '*.go' > "$listing"
  while IFS= read -r -d '' file; do
    if [[ -f "$file" ]]; then sources+=("$file"); fi
  done < "$listing"
  rm "$listing"
  if [[ ${#sources[@]} -eq 0 ]]; then echo 'No Go source files found' >&2; return 1; fi
  unformatted=$(gofmt -l -- "${sources[@]}")
  if [[ -n "$unformatted" ]]; then
    printf 'Go formatting differs; run gofmt on:\n%s\n' "$unformatted" >&2
    return 1
  fi
}
if [[ "$profile" == race ]]; then
  run 'Go race tests' go test -race -count=1 ./...
  success
  exit 0
fi
run 'Go formatting' format
run 'Go dependency verification' go mod verify
run 'Go tests' go test -count=1 ./...
run 'Go vet' go vet ./...
run 'Go build' go build ./...
for suite in \
  scripts/test-image-build.sh \
  scripts/test-image-smoke.sh \
  scripts/test-image-index.sh \
  scripts/test-image-values.sh \
  scripts/test-chart.sh \
  scripts/test-chart-bundled.sh \
  scripts/test-chart-test-network.sh \
  scripts/test-postgres-init-guard.sh \
  scripts/test-preflight-image-inputs.sh; do
  run "$suite" bash "$suite"
done
run 'scripts/test-isolated-test-chart.py' python3 scripts/test-isolated-test-chart.py
run 'scripts/test-install-metadata.py' python3 scripts/test-install-metadata.py
run 'scripts/test-verify.py' python3 scripts/test-verify.py
success
