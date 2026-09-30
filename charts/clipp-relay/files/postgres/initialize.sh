#!/bin/sh
set -eu

case "$(postgres --version)" in
  *'PostgreSQL) 18.'*) ;;
  *) echo 'expected PostgreSQL 18 image' >&2; exit 1 ;;
esac

root=${CLIPP_PG_ROOT:-/var/lib/postgresql}
admin_dir=${CLIPP_ADMIN_DIR:-/run/secrets/admin}
data=$root/18/docker
marker=$root/.clipp-pg18-owner
expected="${CLIPP_NAMESPACE}/${CLIPP_RELEASE}"
if [ -e "$marker" ]; then
  [ "$(cat "$marker")" = "$expected" ] || { echo 'PostgreSQL claim belongs to another release' >&2; exit 1; }
  [ -f "$data/PG_VERSION" ] && [ "$(cat "$data/PG_VERSION")" = 18 ] || { echo 'incomplete or wrong-major PostgreSQL data; manual recovery required' >&2; exit 1; }
  exit 0
fi
if [ -e "$data/PG_VERSION" ] || { [ -d "$data" ] && [ -n "$(ls -A "$data")" ]; }; then
  echo 'unmarked PostgreSQL data; refusing to adopt or overwrite' >&2
  exit 1
fi
[ "$CLIPP_INITIALIZE" = true ] || { echo 'empty PostgreSQL claim; explicit stopped-phase initialization required' >&2; exit 1; }
for entry in "$root"/* "$root"/.[!.]* "$root"/..?*; do
  [ -e "$entry" ] || [ -L "$entry" ] || continue
  [ "$entry" = "$root/lost+found" ] || { echo 'PostgreSQL claim is not empty; refusing fresh initialization' >&2; exit 1; }
done
admin_user=$(cat "$admin_dir/username")
printf '%s' "$admin_user" | grep -Eq '^[a-z][a-z0-9_]{0,62}$' || { echo 'invalid PostgreSQL administrator role name' >&2; exit 1; }
[ -s "$admin_dir/password" ] || { echo 'empty PostgreSQL administrator password' >&2; exit 1; }
mkdir -p "$data"
[ -z "$(ls -A "$data")" ] || { echo 'PostgreSQL data directory is not empty' >&2; exit 1; }
# The marker precedes initdb. An interrupted init requires operator inspection,
# never an automatic fresh init on the same or a replacement claim.
printf '%s' "$expected" > "$marker"
initdb -D "$data" --username="$admin_user" --pwfile="$admin_dir/password" --auth-host=scram-sha-256 --auth-local=peer
