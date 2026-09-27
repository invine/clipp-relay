#!/usr/bin/env bash
set -euo pipefail

# Runs only against a new local Docker container and a disposable database.
cd "$(dirname "$0")/.."
work=$(mktemp -d)
name="clipp-ticket02-$$"
supported_name="clipp-ticket02-supported17-$$"
wrong_name="clipp-ticket02-wrong-major-$$"
created_ids=()
cleanup() {
  local status=$?
  if ((status != 0)) && [[ -f "$work/service.log" ]]; then
    echo 'service startup diagnostics:' >&2
    cat "$work/service.log" >&2
  fi
  if [[ -n "${pid:-}" ]]; then kill "$pid" >/dev/null 2>&1 || true; wait "$pid" >/dev/null 2>&1 || true; fi
  if [[ -n "${journal_proxy_pid:-}" ]]; then kill "$journal_proxy_pid" >/dev/null 2>&1 || true; wait "$journal_proxy_pid" >/dev/null 2>&1 || true; fi
  if ((${#created_ids[@]})); then docker rm -f "${created_ids[@]}" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

wait_for_pg() {
  local target=$1
  for i in $(seq 1 60); do
    if docker exec "$target" pg_isready -U postgres >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  docker exec "$target" pg_isready -U postgres >/dev/null
}

start_tls_pg() {
  local target=$1 major=$2 created_id
  created_id=$(docker create --name "$target" -e POSTGRES_PASSWORD="$admin_pass" -p 127.0.0.1::5432 "postgres:$major")
  created_ids+=("$created_id")
  docker start "$created_id" >/dev/null
  wait_for_pg "$target"
  docker cp "$work/server.crt" "$target:/var/lib/postgresql/server.crt"
  docker cp "$work/server.key" "$target:/var/lib/postgresql/server.key"
  docker exec -u root "$target" chown postgres:postgres /var/lib/postgresql/server.crt /var/lib/postgresql/server.key
  docker exec -u root "$target" chmod 600 /var/lib/postgresql/server.key
  docker exec -e PGPASSWORD="$admin_pass" "$target" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl = on" >/dev/null
  docker exec -e PGPASSWORD="$admin_pass" "$target" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_cert_file = '/var/lib/postgresql/server.crt'" >/dev/null
  docker exec -e PGPASSWORD="$admin_pass" "$target" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_key_file = '/var/lib/postgresql/server.key'" >/dev/null
  docker exec -u root "$target" sh -c 'printf "hostnossl all all all reject\nhostssl all all all scram-sha-256\n" > /tmp/clipp-hba; cat "$PGDATA/pg_hba.conf" >> /tmp/clipp-hba; cat /tmp/clipp-hba > "$PGDATA/pg_hba.conf"'
  docker restart "$target" >/dev/null
  wait_for_pg "$target"
}

expect_ddl_role_rejected() {
  local label=$1 probe
  "$work/clipp-relay" -command serve -config "$work/serving.json" > "$work/$label.log" 2>&1 &
  probe=$!
  for i in $(seq 1 10); do
    if ! kill -0 "$probe" 2>/dev/null; then break; fi
    sleep 1
  done
  if kill -0 "$probe" 2>/dev/null; then
    kill "$probe"; wait "$probe" || true
    echo "$label serving role was accepted" >&2
    return 1
  fi
  if wait "$probe"; then
    echo "$label serving role was accepted" >&2
    return 1
  fi
  if ! grep -q 'serving database role has DDL authority' "$work/$label.log"; then
    cat "$work/$label.log" >&2
    return 1
  fi
}

migrate_when_ready() {
  local config_path=$1
  for i in $(seq 1 30); do
    if "$work/clipp-relay" -command migrate -config "$config_path" > "$work/migrate.log" 2>&1; then
      cat "$work/migrate.log"
      return 0
    fi
    sleep 1
  done
  cat "$work/migrate.log" >&2
  return 1
}

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Clipp test CA' -keyout "$work/ca.key" -out "$work/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=localhost' -keyout "$work/server.key" -out "$work/server.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' > "$work/server.ext"
openssl x509 -req -in "$work/server.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -days 1 -extfile "$work/server.ext" -out "$work/server.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=objectstorage.us-ashburn-1.oraclecloud.com' -keyout "$work/journal-tls.key" -out "$work/journal-tls.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:objectstorage.us-ashburn-1.oraclecloud.com\nextendedKeyUsage=serverAuth\n' > "$work/journal-tls.ext"
openssl x509 -req -in "$work/journal-tls.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -days 1 -extfile "$work/journal-tls.ext" -out "$work/journal-tls.crt" >/dev/null 2>&1
openssl genrsa -out "$work/journal-signing.key" 2048 >/dev/null 2>&1
python3 scripts/journal-fixture-proxy.py "$work/journal-tls.crt" "$work/journal-tls.key" "$work/journal-proxy-port" > "$work/journal-proxy.log" 2>&1 &
journal_proxy_pid=$!
for i in $(seq 1 30); do
  if [[ -s "$work/journal-proxy-port" ]]; then break; fi
  sleep 1
done
test -s "$work/journal-proxy-port"
journal_proxy_port=$(cat "$work/journal-proxy-port")
zero_hash=$(printf '%064d' 0)

admin_pass=$(openssl rand -hex 24)
migration_pass=$(openssl rand -hex 24)
serving_pass=$(openssl rand -hex 24)
start_tls_pg "$name" 18

docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_migration LOGIN PASSWORD '$migration_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_serving LOGIN PASSWORD '$serving_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER ROLE clipp_serving SET temp_file_limit = '64MB'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE DATABASE clipp_ticket02_smoke" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "REVOKE TEMPORARY ON DATABASE clipp_ticket02_smoke FROM PUBLIC" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c "ALTER SCHEMA public OWNER TO clipp_migration; REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT USAGE ON SCHEMA public TO clipp_serving" >/dev/null
if docker exec -e PGPASSWORD="$serving_pass" "$name" psql 'host=localhost port=5432 dbname=clipp_ticket02_smoke user=clipp_serving sslmode=disable' -c 'SELECT 1' > "$work/plaintext.log" 2>&1; then
  echo 'plaintext PostgreSQL unexpectedly accepted' >&2; exit 1
fi
port=$(docker port "$name" 5432/tcp | sed -n 's/.*://p')
printf 'clipp_migration\n' > "$work/migration-user"
printf '%s\n' "$migration_pass" > "$work/migration-pass"
printf 'clipp_serving\n' > "$work/serving-user"
printf '%s\n' "$serving_pass" > "$work/serving-pass"
printf 'test-client\n' > "$work/google-id"
printf 'test-secret\n' > "$work/google-secret"
printf '{"revision":1,"emails":[]}\n' > "$work/allowlist.json"
printf '{"current":1,"keys":[{"version":1,"material":"%s"}]}\n' "$(openssl rand -base64 32 | tr -d '\n')" > "$work/keyring.json"
cat > "$work/migration.json" <<EOF
{"version":1,"portal_origin":"https://portal.example.test","wss_hostname":"wss.example.test","public_clients":{"android_redirect":"clipp-relay://oauth/callback","extension_redirect":"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"},"listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/18082"},"database":{"mode":"external","host":"localhost","port":$port,"name":"clipp_ticket02_smoke","username_file":"$work/migration-user","password_file":"$work/migration-pass","ca_file":"$work/ca.crt"},"journal":{"region":"us-ashburn-1","namespace":"fixture","bucket":"events","repository_id":"local-smoke-repository","coverage_floor":0,"coverage_hash":"$zero_hash","tenancy_ocid":"ocid1.tenancy.oc1..test","user_ocid":"ocid1.user.oc1..test","fingerprint":"aa:bb","private_key_file":"$work/journal-signing.key"},"secrets":{"google_client_id_file":"$work/google-id","google_client_secret_file":"$work/google-secret","admin_allowlist_file":"$work/allowlist.json","pepper_keyring_file":"$work/keyring.json"}}
EOF
sed "s|migration-user|serving-user|;s|migration-pass|serving-pass|" "$work/migration.json" > "$work/serving.json"

export GOCACHE="${GOCACHE:-/private/tmp/clipp-go-cache}"
go build -o "$work/clipp-relay" ./cmd/clipp-relay
migrate_when_ready "$work/migration.json"
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT ON public.schema_migrations TO clipp_serving' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT, INSERT, UPDATE, DELETE ON public.accounts, public.authorization_transactions, public.portal_sessions, public.audit_events, public.quota_plans, public.authorization_codes, public.login_grants, public.refresh_generations, public.relay_access_tokens, public.weekly_quota_usage, public.deletion_operations, public.deletion_capacity, public.retained_quota_usage TO clipp_serving' >/dev/null
python3 - "$work/serving.json" "$work/missing-journal.json" "$work/wrong-journal.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as source: config=json.load(source)
missing=dict(config); missing.pop('journal')
wrong=dict(config); wrong['journal']=dict(config['journal']); wrong['journal']['repository_id']='wrong-repository'
for path, value in ((sys.argv[2],missing),(sys.argv[3],wrong)):
    with open(path,'w',encoding='utf-8') as output: json.dump(value,output)
PY
if env HTTPS_PROXY="http://127.0.0.1:$journal_proxy_port" SSL_CERT_FILE="$work/ca.crt" NO_PROXY=localhost,127.0.0.1 "$work/clipp-relay" -command serve -config "$work/missing-journal.json" > "$work/missing-journal.log" 2>&1; then
  echo 'serving accepted missing journal' >&2; exit 1
fi
grep -q 'journal configuration required' "$work/missing-journal.log"
if env HTTPS_PROXY="http://127.0.0.1:$journal_proxy_port" SSL_CERT_FILE="$work/ca.crt" NO_PROXY=localhost,127.0.0.1 "$work/clipp-relay" -command serve -config "$work/wrong-journal.json" > "$work/wrong-journal.log" 2>&1; then
  echo 'serving accepted wrong journal' >&2; exit 1
fi
grep -q 'journal qualification failed' "$work/wrong-journal.log"
env HTTPS_PROXY="http://127.0.0.1:$journal_proxy_port" SSL_CERT_FILE="$work/ca.crt" NO_PROXY=localhost,127.0.0.1 "$work/clipp-relay" -command serve -config "$work/serving.json" > "$work/service.log" 2>&1 &
pid=$!
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18081/livez > "$work/livez" 2>/dev/null; then break; fi
  sleep 1
done
test "$(cat "$work/livez")" = ok
echo 'PostgreSQL 18 livez passed'
test "$(curl -s -o "$work/readyz" -w '%{http_code}' http://127.0.0.1:18081/readyz)" = 503
test "$(cat "$work/readyz")" = unavailable
test "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/livez)" = 404
echo 'PostgreSQL 18 health and public isolation passed'
docker pause "$name" >/dev/null
test "$(curl -s -o "$work/livez" -w '%{http_code}' http://127.0.0.1:18081/livez)" = 200
test "$(curl -s -o "$work/readyz" -w '%{http_code}' http://127.0.0.1:18081/readyz)" = 503
echo 'PostgreSQL 18 outage health passed'
kill "$pid"; wait "$pid" || true
pid=
docker unpause "$name" >/dev/null
wait_for_pg "$name"
migrate_when_ready "$work/migration.json"
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'ALTER TABLE public.schema_migrations OWNER TO clipp_serving' >/dev/null
expect_ddl_role_rejected direct-owner
echo 'Direct table ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'ALTER TABLE public.schema_migrations OWNER TO clipp_migration' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'CREATE ROLE clipp_table_owner NOLOGIN; ALTER TABLE public.schema_migrations OWNER TO clipp_table_owner; GRANT clipp_table_owner TO clipp_serving' >/dev/null
expect_ddl_role_rejected member-owner
echo 'Inherited table ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'ALTER TABLE public.schema_migrations OWNER TO clipp_migration; REVOKE clipp_table_owner FROM clipp_serving; DROP ROLE clipp_table_owner' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT ON public.schema_migrations TO clipp_serving' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c 'ALTER DATABASE clipp_ticket02_smoke OWNER TO clipp_serving; REVOKE CREATE, TEMPORARY ON DATABASE clipp_ticket02_smoke FROM clipp_serving' >/dev/null
test "$(docker exec -e PGPASSWORD="$serving_pass" "$name" psql -At -h localhost -U clipp_serving -d clipp_ticket02_smoke -c "SELECT has_database_privilege(current_database(),'CREATE'),has_database_privilege(current_database(),'TEMP')")" = 'f|f'
expect_ddl_role_rejected direct-database-owner
echo 'Direct database ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c 'ALTER DATABASE clipp_ticket02_smoke OWNER TO postgres; CREATE ROLE clipp_database_owner NOLOGIN; ALTER DATABASE clipp_ticket02_smoke OWNER TO clipp_database_owner; REVOKE CREATE, TEMPORARY ON DATABASE clipp_ticket02_smoke FROM clipp_database_owner; GRANT clipp_database_owner TO clipp_serving' >/dev/null
test "$(docker exec -e PGPASSWORD="$serving_pass" "$name" psql -At -h localhost -U clipp_serving -d clipp_ticket02_smoke -c "SELECT has_database_privilege(current_database(),'CREATE'),has_database_privilege(current_database(),'TEMP')")" = 'f|f'
expect_ddl_role_rejected member-database-owner
echo 'Inherited database ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c 'ALTER DATABASE clipp_ticket02_smoke OWNER TO postgres; REVOKE clipp_database_owner FROM clipp_serving; DROP ROLE clipp_database_owner' >/dev/null
# An empty disposable extension has an owner but no table, routine, or type
# that the enumerated ownership checks could otherwise catch.
extension_dir=$(docker exec "$name" pg_config --sharedir | tr -d '\r')/extension
printf "comment = 'Disposable ownership probe'\ndefault_version = '1.0'\nrelocatable = true\nsuperuser = false\n" > "$work/clipp_ownership_probe.control"
printf '%s\n' '-- no member objects' > "$work/clipp_ownership_probe--1.0.sql"
docker cp "$work/clipp_ownership_probe.control" "$name:$extension_dir/clipp_ownership_probe.control"
docker cp "$work/clipp_ownership_probe--1.0.sql" "$name:$extension_dir/clipp_ownership_probe--1.0.sql"
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT CREATE ON DATABASE clipp_ticket02_smoke TO clipp_serving; GRANT CREATE ON SCHEMA public TO clipp_serving' >/dev/null
docker exec -e PGPASSWORD="$serving_pass" "$name" psql -v ON_ERROR_STOP=1 -h localhost -U clipp_serving -d clipp_ticket02_smoke -c 'CREATE EXTENSION clipp_ownership_probe' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'REVOKE CREATE ON DATABASE clipp_ticket02_smoke FROM clipp_serving; REVOKE CREATE ON SCHEMA public FROM clipp_serving' >/dev/null
test "$(docker exec -e PGPASSWORD="$serving_pass" "$name" psql -At -h localhost -U clipp_serving -d clipp_ticket02_smoke -c "SELECT has_database_privilege(current_database(),'CREATE'),has_schema_privilege('public','CREATE')")" = 'f|f'
test "$(docker exec -e PGPASSWORD="$admin_pass" "$name" psql -At -U postgres -d clipp_ticket02_smoke -c "SELECT extowner::regrole::text FROM pg_extension WHERE extname='clipp_ownership_probe'")" = 'clipp_serving'
expect_ddl_role_rejected extension-owner
echo 'Extension ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'DROP EXTENSION clipp_ownership_probe' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'CREATE ROLE clipp_extension_owner NOLOGIN; GRANT CREATE ON DATABASE clipp_ticket02_smoke TO clipp_extension_owner; GRANT CREATE ON SCHEMA public TO clipp_extension_owner' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'SET ROLE clipp_extension_owner; CREATE EXTENSION clipp_ownership_probe; RESET ROLE' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'REVOKE CREATE ON DATABASE clipp_ticket02_smoke FROM clipp_extension_owner; REVOKE CREATE ON SCHEMA public FROM clipp_extension_owner; GRANT clipp_extension_owner TO clipp_serving' >/dev/null
test "$(docker exec -e PGPASSWORD="$serving_pass" "$name" psql -At -h localhost -U clipp_serving -d clipp_ticket02_smoke -c "SELECT has_database_privilege(current_database(),'CREATE'),has_schema_privilege('public','CREATE')")" = 'f|f'
expect_ddl_role_rejected member-extension-owner
echo 'Inherited extension ownership rejection passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'DROP EXTENSION clipp_ownership_probe; REVOKE clipp_extension_owner FROM clipp_serving; DROP ROLE clipp_extension_owner' >/dev/null
CLIPP_TEST_MIGRATION_CONFIG="$work/migration.json" CLIPP_TEST_SERVING_CONFIG="$work/serving.json" go test -count=1 ./internal/database
echo 'PostgreSQL 18 integration tests passed'
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT, INSERT, UPDATE, DELETE ON public.accounts, public.authorization_transactions, public.portal_sessions, public.audit_events, public.quota_plans, public.authorization_codes, public.login_grants, public.refresh_generations, public.relay_access_tokens, public.weekly_quota_usage, public.deletion_operations, public.deletion_capacity, public.retained_quota_usage TO clipp_serving' >/dev/null
CLIPP_TEST_MIGRATION_CONFIG="$work/migration.json" CLIPP_TEST_SERVING_CONFIG="$work/serving.json" go test -count=1 ./internal/auth
echo 'PostgreSQL 18 OIDC browser integration tests passed'
CLIPP_TEST_SERVING_CONFIG="$work/serving.json" go test -count=1 ./internal/quota
echo 'PostgreSQL 18 quota integration tests passed'

# Both supported majors must migrate and start with the same verified TLS policy.
start_tls_pg "$supported_name" 17
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_migration LOGIN PASSWORD '$migration_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_serving LOGIN PASSWORD '$serving_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER ROLE clipp_serving SET temp_file_limit = '64MB'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -c 'CREATE DATABASE clipp_ticket02_smoke' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -c 'REVOKE TEMPORARY ON DATABASE clipp_ticket02_smoke FROM PUBLIC' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'ALTER SCHEMA public OWNER TO clipp_migration; REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT USAGE ON SCHEMA public TO clipp_serving' >/dev/null
supported_port=$(docker port "$supported_name" 5432/tcp | sed -n 's/.*://p')
sed "s/\"port\":$port/\"port\":$supported_port/" "$work/migration.json" > "$work/migration17.json"
sed "s/\"port\":$port/\"port\":$supported_port/" "$work/serving.json" > "$work/serving17.json"
migrate_when_ready "$work/migration17.json"
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT ON public.schema_migrations TO clipp_serving' >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$supported_name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT, INSERT, UPDATE, DELETE ON public.accounts, public.authorization_transactions, public.portal_sessions, public.audit_events, public.quota_plans, public.authorization_codes, public.login_grants, public.refresh_generations, public.relay_access_tokens, public.weekly_quota_usage, public.deletion_operations, public.deletion_capacity, public.retained_quota_usage TO clipp_serving' >/dev/null
env HTTPS_PROXY="http://127.0.0.1:$journal_proxy_port" SSL_CERT_FILE="$work/ca.crt" NO_PROXY=localhost,127.0.0.1 "$work/clipp-relay" -command serve -config "$work/serving17.json" > "$work/service17.log" 2>&1 &
pid=$!
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18081/livez > "$work/livez17" 2>/dev/null; then break; fi
  sleep 1
done
test "$(cat "$work/livez17")" = ok
test "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18081/readyz)" = 503
kill "$pid"; wait "$pid" || true
pid=

# A separate verified-TLS PostgreSQL 16 target must fail before migration.
start_tls_pg "$wrong_name" 16
docker exec -e PGPASSWORD="$admin_pass" "$wrong_name" psql -v ON_ERROR_STOP=1 -U postgres -c 'CREATE DATABASE clipp_ticket02_smoke' >/dev/null
wrong_port=$(docker port "$wrong_name" 5432/tcp | sed -n 's/.*://p')
printf 'postgres\n' > "$work/admin-user"
printf '%s\n' "$admin_pass" > "$work/admin-pass"
sed "s/\"port\":$port/\"port\":$wrong_port/;s|migration-user|admin-user|;s|migration-pass|admin-pass|" "$work/migration.json" > "$work/wrong-major.json"
if "$work/clipp-relay" -command migrate -config "$work/wrong-major.json" > "$work/wrong-major.log" 2>&1; then
  echo 'wrong PostgreSQL major unexpectedly accepted' >&2; exit 1
fi
grep -q 'unsupported PostgreSQL major' "$work/wrong-major.log"
echo 'smoke passed: verified-TLS PostgreSQL 17/18 migrate and serve, table/database/extension ownership rejection, role/schema/rollback/concurrency, private health, public isolation, lost database, PostgreSQL 16 rejection'
