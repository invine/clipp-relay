#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_DOCKER_ARGS"
if [[ "$1 $2" == 'image inspect' ]]; then
  case "$4" in
    *Architecture*) printf 'linux/%s\n' "${FAKE_ARCH:-amd64}" ;;
    *User*) printf '10001:10001\n' ;;
  esac
elif [[ "$1" == run ]]; then
  echo '{"msg":"configuration rejected"}' >&2
  exit 1
fi
SH
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" FAKE_DOCKER_ARGS="$tmp/args"

bash "$root/scripts/smoke-relay-image.sh" amd64 local/clipp-relay:test
grep -Fq -- '--read-only' "$FAKE_DOCKER_ARGS"
grep -Fq -- '--network none' "$FAKE_DOCKER_ARGS"
grep -Fq -- '--platform linux/amd64' "$FAKE_DOCKER_ARGS"

: > "$FAKE_DOCKER_ARGS"
if FAKE_ARCH=arm64 bash "$root/scripts/smoke-relay-image.sh" amd64 local/clipp-relay:test; then
  echo 'wrong-platform image passed smoke check' >&2
  exit 1
fi
if grep -Fq 'run ' "$FAKE_DOCKER_ARGS"; then
  echo 'wrong-platform image was run' >&2
  exit 1
fi

echo 'image smoke checks passed'
