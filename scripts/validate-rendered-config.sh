#!/usr/bin/env bash
set -euo pipefail
if [[ $# -ne 1 || ! -f $1 ]]; then
  echo 'usage: scripts/validate-rendered-config.sh RENDERED.yaml' >&2
  exit 2
fi
rendered=$1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
validator=${CLIPP_RELAY_VALIDATOR:-}
if [[ -z $validator ]]; then
  validator="$work/clipp-relay-validator"
  go build -o "$validator" ./cmd/clipp-relay
fi
if [[ ! -x $validator ]]; then
  echo 'FAIL runtime validator binary is missing or not executable' >&2
  exit 1
fi
ruby -ryaml -rjson -e '
  docs = YAML.load_stream(File.read(ARGV.fetch(0))).compact
  config_map = docs.find { |doc| doc["kind"] == "ConfigMap" && doc.dig("data", "config.json") }
  abort "FAIL rendered chart is missing ConfigMap data.config.json" unless config_map
  raw = config_map.dig("data", "config.json")
  config = JSON.parse(raw)
  abort "FAIL rendered runtime configuration must be a JSON object" unless config.is_a?(Hash)
  File.write(ARGV.fetch(1), raw)
' "$rendered" "$work/config.json"
if ! "$validator" -command validate-config -config "$work/config.json"; then
  echo "FAIL runtime rejected configuration rendered in $rendered" >&2
  exit 1
fi
