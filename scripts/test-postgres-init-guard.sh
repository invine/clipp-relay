#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/admin" "$work/claim"
printf 'postgres\n' > "$work/admin/username"
printf 'test-only-password\n' > "$work/admin/password"
cat > "$work/bin/postgres" <<'SH'
#!/bin/sh
printf 'postgres (PostgreSQL) 18.6\n'
SH
cat > "$work/bin/initdb" <<'SH'
#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D) data=$2; shift 2 ;;
    *) shift ;;
  esac
done
printf '18\n' > "$data/PG_VERSION"
printf 'run\n' >> "$CLIPP_TEST_INIT_LOG"
SH
chmod +x "$work/bin/postgres" "$work/bin/initdb"
export PATH="$work/bin:$PATH" CLIPP_PG_ROOT="$work/claim" CLIPP_ADMIN_DIR="$work/admin" CLIPP_NAMESPACE=relay-portal-test CLIPP_RELEASE=isolated CLIPP_TEST_INIT_LOG="$work/init.log"
guard=charts/clipp-relay/files/postgres/initialize.sh
export CLIPP_INITIALIZE=false
if /bin/sh "$guard" > "$work/refused.log" 2>&1; then echo 'empty claim initialized without permission' >&2; exit 1; fi
export CLIPP_INITIALIZE=true
/bin/sh "$guard"
[[ $(cat "$work/claim/.clipp-pg18-owner") == relay-portal-test/isolated ]]
[[ $(cat "$work/claim/18/docker/PG_VERSION") == 18 ]]
export CLIPP_INITIALIZE=false
/bin/sh "$guard"
[[ $(wc -l < "$work/init.log") == 1 ]]
rm "$work/claim/18/docker/PG_VERSION"
if /bin/sh "$guard" > "$work/incomplete.log" 2>&1; then echo 'incomplete data accepted' >&2; exit 1; fi
printf '17\n' > "$work/claim/18/docker/PG_VERSION"
if /bin/sh "$guard" > "$work/wrong-major.log" 2>&1; then echo 'wrong-major data accepted' >&2; exit 1; fi
printf '18\n' > "$work/claim/18/docker/PG_VERSION"
printf 'other/release' > "$work/claim/.clipp-pg18-owner"
if /bin/sh "$guard" > "$work/foreign.log" 2>&1; then echo 'foreign data accepted' >&2; exit 1; fi
rm "$work/claim/.clipp-pg18-owner"
if /bin/sh "$guard" > "$work/unmarked.log" 2>&1; then echo 'unmarked data accepted' >&2; exit 1; fi
mkdir -p "$work/old-claim/17/docker"
printf '17\n' > "$work/old-claim/17/docker/PG_VERSION"
export CLIPP_PG_ROOT="$work/old-claim" CLIPP_INITIALIZE=true
if /bin/sh "$guard" > "$work/old-major-dir.log" 2>&1; then echo 'older-major claim silently initialized as PostgreSQL 18' >&2; exit 1; fi
echo 'PostgreSQL initialization guard checks passed'
