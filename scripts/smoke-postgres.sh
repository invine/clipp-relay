#!/usr/bin/env bash
set -euo pipefail

# Runs only against a new local Docker container and a disposable database.
cd "$(dirname "$0")/.."
work=$(mktemp -d)
name="clipp-ticket02-$$"
wrong_name="clipp-ticket02-wrong-major-$$"
cleanup() { if [[ -n "${pid:-}" ]]; then kill "$pid" >/dev/null 2>&1 || true; wait "$pid" >/dev/null 2>&1 || true; fi; docker rm -f "$name" "$wrong_name" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Clipp test CA' -keyout "$work/ca.key" -out "$work/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=localhost' -keyout "$work/server.key" -out "$work/server.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' > "$work/server.ext"
openssl x509 -req -in "$work/server.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -days 1 -extfile "$work/server.ext" -out "$work/server.crt" >/dev/null 2>&1

admin_pass=$(openssl rand -hex 24)
migration_pass=$(openssl rand -hex 24)
serving_pass=$(openssl rand -hex 24)
docker run -d --name "$name" -e POSTGRES_PASSWORD="$admin_pass" -p 127.0.0.1::5432 postgres:18 >/dev/null
for i in $(seq 1 60); do
  if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$name" pg_isready -U postgres >/dev/null
docker cp "$work/server.crt" "$name:/var/lib/postgresql/server.crt"
docker cp "$work/server.key" "$name:/var/lib/postgresql/server.key"
docker exec -u root "$name" chown postgres:postgres /var/lib/postgresql/server.crt /var/lib/postgresql/server.key
docker exec -u root "$name" chmod 600 /var/lib/postgresql/server.key
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl = on" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_cert_file = '/var/lib/postgresql/server.crt'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_key_file = '/var/lib/postgresql/server.key'" >/dev/null
docker exec -u root "$name" sh -c 'printf "hostnossl all all all reject\nhostssl all all all scram-sha-256\n" > /tmp/clipp-hba; cat "$PGDATA/pg_hba.conf" >> /tmp/clipp-hba; cat /tmp/clipp-hba > "$PGDATA/pg_hba.conf"'
docker restart "$name" >/dev/null
for i in $(seq 1 60); do
  if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$name" pg_isready -U postgres >/dev/null

docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_migration LOGIN PASSWORD '$migration_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE ROLE clipp_serving LOGIN PASSWORD '$serving_pass'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER ROLE clipp_serving SET temp_file_limit = '64MB'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -c "CREATE DATABASE clipp_ticket02_smoke" >/dev/null
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
{"version":1,"portal_origin":"https://portal.example.test","wss_hostname":"wss.example.test","listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"database":{"mode":"external","host":"localhost","port":$port,"name":"clipp_ticket02_smoke","username_file":"$work/migration-user","password_file":"$work/migration-pass","ca_file":"$work/ca.crt"},"secrets":{"google_client_id_file":"$work/google-id","google_client_secret_file":"$work/google-secret","admin_allowlist_file":"$work/allowlist.json","pepper_keyring_file":"$work/keyring.json"}}
EOF
sed "s|migration-user|serving-user|;s|migration-pass|serving-pass|" "$work/migration.json" > "$work/serving.json"

export GOCACHE="${GOCACHE:-/private/tmp/clipp-go-cache}"
go build -o "$work/clipp-relay" ./cmd/clipp-relay
"$work/clipp-relay" -command migrate -config "$work/migration.json"
docker exec -e PGPASSWORD="$admin_pass" "$name" psql -v ON_ERROR_STOP=1 -U postgres -d clipp_ticket02_smoke -c 'GRANT SELECT ON public.schema_migrations TO clipp_serving' >/dev/null
"$work/clipp-relay" -command serve -config "$work/serving.json" > "$work/service.log" 2>&1 &
pid=$!
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18081/livez > "$work/livez"; then break; fi
  sleep 1
done
test "$(cat "$work/livez")" = ok
test "$(curl -s -o "$work/readyz" -w '%{http_code}' http://127.0.0.1:18081/readyz)" = 503
test "$(cat "$work/readyz")" = unavailable
test "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/livez)" = 404
docker stop "$name" >/dev/null
test "$(curl -s -o "$work/livez" -w '%{http_code}' http://127.0.0.1:18081/livez)" = 200
test "$(curl -s -o "$work/readyz" -w '%{http_code}' http://127.0.0.1:18081/readyz)" = 503
kill "$pid"; wait "$pid" || true
pid=
docker start "$name" >/dev/null
for i in $(seq 1 60); do
  if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
CLIPP_TEST_MIGRATION_CONFIG="$work/migration.json" CLIPP_TEST_SERVING_CONFIG="$work/serving.json" go test -count=1 ./internal/database

# A separate supported-TLS PostgreSQL 16 target must fail before migration.
docker run -d --name "$wrong_name" -e POSTGRES_PASSWORD="$admin_pass" -p 127.0.0.1::5432 postgres:16 >/dev/null
for i in $(seq 1 60); do if docker exec "$wrong_name" pg_isready -U postgres >/dev/null 2>&1; then break; fi; sleep 1; done
docker exec "$wrong_name" pg_isready -U postgres >/dev/null
docker cp "$work/server.crt" "$wrong_name:/var/lib/postgresql/server.crt"
docker cp "$work/server.key" "$wrong_name:/var/lib/postgresql/server.key"
docker exec -u root "$wrong_name" chown postgres:postgres /var/lib/postgresql/server.crt /var/lib/postgresql/server.key
docker exec -u root "$wrong_name" chmod 600 /var/lib/postgresql/server.key
docker exec -e PGPASSWORD="$admin_pass" "$wrong_name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl = on" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$wrong_name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_cert_file = '/var/lib/postgresql/server.crt'" >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$wrong_name" psql -v ON_ERROR_STOP=1 -U postgres -c "ALTER SYSTEM SET ssl_key_file = '/var/lib/postgresql/server.key'" >/dev/null
docker exec -u root "$wrong_name" sh -c 'printf "hostnossl all all all reject\nhostssl all all all scram-sha-256\n" > /tmp/clipp-hba; cat "$PGDATA/pg_hba.conf" >> /tmp/clipp-hba; cat /tmp/clipp-hba > "$PGDATA/pg_hba.conf"'
docker restart "$wrong_name" >/dev/null
for i in $(seq 1 60); do if docker exec "$wrong_name" pg_isready -U postgres >/dev/null 2>&1; then break; fi; sleep 1; done
docker exec "$wrong_name" pg_isready -U postgres >/dev/null
docker exec -e PGPASSWORD="$admin_pass" "$wrong_name" psql -v ON_ERROR_STOP=1 -U postgres -c 'CREATE DATABASE clipp_ticket02_smoke' >/dev/null
wrong_port=$(docker port "$wrong_name" 5432/tcp | sed -n 's/.*://p')
printf 'postgres\n' > "$work/admin-user"
printf '%s\n' "$admin_pass" > "$work/admin-pass"
sed "s/\"port\":$port/\"port\":$wrong_port/;s|migration-user|admin-user|;s|migration-pass|admin-pass|" "$work/migration.json" > "$work/wrong-major.json"
if "$work/clipp-relay" -command migrate -config "$work/wrong-major.json" > "$work/wrong-major.log" 2>&1; then
  echo 'wrong PostgreSQL major unexpectedly accepted' >&2; exit 1
fi
grep -q 'unsupported PostgreSQL major' "$work/wrong-major.log"
echo 'smoke passed: TLS PostgreSQL 18 migrate/serve/role/schema/rollback/concurrency, private health, public isolation, lost database, PostgreSQL 16 rejection'
