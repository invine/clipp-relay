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

helm template isolated "$chart" -n clipp-isolated -f "$example" \
  -f "$chart/examples/letsencrypt-values.yaml" > "$work/certificates.yaml"
ruby -ryaml -e '
  docs = YAML.load_stream(File.read(ARGV.fetch(0))).compact
  certificates = docs.select { |doc| doc["kind"] == "Certificate" }
  abort "expected portal and WSS Certificates" unless certificates.length == 2
  expected = {
    "portal.example.test" => "portal-tls-v1",
    "wss.example.test" => "wss-tls-v1"
  }
  certificates.each do |certificate|
    spec = certificate.fetch("spec")
    host = spec.fetch("dnsNames").fetch(0)
    abort "wrong Certificate target" unless expected.delete(host) == spec.fetch("secretName")
    abort "wrong ClusterIssuer" unless spec.fetch("issuerRef") == {"name" => "letsencrypt-prod", "kind" => "ClusterIssuer"}
  end
  abort "Certificate target missing" unless expected.empty?
' "$work/certificates.yaml"

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
  'certificates.enabled=true' \
  'certificates.clusterIssuer=bad/name' \
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

if helm template isolated "$chart" -n clipp-isolated -f "$example" \
  --set certificates.enabled=true --set certificates.clusterIssuer=letsencrypt-prod \
  --set public.wssTLSSecret=portal-tls-v1 > "$work/conflicting-certificates.yaml" 2>&1; then
  echo 'two Certificates can target one TLS Secret' >&2
  exit 1
fi

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

cp "$example" "$work/managed-cert-values.yaml"
cat >> "$work/managed-cert-values.yaml" <<'YAML'
certificates:
  enabled: true
  clusterIssuer: letsencrypt-prod
YAML
cat > "$work/kubectl" <<'SH'
#!/bin/sh
case " $* " in
  *" portal-tls-v1 "*|*" wss-tls-v1 "*)
    case " $* " in
      *" --ignore-not-found "*) exit 0 ;;
      *) exit 1 ;;
    esac ;;
  *) exit 0 ;;
esac
SH
chmod +x "$work/kubectl"
PATH="$work:$PATH" bash scripts/preflight-external.sh "$work/managed-cert-values.yaml" isolated clipp-isolated fake-context > "$work/managed-cert-preflight.log"
grep -q 'PENDING cert-manager TLS Secret' "$work/managed-cert-preflight.log"
grep -q 'PENDING ClusterIssuer readiness and ACME solver' "$work/managed-cert-preflight.log"
PATH="$work:$PATH" bash scripts/preflight-external.sh "$example" isolated clipp-isolated fake-context \
  "$chart/examples/letsencrypt-values.yaml" > "$work/overlay-preflight.log"
grep -q 'PASS existing ClusterIssuer: letsencrypt-prod' "$work/overlay-preflight.log"
cat > "$work/kubectl" <<'SH'
#!/bin/sh
case " $* " in
  *" get clusterissuer letsencrypt-prod "*) exit 1 ;;
  *) exit 0 ;;
esac
SH
chmod +x "$work/kubectl"
if PATH="$work:$PATH" bash scripts/preflight-external.sh "$work/managed-cert-values.yaml" isolated clipp-isolated fake-context > "$work/missing-issuer.log" 2>&1; then
  echo 'missing ClusterIssuer accepted by preflight' >&2
  exit 1
fi
cat > "$work/kubectl" <<'SH'
#!/bin/sh
case " $* " in
  *" portal-tls-v1 "*) echo 'Error from server (Forbidden): TLS Secret read denied' >&2; exit 1 ;;
  *) exit 0 ;;
esac
SH
chmod +x "$work/kubectl"
if PATH="$work:$PATH" bash scripts/preflight-external.sh "$work/managed-cert-values.yaml" isolated clipp-isolated fake-context > "$work/forbidden-tls.log" 2>&1; then
  echo 'TLS Secret read error accepted as pending issuance' >&2
  exit 1
fi
echo 'chart render and rejection checks passed'
bash scripts/test-chart-bundled.sh
