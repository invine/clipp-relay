#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --local amd64|arm64 IMAGE:TAG | --push OCIR_IMAGE:TAG METADATA_FILE" >&2
  exit 2
}

mode=${1:-}
case "$mode" in
  --local)
    [[ $# == 3 ]] || usage
    arch=$2
    image=$3
    [[ "$arch" == amd64 || "$arch" == arm64 ]] || usage
    platform="linux/$arch"
    ;;
  --push)
    [[ $# == 3 ]] || usage
    image=$2
    platform='linux/amd64,linux/arm64'
    ;;
  *) usage ;;
esac

if [[ ! "$image" =~ ^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]]; then
  echo 'image must be a valid tagged repository, without a digest or whitespace' >&2
  exit 2
fi

args=(buildx build "--platform=$platform" -f Dockerfile -t "$image")
if [[ -n "${GO_BUILDER_IMAGE:-}" ]]; then
  if [[ ! "$GO_BUILDER_IMAGE" =~ ^golang:1\.27\.1-bookworm@sha256:[0-9a-f]{64}$ ]]; then
    echo 'GO_BUILDER_IMAGE must pin the Go 1.27.1 Bookworm image by sha256 digest' >&2
    exit 2
  fi
  args+=("--build-arg=GO_BUILDER_IMAGE=$GO_BUILDER_IMAGE")
fi

if [[ "$mode" == --push ]]; then
  if [[ ! "$image" =~ ^[a-z0-9-]+\.ocir\.io/[a-z0-9._/-]+:[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]]; then
    echo 'push target must be a tagged OCI Container Registry repository' >&2
    exit 2
  fi
  if [[ -z "${GO_BUILDER_IMAGE:-}" ]]; then
    echo 'push requires a digest-pinned GO_BUILDER_IMAGE' >&2
    exit 2
  fi
  metadata=$3
  if [[ ! -d "$(dirname "$metadata")" ]]; then
    echo 'metadata file parent directory must already exist' >&2
    exit 2
  fi
  args+=(--push "--metadata-file=$metadata")
else
  args+=(--load)
fi

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
docker "${args[@]}" .
