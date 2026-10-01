#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
chart=charts/clipp-relay
values=$chart/examples/bundled-test-values.yaml
helm template isolated "$chart" -n relay-portal-test -f "$values" \
  --set oci.securityRuleManagementMode=None --set oci.internal=true \
  --set oci.frontendNsgOcid= --set oci.backendNsgOcid= > "$work/private.yaml"
ruby -ryaml -e '
docs=YAML.load_stream(File.read(ARGV[0])).compact
services=docs.select { |d| d["kind"] == "Service" && d.dig("spec", "type") == "LoadBalancer" }
abort "expected TCP and UDP NLBs" unless services.length == 2
services.each do |s|
  a=s.dig("metadata", "annotations")
  abort "wrong rule management mode" unless a["oci.oraclecloud.com/security-rule-management-mode"] == "None"
  abort "internal NLB missing" unless a["oci-network-load-balancer.oraclecloud.com/internal"] == "true"
  abort "private address hidden" unless a["oci-network-load-balancer.oraclecloud.com/external-ip-only"] == "false"
  abort "NSG annotation emitted" if a.keys.any? { |k| k.include?("network-security-group") }
end
' "$work/private.yaml"
for invalid in 'oci.securityRuleManagementMode=unknown' 'oci.frontendNsgOcid=' 'oci.backendNsgOcid='; do
  if helm template isolated "$chart" -n relay-portal-test -f "$values" --set "$invalid" > "$work/invalid.yaml" 2>&1; then
    echo "invalid networking configuration accepted: $invalid" >&2; exit 1
  fi
done
echo 'private-subnet and explicit no-NSG chart checks passed'
