#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 1 || ! "$1" =~ ^(ghcr\.io/[a-z0-9._-]+/[a-z0-9._/-]+|[a-z0-9-]+\.ocir\.io/[a-z0-9._/-]+)@sha256:[0-9a-f]{64}$ ]]; then
  echo "usage: $0 REGISTRY_REPOSITORY@sha256:DIGEST" >&2
  exit 2
fi

docker buildx imagetools inspect --raw "$1" |
  jq -e '[.manifests[].platform | select(.os == "linux") | .architecture] |
    (index("amd64") != null and index("arm64") != null)' > /dev/null

echo 'registry index contains Linux AMD64 and ARM64 manifests'
