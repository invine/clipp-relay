#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
digest="sha256:$(printf 'b%.0s' {1..64})"
printf '{"containerimage.digest":"%s","containerimage.config.digest":"sha256:%s"}\n' \
  "$digest" "$(printf 'a%.0s' {1..64})" > "$tmp/metadata.json"

bash "$root/scripts/helm-image-values.sh" iad.ocir.io/ns/clipp-relay "$tmp/metadata.json" > "$tmp/values.yaml"
grep -Fxq '  repository: "iad.ocir.io/ns/clipp-relay"' "$tmp/values.yaml"
bash "$root/scripts/helm-image-values.sh" ghcr.io/invine/clipp-relay "$tmp/metadata.json" > "$tmp/ghcr-values.yaml"
grep -Fxq '  repository: "ghcr.io/invine/clipp-relay"' "$tmp/ghcr-values.yaml"
grep -Fxq "  digest: \"$digest\"" "$tmp/values.yaml"
if grep -Fq "$(printf 'a%.0s' {1..64})" "$tmp/values.yaml"; then
  echo 'config digest was reported instead of registry manifest digest' >&2
  exit 1
fi

printf '{"containerimage.digest":"sha256:bad"}\n' > "$tmp/metadata.json"
if bash "$root/scripts/helm-image-values.sh" iad.ocir.io/ns/clipp-relay "$tmp/metadata.json" > "$tmp/values.yaml"; then
  echo 'invalid registry digest was accepted' >&2
  exit 1
fi

echo 'Helm image values checks passed'
