#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
chart=charts/clipp-relay
example=$chart/examples/external-values.yaml
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

helm lint "$chart" -f "$example" --strict
helm template isolated "$chart" -n clipp-isolated -f "$example" > "$work/serving.yaml"
grep -q 'kind: StatefulSet' "$work/serving.yaml"
grep -q 'replicas: 1' "$work/serving.yaml"
grep -q 'kind: NetworkPolicy' "$work/serving.yaml"
grep -q 'kind: Ingress' "$work/serving.yaml"
grep -q 'kind: Role' "$work/serving.yaml"
! grep -q 'kind: Secret' "$work/serving.yaml"
! grep -q 'kind: PersistentVolumeClaim' "$work/serving.yaml"
! grep -q 'subPath:' "$work/serving.yaml"
! grep -q 'kind: HorizontalPodAutoscaler' "$work/serving.yaml"
! grep -q 'kind: PodDisruptionBudget' "$work/serving.yaml"

helm template isolated "$chart" -n clipp-isolated -f "$example" --set deployment.phase=stopped > "$work/stopped.yaml"
grep -q 'replicas: 0' "$work/stopped.yaml"
ruby scripts/assert-chart-render.rb "$work/serving.yaml" "$work/stopped.yaml"
grep -q 'kind: Service' "$work/stopped.yaml"
! grep -q 'kind: Job' "$work/stopped.yaml"

for override in \
  'database.mode=bundled' \
  'public.wssHostname=portal.example.test' \
  'listeners.privatePort=8080' \
  'transports.tcp.publicPort=0' \
  'database.external.host=' \
  'deployment.phase=migrating' \
  'image.digest=latest' \
  'unrecognized.key=value'; do
  if helm template isolated "$chart" -n clipp-isolated -f "$example" --set "$override" > "$work/invalid.yaml" 2>&1; then
    echo "invalid chart setting accepted: $override" >&2
    exit 1
  fi
done

helm template isolated "$chart" -n clipp-isolated -f "$example" \
  --set 'transports.tcp.addresses[0]=/dns4/tcp.example.test/tcp/4001' \
  --set 'transports.udp.addresses[0]=/dns4/udp.example.test/udp/4003' > "$work/overrides.yaml"
! grep -q 'kind: Role$' "$work/overrides.yaml"
grep -q 'automountServiceAccountToken: false' "$work/overrides.yaml"

echo 'chart render and rejection checks passed'
