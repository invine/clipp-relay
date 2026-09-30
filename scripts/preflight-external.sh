#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 3 ]]; then
  echo 'usage: scripts/preflight-external.sh VALUES.yaml RELEASE NAMESPACE [KUBECTL_CONTEXT [OVERLAY_VALUES.yaml ...]]' >&2
  exit 2
fi
cd "$(dirname "$0")/.."
values=$1
release=$2
namespace=$3
context=${4:-}
values_flags=(-f "$values")
if (( $# > 4 )); then
  for overlay in "${@:5}"; do values_flags+=(-f "$overlay"); done
fi
if [[ ! $release =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ || ! $namespace =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
  echo 'release and namespace must be exact Kubernetes DNS labels' >&2
  exit 2
fi
chart=charts/clipp-relay
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if ! helm lint "$chart" "${values_flags[@]}" --strict > "$work/lint.log" 2>&1; then
  echo 'FAIL static chart lint/schema; inspect values and rerun' >&2
  sed -n '1,30p' "$work/lint.log" >&2
  exit 1
fi
for phase in serving stopped migrating; do
  if [[ $phase == migrating ]]; then
    helm template "$release" "$chart" -n "$namespace" "${values_flags[@]}" --set deployment.phase=migrating --set deployment.runId=preflight > "$work/$phase.yaml"
  else
    helm template "$release" "$chart" -n "$namespace" "${values_flags[@]}" --set deployment.phase="$phase" --set deployment.runId= > "$work/$phase.yaml"
  fi
done
echo "PASS rendered chart/schema for release $release in namespace $namespace: external database, serving/stopped/migrating phases"
echo 'PENDING operator render review: inspect exact hosts, separated public/private surfaces and mounted Secret names'
echo 'PENDING operator: confirm pinned linux/arm64 or linux/amd64 Go-only image and process soft nofile >=16384 on eligible node'
echo 'PENDING operator: confirm PostgreSQL 17/18, expected schema 8, SCRAM and verify-full TLS, serving role without DDL, and distinct migration role'
echo 'PENDING operator: confirm F5 WSS proxy read/send timeout honors 3600s and does not evict valid sessions; verify existing class/TLS Secrets/DNS, VCN-native Pod backends, subnet/NSG rules, NLB health and disabled instant failover'
echo 'PENDING operator: confirm actual NetworkPolicy engine enforcement and CIDR/NAT/host-network behavior, ingress metadata trust, egress DNS/API/DB/HTTPS only'
echo 'PENDING operator: execute isolated HTTPS discovery, TCP/WSS/UDP connection, readiness under pressure, and wrong CA/hostname rejection before serving'
if [[ -z $context ]]; then
  echo 'PENDING cluster read-only inspection: supply an authorized isolated kubectl context; no cluster changes performed'
  exit 0
fi
kubectl --context "$context" get namespace "$namespace" -o name >/dev/null
managed_tls_secrets=$(awk '
  /^---$/ { certificate = 0 }
  /^kind: Certificate$/ { certificate = 1 }
  certificate && /^  secretName:/ { gsub(/"/, "", $2); print $2 }
' "$work/serving.yaml")
certificate_issuers=$(awk '
  /^---$/ { certificate = 0; issuer_ref = 0 }
  /^kind: Certificate$/ { certificate = 1 }
  certificate && /^  issuerRef:$/ { issuer_ref = 1; next }
  issuer_ref && /^    name:/ { gsub(/"/, "", $2); print $2; issuer_ref = 0 }
' "$work/serving.yaml" | sort -u)
for issuer in $certificate_issuers; do
  kubectl --context "$context" get clusterissuer "$issuer" -o name >/dev/null
  echo "PASS existing ClusterIssuer: $issuer"
  echo "PENDING ClusterIssuer readiness and ACME solver: verify issuer status, Let's Encrypt endpoint, HTTP-01 ingress class, DNS and reachability"
done
for name in $(awk '$1 == "secretName:" {gsub(/\"/, "", $2); print $2}' "$work/serving.yaml" | sort -u); do
  if printf '%s\n' "$managed_tls_secrets" | grep -Fxq "$name"; then
    if ! tls_secret=$(kubectl --context "$context" -n "$namespace" get secret "$name" --ignore-not-found -o name); then
      echo "FAIL TLS Secret lookup: $name" >&2
      exit 1
    fi
    if [[ -n $tls_secret ]]; then
      echo "PASS cert-manager TLS Secret: $name"
    else
      echo "PENDING cert-manager TLS Secret: $name (Certificate renders; issuance and trusted TLS require live verification)"
    fi
  else
    kubectl --context "$context" -n "$namespace" get secret "$name" -o name >/dev/null
    echo "PASS existing Secret reference: $name"
  fi
done
ingress_class=$(awk '$1 == "ingressClassName:" {gsub(/\"/, "", $2); print $2; exit}' "$work/serving.yaml")
kubectl --context "$context" get ingressclass "$ingress_class" -o name >/dev/null
echo 'PASS existing namespace, non-TLS Secret names and ingress class; cert-manager TLS may remain pending (contents and authority not inspected)'
