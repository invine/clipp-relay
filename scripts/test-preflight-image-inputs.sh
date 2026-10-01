#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat > "$work/bin/helm" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$HELM_CALL_LOG"
echo 'unexpected Helm invocation before image validation' >&2
exit 99
SH
chmod +x "$work/bin/helm"
export HELM_CALL_LOG="$work/helm.log"
values=charts/clipp-relay/examples/bundled-test-values.yaml
for digest in '' latest; do
  printf 'image:\n  digest: "%s"\n' "$digest" > "$work/override.yaml"
  if PATH="$work/bin:$PATH" bash scripts/preflight-bundled-test.sh "$values" isolated relay-portal-test '' "$work/override.yaml" > "$work/result.log" 2>&1; then
    echo 'missing/invalid relay digest accepted' >&2; exit 1
  fi
  if ! grep -q 'FAIL relay image.digest' "$work/result.log"; then
    cat "$work/result.log" >&2
    echo 'missing actionable digest diagnostic' >&2; exit 1
  fi
  test ! -e "$HELM_CALL_LOG"
done

# A later overlay must override the empty digest, just as it does in Helm.
printf 'image:\n  digest: "sha256:%064d"\n' 1 > "$work/valid-image.yaml"
if PATH="$work/bin:$PATH" bash scripts/preflight-bundled-test.sh "$values" isolated relay-portal-test '' "$work/override.yaml" "$work/valid-image.yaml" > "$work/result.log" 2>&1; then
  echo 'fake Helm unexpectedly passed' >&2; exit 1
fi
grep -q -- '-n relay-portal-test' "$HELM_CALL_LOG"
echo 'preflight image diagnostics and namespace checks passed'
