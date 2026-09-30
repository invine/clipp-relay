#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
  echo "usage: $0 OCIR_REPOSITORY BUILDX_METADATA_FILE" >&2
  exit 2
fi

repository=$1
if [[ ! "$repository" =~ ^[a-z0-9-]+\.ocir\.io/[a-z0-9._/-]+$ ]]; then
  echo 'repository must be an OCI Container Registry path without a tag or digest' >&2
  exit 2
fi

digest=$(jq -er '."containerimage.digest" | select(type == "string")' "$2")
if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo 'Buildx metadata has no valid pushed registry manifest digest' >&2
  exit 2
fi

printf 'image:\n  repository: "%s"\n  digest: "%s"\n' "$repository" "$digest"
