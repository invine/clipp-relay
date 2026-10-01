#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
if [[ "$1 $2 $3" != 'buildx imagetools inspect' || "$4" != '--raw' ]]; then
  exit 2
fi
cat "$FAKE_INDEX"
SH
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" FAKE_INDEX="$tmp/index.json"
ref="iad.ocir.io/ns/clipp-relay@sha256:$(printf 'a%.0s' {1..64})"

cat > "$FAKE_INDEX" <<'JSON'
{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}
JSON
bash "$root/scripts/verify-image-index.sh" "$ref"
bash "$root/scripts/verify-image-index.sh" "ghcr.io/invine/clipp-relay@sha256:$(printf 'a%.0s' {1..64})"

cat > "$FAKE_INDEX" <<'JSON'
{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}}]}
JSON
if bash "$root/scripts/verify-image-index.sh" "$ref"; then
  echo 'single-architecture registry index was accepted' >&2
  exit 1
fi

echo 'registry image index checks passed'
