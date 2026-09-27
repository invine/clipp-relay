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

long_label=$(printf 'a%.0s' {1..64})
for override in \
  'database.mode=bundled' \
  'public.wssHostname=portal.example.test' \
  'listeners.privatePort=8080' \
  'transports.tcp.publicPort=0' \
  'database.external.host=' \
  'deployment.phase=migrating' \
  'image.digest=latest' \
  'unrecognized.key=value' \
  'public.portalOrigin=https://portal.example.test?bad' \
  'public.wssHostname=wss..example.test' \
  'public.ingressClass=bad/name' \
  'database.servingSecret=bad/name' \
  'database.migrationSecret=bad/name' \
  'database.caSecret=bad/name' \
  'database.caKey=bad/key' \
  "public.portalOrigin=https://$long_label.example.test" \
  "public.wssHostname=$long_label.example.test" \
  "database.external.host=$long_label.example.test" \
  "database.servingSecret=$long_label"; do
  if helm template isolated "$chart" -n clipp-isolated -f "$example" --set "$override" > "$work/invalid.yaml" 2>&1; then
    echo "invalid chart setting accepted: $override" >&2
    exit 1
  fi
done

helm template isolated "$chart" -n clipp-isolated -f "$example" \
  --set public.ingressClass=f5.nginx > "$work/dotted-class.yaml"
grep -q 'ingressClassName: "f5.nginx"' "$work/dotted-class.yaml"

helm template isolated "$chart" -n clipp-isolated -f "$example" \
  --set 'transports.tcp.addresses[0]=/dns4/tcp.example.test/tcp/4001' \
  --set 'transports.udp.addresses[0]=/dns4/udp.example.test/udp/4003' \
  --set-json 'networkPolicy.apiCidrs=[]' > "$work/overrides.yaml"
helm template isolated "$chart" -n clipp-isolated -f "$example" \
  --set transports.tcp.enabled=false --set transports.udp.enabled=false \
  --set oci.subnetOcid= --set oci.frontendNsgOcid= --set oci.backendNsgOcid= \
  --set-json 'networkPolicy.apiCidrs=[]' > "$work/wss-only.yaml"
! grep -q 'type: LoadBalancer' "$work/wss-only.yaml"
! grep -q 'kind: Role$' "$work/wss-only.yaml"
! grep -q 'kind: Role$' "$work/overrides.yaml"
grep -q 'automountServiceAccountToken: false' "$work/overrides.yaml"

injected=$'nginx\n  defaultBackend:\n    service:\n      name: attacker'
if helm template isolated "$chart" -n clipp-isolated -f "$example" --set-string "public.ingressClass=$injected" > "$work/injected.yaml" 2>&1; then
  echo 'newline Ingress injection accepted' >&2
  exit 1
fi

bash scripts/preflight-external.sh "$example" isolated-other clipp-other > "$work/preflight.log"
grep -q 'isolated-other' "$work/preflight.log"
grep -q 'clipp-other' "$work/preflight.log"
echo 'chart render and rejection checks passed'
