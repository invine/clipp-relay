#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ( "$1" != amd64 && "$1" != arm64 ) ]]; then
  echo "usage: $0 amd64|arm64 IMAGE:TAG" >&2
  exit 2
fi

arch=$1
image=$2
if [[ ! "$image" =~ ^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]]; then
  echo 'image must be a tagged repository' >&2
  exit 2
fi

platform=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")
user=$(docker image inspect --format '{{.Config.User}}' "$image")
if [[ "$platform" != "linux/$arch" || "$user" != '10001:10001' ]]; then
  echo "image platform/user rejected: $platform $user" >&2
  exit 1
fi

if output=$(docker run --rm --read-only --network none --cap-drop ALL \
    --security-opt no-new-privileges --platform "linux/$arch" \
    "$image" -command serve -config /nonexistent/config.json 2>&1); then
  echo 'image unexpectedly accepted a missing config' >&2
  exit 1
else
  status=$?
fi
if [[ "$status" != 1 ]] || ! jq -e -s '
    length == 1 and
    .[0].event == "service.startup" and
    .[0].level == "ERROR" and
    .[0].msg == "service startup" and
    .[0].outcome == "failed" and
    .[0].reason == "configuration_rejected"
  ' >/dev/null 2>&1 <<< "$output"; then
  echo "image failed its missing-config smoke check: exit=$status" >&2
  exit 1
fi

echo "linux/$arch image smoke check passed"
