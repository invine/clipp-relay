#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo 'usage: scripts/preflight-external.sh VALUES.yaml [KUBECTL_CONTEXT]' >&2
  exit 2
fi
cd "$(dirname "$0")/.."
values=$1
context=${2:-}
chart=charts/clipp-relay
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if ! helm lint "$chart" -f "$values" --strict > "$work/lint.log" 2>&1; then
  echo 'FAIL static chart lint/schema; inspect values and rerun' >&2
  sed -n '1,30p' "$work/lint.log" >&2
  exit 1
fi
for phase in serving stopped migrating; do
  if [[ $phase == migrating ]]; then
    helm template isolated "$chart" -n clipp-isolated -f "$values" --set deployment.phase=migrating --set deployment.runId=preflight > "$work/$phase.yaml"
  else
    helm template isolated "$chart" -n clipp-isolated -f "$values" --set deployment.phase="$phase" --set deployment.runId= > "$work/$phase.yaml"
  fi
done
echo 'PASS rendered chart/schema: external database, serving/stopped/migrating phases'
echo 'PASS static resources: inspect rendered manifests for exact hosts, separated public/private surfaces and mounted Secret names'
echo 'PENDING operator: confirm pinned linux/arm64 or linux/amd64 Go-only image and process soft nofile >=16384 on eligible node'
echo 'PENDING operator: confirm PostgreSQL 17/18, expected schema 8, SCRAM and verify-full TLS, serving role without DDL, and distinct migration role'
echo 'PENDING operator: confirm existing F5 NGINX class/TLS Secrets/DNS, VCN-native Pod backend support, subnet/NSG rules, NLB health and disabled instant failover'
echo 'PENDING operator: confirm actual NetworkPolicy engine enforcement and CIDR/NAT/host-network behavior, ingress metadata trust, egress DNS/API/DB/HTTPS only'
echo 'PENDING operator: execute isolated HTTPS discovery, TCP/WSS/UDP connection, readiness under pressure, and wrong CA/hostname rejection before serving'
if [[ -z $context ]]; then
  echo 'PENDING cluster read-only inspection: supply an authorized isolated kubectl context; no cluster changes performed'
  exit 0
fi
kubectl --context "$context" get namespace clipp-isolated -o name >/dev/null
for name in $(awk '$1 == "secretName:" {print $2}' "$work/serving.yaml" | sort -u); do
  kubectl --context "$context" -n clipp-isolated get secret "$name" -o name >/dev/null
  echo "PASS existing Secret reference: $name"
done
ingress_class=$(awk '$1 == "ingressClassName:" {print $2; exit}' "$work/serving.yaml")
kubectl --context "$context" get ingressclass "$ingress_class" -o name >/dev/null
echo 'PASS existing isolated namespace, Secret names and ingress class (contents and authority not inspected)'
