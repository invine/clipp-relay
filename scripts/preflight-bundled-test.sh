#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 3 ]]; then
  echo 'usage: scripts/preflight-bundled-test.sh VALUES.yaml RELEASE relay-portal-test [KUBECTL_CONTEXT [OVERLAY_VALUES.yaml ...]]' >&2
  exit 2
fi
cd "$(dirname "$0")/.."
values=$1
release=$2
namespace=$3
context=${4:-}
[[ $namespace == relay-portal-test && $release =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || { echo 'bundled test preflight requires relay-portal-test and a DNS-label release' >&2; exit 2; }
flags=(-f "$values")
value_files=("$values")
if (( $# > 4 )); then
  for overlay in "${@:5}"; do flags+=(-f "$overlay"); value_files+=("$overlay"); done
fi
# Helm lint may log template fail() messages and still return success. Check the
# image prerequisite before Helm, honoring the same values-file precedence.
ruby -ryaml -e '
  image = {}
  ARGV.each do |path|
    begin
      values = YAML.safe_load(File.read(path)) || {}
    rescue Psych::Exception
      abort "FAIL invalid values YAML: #{path}"
    end
    abort "FAIL values must be a mapping: #{path}" unless values.is_a?(Hash)
    next unless values.key?("image")
    section = values["image"]
    if section.nil?
      image = {}
      next
    end
    abort "FAIL image must be a mapping: #{path}" unless section.is_a?(Hash)
    image.merge!(section)
  end
  unless image["repository"].is_a?(String) && !image["repository"].strip.empty?
    abort "FAIL relay image.repository: set the repository from the successful Publish relay image workflow artifact."
  end
  unless image["digest"].is_a?(String) && image["digest"].match?(/\Asha256:[0-9a-f]{64}\z/)
    abort "FAIL relay image.digest: missing or invalid. Publish the relay image with .github/workflows/relay-image.yml, then copy image.repository and image.digest from its relay-image-values artifact into the private values file. The PostgreSQL digest is separate. No cluster checks were run."
  end
' "${value_files[@]}"
chart=charts/clipp-relay
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
helm lint "$chart" -n "$namespace" "${flags[@]}" --strict > "$work/lint.log" 2>&1 || { cat "$work/lint.log" >&2; exit 1; }
for phase in stopped migrating serving; do
  extra=(--set deployment.phase="$phase" --set deployment.runId= --set database.bundled.initialize=false)
  if [[ $phase == migrating ]]; then extra=(--set deployment.phase=migrating --set deployment.runId=preflight --set database.bundled.initialize=false); fi
  helm template "$release" "$chart" -n "$namespace" "${flags[@]}" "${extra[@]}" > "$work/$phase.yaml"
done
# An explicit first-install render is separate from later maintenance and serving.
helm template "$release" "$chart" -n "$namespace" "${flags[@]}" --set deployment.phase=stopped --set deployment.runId= > "$work/initial.yaml"
ruby -ryaml -e '
  docs = YAML.load_stream(File.read(ARGV[0])).compact
  abort "not bundled mode" unless docs.any? { |d| d["kind"] == "StatefulSet" && d.dig("metadata", "name").end_with?("-postgres") }
' "$work/stopped.yaml"
echo "PASS bundled test chart/schema rendered for $release in $namespace: initial/stopped/migrating/serving"
echo 'PENDING operator: verify relay image digest against the published workflow artifact and node architecture'
echo 'PENDING operator: verify PostgreSQL 18 Debian image digest, node architecture, and provenance against registry evidence'
echo 'PENDING operator: verify explicit Longhorn StorageClass, capacity/permissions, and claim identity; Retain recommended, explicit operator-approved Delete allowed for disposable tests'
echo 'PENDING operator: verify existing PostgreSQL admin, serving, migration, server TLS, and trust CA Secrets; validate certificate SAN for the internal Service FQDN and CA chain'
echo 'PENDING operator: prove empty authorized claim before first init; observe bootstrap completion and manually migrate schema before serving; clear initialize permission'
echo 'PENDING operator: verify CNI NetworkPolicy enforcement, TLS/SCRAM rejection, role/PUBLIC privileges, restart with retained data, and recovery procedure'
echo 'PENDING release gates: this test-only path has no backup scheduler, certificate reload watcher, or validated live OCI installation'
if [[ -z $context ]]; then
  echo 'PENDING cluster read-only inspection: supply an authorized context; no cluster changes performed'
  exit 0
fi
kubectl --context "$context" get namespace "$namespace" -o name >/dev/null
ingress_class=$(awk '$1 == "ingressClassName:" {gsub(/\"/, "", $2); print $2; exit}' "$work/serving.yaml")
kubectl --context "$context" get ingressclass "$ingress_class" -o name >/dev/null
echo "PASS existing IngressClass name: $ingress_class"
for issuer in $(awk '/^kind: Certificate$/ {cert=1} cert && /^  issuerRef:$/ {ref=1; next} ref && /^    name:/ {gsub(/"/, "", $2); print $2; ref=0} /^---$/ {cert=0; ref=0}' "$work/serving.yaml" | sort -u); do
  kubectl --context "$context" get clusterissuer "$issuer" -o name >/dev/null
  echo "PASS existing ClusterIssuer name: $issuer"
  echo "PENDING ClusterIssuer readiness, DNS and HTTP-01 reachability: $issuer"
done
for name in $(awk '$1 == "secretName:" {gsub(/\"/, "", $2); print $2}' "$work/serving.yaml" | sort -u); do
  if awk '/^kind: Certificate$/ {cert=1} cert && /secretName:/ {gsub(/\"/, "", $2); print $2} /^---$/ {cert=0}' "$work/serving.yaml" | grep -Fxq "$name"; then
    if ! found=$(kubectl --context "$context" -n "$namespace" get secret "$name" --ignore-not-found -o name); then
      echo "FAIL TLS Secret lookup: $name" >&2; exit 1
    fi
    if [[ -z $found ]]; then echo "PENDING cert-manager TLS Secret: $name"; else echo "PASS cert-manager TLS Secret name: $name"; fi
  else
    kubectl --context "$context" -n "$namespace" get secret "$name" -o name >/dev/null
    echo "PASS existing Secret name: $name"
  fi
done
claim=$(ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; p=docs.find { |d| d["kind"] == "PersistentVolumeClaim" }; puts p.dig("metadata", "name") if p' "$work/stopped.yaml")
if [[ -n $claim ]]; then
  initializing=$(ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; p=docs.find { |d| d["kind"] == "StatefulSet" && d.dig("metadata", "name").end_with?("-postgres") }; c=p.dig("spec", "template", "spec", "initContainers").find { |x| x["name"] == "initialize" }; puts c["env"].find { |e| e["name"] == "CLIPP_INITIALIZE" }["value"]' "$work/initial.yaml")
  if ! existing_claim=$(kubectl --context "$context" -n "$namespace" get pvc "$claim" --ignore-not-found -o name); then
    echo "FAIL PVC collision lookup: $claim" >&2; exit 1
  fi
  if [[ $initializing == true && -n $existing_claim ]]; then
    echo "FAIL existing same-name PVC blocks first initialization: $claim; use the explicit existing-claim path only after verifying ownership and data" >&2
    exit 1
  fi
  storage_class=$(ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; puts docs.find { |d| d["kind"] == "PersistentVolumeClaim" }.dig("spec", "storageClassName")' "$work/stopped.yaml")
  kubectl --context "$context" get storageclass "$storage_class" -o name >/dev/null
  echo "PASS existing StorageClass name: $storage_class"
  echo "PENDING PVC admission/reclaim and ownership verification: $claim (chart-created only at authorized install)"
else
  claim=$(ruby -ryaml -e 'docs=YAML.load_stream(File.read(ARGV[0])).compact; puts docs.find { |d| d["kind"] == "StatefulSet" && d.dig("metadata", "name").end_with?("-postgres") }.dig("spec", "template", "spec", "volumes").find { |v| v["name"] == "data" }.dig("persistentVolumeClaim", "claimName")' "$work/stopped.yaml")
  kubectl --context "$context" -n "$namespace" get pvc "$claim" -o name >/dev/null
  echo "PASS existing PVC name: $claim (contents and major unverified)"
fi
echo 'PENDING Secret contents, TLS trust, storage reclaim, cluster policy and readiness verification; metadata inspection only'
