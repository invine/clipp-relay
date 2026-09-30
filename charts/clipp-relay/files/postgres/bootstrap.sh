#!/bin/sh
set -eu
umask 077
export PGHOST="$CLIPP_DB_HOST" PGPORT=5432 PGSSLMODE=verify-full PGSSLROOTCERT=/run/secrets/ca/ca.crt PGCONNECT_TIMEOUT=3
admin_user=$(cat /run/secrets/admin/username)
admin_password=$(cat /run/secrets/admin/password)
serving_user=$(cat /run/secrets/serving/username)
serving_password=$(cat /run/secrets/serving/password)
migration_user=$(cat /run/secrets/migration/username)
migration_password=$(cat /run/secrets/migration/password)
for name in "$admin_user" "$serving_user" "$migration_user" "$CLIPP_DB_NAME"; do
  printf '%s' "$name" | grep -Eq '^[a-z][a-z0-9_]{0,62}$' || { echo 'invalid PostgreSQL role or database name' >&2; exit 1; }
done
[ "$admin_user" != "$serving_user" ] && [ "$admin_user" != "$migration_user" ] && [ "$serving_user" != "$migration_user" ] || { echo 'database role names must differ' >&2; exit 1; }
[ -n "$serving_password" ] && [ -n "$migration_password" ] && [ -n "$admin_password" ] || { echo 'empty database password' >&2; exit 1; }
export PGPASSWORD="$admin_password" PGDATABASE=postgres
ready=false
for i in $(seq 1 60); do
  if psql -X -qAt -U "$admin_user" -c 'SELECT 1' >/dev/null 2>&1; then ready=true; break; fi
  sleep 5
done
[ "$ready" = true ] || { echo 'PostgreSQL admin TLS connection unavailable' >&2; exit 1; }
psql -X -q -v ON_ERROR_STOP=1 -U "$admin_user" \
  -v serving_user="$serving_user" -v serving_password="$serving_password" \
  -v migration_user="$migration_user" -v migration_password="$migration_password" \
  -v dbname="$CLIPP_DB_NAME" -f /etc/postgres/bootstrap-server.sql
# Authenticate both configured passwords before granting application access.
export PGDATABASE="$CLIPP_DB_NAME" PGPASSWORD="$serving_password"
psql -X -qAt -U "$serving_user" -c 'SELECT 1' >/dev/null || { echo 'serving credential mismatch' >&2; exit 1; }
export PGPASSWORD="$migration_password"
psql -X -qAt -U "$migration_user" -c 'SELECT 1' >/dev/null || { echo 'migration credential mismatch' >&2; exit 1; }
export PGPASSWORD="$admin_password"
psql -X -q -v ON_ERROR_STOP=1 -U "$admin_user" \
  -v serving_user="$serving_user" -v migration_user="$migration_user" \
  -f /etc/postgres/bootstrap-schema.sql
