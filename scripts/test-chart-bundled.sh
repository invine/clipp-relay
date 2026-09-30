#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
chart=charts/clipp-relay
values=$chart/examples/bundled-test-values.yaml
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
helm lint "$chart" -f "$values" --namespace relay-portal-test --strict
for phase in stopped migrating serving; do
  flags=(--set deployment.phase="$phase" --set database.bundled.initialize=false)
  if [[ "$phase" == stopped ]]; then flags=(--set deployment.phase=stopped); fi
  if [[ "$phase" == migrating ]]; then flags+=(--set deployment.runId=first); fi
  helm template isolated "$chart" -n relay-portal-test -f "$values" "${flags[@]}" > "$work/$phase.yaml"
done
ruby scripts/assert-bundled-chart-render.rb "$work/stopped.yaml" "$work/migrating.yaml" "$work/serving.yaml"
helm template isolated "$chart" -n relay-portal-test -f "$chart/examples/external-values.yaml" > "$work/external.yaml"
ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; abort "external mode rendered PostgreSQL" if docs.any? { |d| d.dig("metadata", "name").to_s.end_with?("-postgres", "-postgres-scripts") }' "$work/external.yaml"
for invalid in \
  'database.mode=external' \
  'database.external.host=another.example.test' \
  'database.bundled.image.digest=latest' \
  'database.bundled.storage.existingClaim=other-claim' \
  'database.bundled.storage.newClaim.storageClassName=' \
  'database.bundled.adminSecret=' \
  'database.bundled.tlsSecret=' \
  'database.caSecret=' \
  'database.bundled.adminSecret=clipp-db-serving-v1' \
  'database.name=bad-name' \
  'database.bundled.initialize=true'; do
  if helm template isolated "$chart" -n relay-portal-test -f "$values" --set "$invalid" --set deployment.phase=serving > "$work/invalid.yaml" 2>&1; then
    echo "unsafe bundled setting accepted: $invalid" >&2
    exit 1
  fi
done
if helm template isolated "$chart" -n relay-portal-test -f "$values" \
  --set database.bundled.initialize=false --set deployment.phase=migrating \
  --set deployment.runId=bad/path > "$work/invalid-run.yaml" 2>&1; then
  echo 'unsafe bootstrap run ID accepted' >&2
  exit 1
fi
if helm template isolated "$chart" -n production -f "$values" > "$work/other-namespace.yaml" 2>&1; then
  echo 'bundled database accepted outside relay-portal-test' >&2
  exit 1
fi
if helm template isolated "$chart" -n relay-portal-test -f "$values" --set deployment.phase=migrating > "$work/no-run-id.yaml" 2>&1; then
  echo 'bootstrap accepted without run ID' >&2
  exit 1
fi
helm template isolated "$chart" -n relay-portal-test -f "$values" \
  --set database.bundled.storage.newClaim.enabled=false \
  --set database.bundled.storage.existingClaim=retained-pgdata \
  --set database.bundled.storage.newClaim.storageClassName= \
  --set database.bundled.storage.newClaim.size= \
  --set database.bundled.initialize=false > "$work/existing.yaml"
ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; abort "existing PVC adopted" if docs.any? { |d| d["kind"] == "PersistentVolumeClaim" }; pg=docs.find { |d| d["kind"] == "StatefulSet" && d.dig("metadata", "name").end_with?("-postgres") }; abort "existing PVC not mounted" unless pg.dig("spec", "template", "spec", "volumes").find { |v| v["name"] == "data" }.dig("persistentVolumeClaim", "claimName") == "retained-pgdata"' "$work/existing.yaml"
echo 'bundled chart render checks passed'
bash scripts/preflight-bundled-test.sh "$values" isolated relay-portal-test > "$work/preflight.log"
grep -q 'PASS bundled test chart/schema rendered' "$work/preflight.log"
grep -q 'PENDING operator: verify explicit Longhorn StorageClass' "$work/preflight.log"
! grep -q 'external database' "$work/preflight.log"
cat > "$work/kubectl" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$KUBECTL_TEST_LOG"
case " $* " in
  *' get pvc '* )
    if [ "${KUBECTL_MOCK_PVC:-absent}" = present ]; then printf 'persistentvolumeclaim/isolated-clipp-relay-postgres\n'; fi ;;
  *' get '*) printf 'resource/name\n' ;;
  *) echo 'preflight attempted cluster mutation' >&2; exit 1 ;;
esac
SH
chmod +x "$work/kubectl"
KUBECTL_TEST_LOG="$work/kubectl.log" PATH="$work:$PATH" \
  bash scripts/preflight-bundled-test.sh "$values" isolated relay-portal-test fake-context > "$work/preflight-context.log"
grep -q 'PASS existing StorageClass name' "$work/preflight-context.log"
grep -q 'PASS existing Secret name' "$work/preflight-context.log"
if KUBECTL_MOCK_PVC=present KUBECTL_TEST_LOG="$work/kubectl-collision.log" PATH="$work:$PATH" \
  bash scripts/preflight-bundled-test.sh "$values" isolated relay-portal-test fake-context > "$work/collision.log" 2>&1; then
  echo 'first initialization accepted an existing same-name PVC' >&2
  exit 1
fi
grep -q 'existing same-name PVC' "$work/collision.log"
