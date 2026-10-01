#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$FAKE_DOCKER_ARGS"
SH
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" FAKE_DOCKER_ARGS="$tmp/args"

assert_arg() {
  if ! grep -Fxq -- "$1" "$FAKE_DOCKER_ARGS"; then
    echo "missing docker argument: $1" >&2
    exit 1
  fi
}
reject_arg() {
  if grep -Fxq -- "$1" "$FAKE_DOCKER_ARGS"; then
    echo "unexpected docker argument: $1" >&2
    exit 1
  fi
}

bash "$root/scripts/build-relay-image.sh" --local arm64 local/clipp-relay:test
assert_arg '--platform=linux/arm64'
assert_arg '--load'
assert_arg 'local/clipp-relay:test'
reject_arg '--push'

rm "$FAKE_DOCKER_ARGS"
bash "$root/scripts/build-relay-image.sh" --local amd64 local/clipp-relay:test
assert_arg '--platform=linux/amd64'
assert_arg '--load'

rm "$FAKE_DOCKER_ARGS"
if bash "$root/scripts/build-relay-image.sh" --local ppc64le local/clipp-relay:test; then
  echo 'unsupported image architecture was accepted' >&2
  exit 1
fi
test ! -e "$FAKE_DOCKER_ARGS"

if bash "$root/scripts/build-relay-image.sh" --local arm64 'bad image'; then
  echo 'invalid image reference was accepted' >&2
  exit 1
fi
test ! -e "$FAKE_DOCKER_ARGS"

# CI supplies a real builder pin; this negative case must clear it explicitly.
if GO_BUILDER_IMAGE='' bash "$root/scripts/build-relay-image.sh" --push iad.ocir.io/ns/clipp-relay:run "$tmp/result.json"; then
  echo 'push without pinned builder was accepted' >&2
  exit 1
fi
test ! -e "$FAKE_DOCKER_ARGS"

GO_BUILDER_IMAGE="golang:1.27.1-bookworm@sha256:$(printf 'a%.0s' {1..64})" \
  bash "$root/scripts/build-relay-image.sh" --push iad.ocir.io/ns/clipp-relay:run "$tmp/result.json"
assert_arg '--platform=linux/amd64,linux/arm64'
assert_arg '--push'
assert_arg "--metadata-file=$tmp/result.json"
assert_arg '--build-arg=GO_BUILDER_IMAGE=golang:1.27.1-bookworm@sha256:'"$(printf 'a%.0s' {1..64})"
reject_arg '--load'

GO_BUILDER_IMAGE="golang:1.27.1-bookworm@sha256:$(printf 'a%.0s' {1..64})" \
  bash "$root/scripts/build-relay-image.sh" --push ghcr.io/invine/clipp-relay:run "$tmp/result.json"
assert_arg 'ghcr.io/invine/clipp-relay:run'
assert_arg '--platform=linux/amd64,linux/arm64'
assert_arg '--push'
assert_arg "--metadata-file=$tmp/result.json"

echo 'image build command checks passed'
