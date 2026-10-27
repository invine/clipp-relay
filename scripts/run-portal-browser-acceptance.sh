#!/usr/bin/env bash
set -euo pipefail

# Runs the production portal handler with the disposable-DB credentials supplied
# by the operator and the local identity provider in cmd/portal-browser-fixture.
if (($# != 3)); then
  echo "usage: $0 SERVING_CONFIG LOCALHOST_TLS_CERT LOCALHOST_TLS_KEY" >&2
  exit 2
fi
root=$(cd "$(dirname "$0")/.." && pwd)
config_path=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
cert_path=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
key_path=$(cd "$(dirname "$3")" && pwd)/$(basename "$3")
: "${CHROME_BIN:?set CHROME_BIN to Google Chrome for Testing or another dedicated Chrome executable}"
work=$(mktemp -d "${TMPDIR:-/private/tmp}/clipp-relay-browser-fixture.XXXXXX")
evidence_dir=${ARTIFACT_DIR:-/private/tmp/clipp-relay-portal-browser-evidence}
mkdir -p "$evidence_dir"
pid=
cleanup() {
  if [[ -n "$pid" ]]; then kill "$pid" >/dev/null 2>&1 || true; wait "$pid" >/dev/null 2>&1 || true; fi
  if [[ -f "$work/fixture.log" ]]; then cp "$work/fixture.log" "$evidence_dir/fixture.log"; fi
  rm -rf "$work"
}
trap cleanup EXIT

python3 - "$config_path" "$work/serving.json" "$work/admin-allowlist.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: config=json.load(f)
config['portal_origin']='https://localhost:18443'
config.setdefault('secrets',{})['admin_allowlist_file']=sys.argv[3]
with open(sys.argv[2],'w',encoding='utf-8') as f: json.dump(config,f)
with open(sys.argv[3],'w',encoding='utf-8') as f: json.dump({'revision':1,'emails':['workspace-a@example.test']},f)
PY
cd "$root"
GOCACHE="${GOCACHE:-/private/tmp/clipp-relay-browser-gocache}" go build -o "$work/fixture" ./cmd/portal-browser-fixture
"$work/fixture" -config "$work/serving.json" -tls-cert "$cert_path" -tls-key "$key_path" -listen 127.0.0.1:18443 >"$work/fixture.log" 2>&1 &
pid=$!
ready=0
for _ in $(seq 1 100); do
  if curl --insecure --silent --show-error --fail https://localhost:18443/ >/dev/null 2>&1; then ready=1; break; fi
  if ! kill -0 "$pid" 2>/dev/null; then cat "$work/fixture.log" >&2; exit 1; fi
  sleep 0.1
done
if ((ready == 0)); then cat "$work/fixture.log" >&2; echo 'portal fixture did not become ready within 10 seconds' >&2; exit 1; fi
ADMIN_ALLOWLIST_FILE="$work/admin-allowlist.json" node "$root/scripts/portal-browser-acceptance.mjs" https://localhost:18443
