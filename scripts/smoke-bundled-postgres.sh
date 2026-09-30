#!/usr/bin/env bash
set -euo pipefail
# Disposable local PostgreSQL 18 bootstrap proof. Never contacts Kubernetes.
cd "$(dirname "$0")/.."
work=$(mktemp -d)
name="clipp-bundled-smoke-$$"
data_volume="$name-data"
tls_volume="$name-tls"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$data_volume" "$tls_volume" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$work/admin" "$work/serving" "$work/migration" "$work/tls" "$work/ca"
printf 'postgres\n' > "$work/admin/username"
printf 'clipp_serving\n' > "$work/serving/username"
printf 'clipp_migration\n' > "$work/migration/username"
for role in admin serving migration; do
  openssl rand -base64 24 > "$work/$role/password"
done
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Clipp smoke CA' -keyout "$work/ca.key" -out "$work/ca/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=localhost' -keyout "$work/tls/tls.key" -out "$work/tls/server.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' > "$work/tls/server.ext"
openssl x509 -req -in "$work/tls/server.csr" -CA "$work/ca/ca.crt" -CAkey "$work/ca.key" -CAcreateserial -days 1 -extfile "$work/tls/server.ext" -out "$work/tls/tls.crt" >/dev/null 2>&1
chmod 755 "$work" "$work/admin" "$work/serving" "$work/migration" "$work/ca" "$work/tls"
chmod 644 "$work"/{admin,serving,migration}/{username,password} "$work/ca/ca.crt" "$work/tls"/{tls.crt,tls.key}
docker volume create "$data_volume" >/dev/null
docker volume create "$tls_volume" >/dev/null
docker run --rm -v "$data_volume:/var/lib/postgresql" -v "$tls_volume:/target" -v "$work/tls:/source:ro" --entrypoint sh postgres:18 -ec 'chown 999:999 /var/lib/postgresql; cp /source/tls.crt /source/tls.key /target/; chown root:999 /target/tls.key; chmod 640 /target/tls.key' >/dev/null
docker run --rm --user 999:999 -v "$data_volume:/var/lib/postgresql" -v "$work/admin:/run/secrets/admin:ro" -v "$PWD/charts/clipp-relay/files/postgres:/etc/postgres:ro" -e CLIPP_INITIALIZE=true -e CLIPP_NAMESPACE=relay-portal-test -e CLIPP_RELEASE=isolated --entrypoint /bin/sh postgres:18 /etc/postgres/initialize.sh > "$work/init.log"
docker run -d --name "$name" --user 999:999 -v "$data_volume:/var/lib/postgresql" -v "$tls_volume:/run/secrets/tls:ro" -v "$PWD/charts/clipp-relay/files/postgres:/etc/postgres:ro" --entrypoint postgres postgres:18 -D /var/lib/postgresql/18/docker -c listen_addresses='*' -c hba_file=/etc/postgres/pg_hba.conf -c ssl=on -c ssl_cert_file=/run/secrets/tls/tls.crt -c ssl_key_file=/run/secrets/tls/tls.key -c unix_socket_directories=/tmp >/dev/null
ready=false
for i in $(seq 1 30); do
  if docker exec "$name" pg_isready -h 127.0.0.1 >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
if [[ $ready != true ]]; then docker logs "$name" >&2; exit 1; fi
bootstrap() {
  docker run --rm --network "container:$name" --user 999:999 -v "$work/admin:/run/secrets/admin:ro" -v "$work/serving:/run/secrets/serving:ro" -v "$work/migration:/run/secrets/migration:ro" -v "$work/ca:/run/secrets/ca:ro" -v "$PWD/charts/clipp-relay/files/postgres:/etc/postgres:ro" -e CLIPP_DB_HOST=localhost -e CLIPP_DB_NAME=clipp_relay --entrypoint /bin/sh postgres:18 /etc/postgres/bootstrap.sh
}
bootstrap > "$work/bootstrap.log"
bootstrap > "$work/bootstrap-rerun.log"
cp "$work/serving/password" "$work/original-password"
openssl rand -base64 24 > "$work/serving/password"
if bootstrap > "$work/conflict.log" 2>&1; then
  echo 'bootstrap silently replaced an existing password' >&2
  exit 1
fi
cp "$work/original-password" "$work/serving/password"
docker run --rm --network "container:$name" --user 999:999 -v "$work/serving:/run/secrets/serving:ro" -v "$work/ca:/run/secrets/ca:ro" --entrypoint /bin/sh postgres:18 -ec 'PGPASSWORD=$(cat /run/secrets/serving/password) PGHOST=localhost PGPORT=5432 PGSSLMODE=verify-full PGSSLROOTCERT=/run/secrets/ca/ca.crt psql -X -qAt -U clipp_serving -d clipp_relay -c "SELECT 1"' | grep -qx 1
client() {
  local role=$1 sslmode=$2 ca=$3 sql=$4 host=${5:-localhost}
  docker run --rm --network "container:$name" --user 999:999 \
    -v "$work/$role:/run/secrets/role:ro" -v "$ca:/run/secrets/ca:ro" \
    -e PGHOST="$host" -e PGPORT=5432 -e PGDATABASE=clipp_relay \
    -e PGSSLMODE="$sslmode" -e PGSSLROOTCERT=/run/secrets/ca/ca.crt \
    -e CLIPP_SQL="$sql" --entrypoint /bin/sh postgres:18 -ec \
    'PGPASSWORD=$(cat /run/secrets/role/password) psql -X -qAt -v ON_ERROR_STOP=1 -U "$(cat /run/secrets/role/username)" -c "$CLIPP_SQL"'
}
client migration verify-full "$work/ca" 'CREATE TABLE public.bootstrap_smoke (id integer); INSERT INTO public.bootstrap_smoke VALUES (1)' > "$work/migration-check.log"
client serving verify-full "$work/ca" 'SELECT id FROM public.bootstrap_smoke' | grep -qx 1
if client serving verify-full "$work/ca" 'CREATE TABLE public.forbidden (id integer)' > "$work/ddl.log" 2>&1; then
  echo 'serving role can create tables' >&2; exit 1
fi
if client serving disable "$work/ca" 'SELECT 1' > "$work/plaintext.log" 2>&1; then
  echo 'plaintext client connection accepted' >&2; exit 1
fi
mkdir "$work/wrong-ca"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Wrong smoke CA' -keyout "$work/wrong-ca/key" -out "$work/wrong-ca/ca.crt" >/dev/null 2>&1
chmod 755 "$work/wrong-ca"
chmod 644 "$work/wrong-ca/ca.crt"
if client serving verify-full "$work/wrong-ca" 'SELECT 1' > "$work/wrong-ca.log" 2>&1; then
  echo 'wrong CA accepted' >&2; exit 1
fi
if client serving verify-full "$work/ca" 'SELECT 1' 127.0.0.1 > "$work/wrong-host.log" 2>&1; then
  echo 'wrong TLS hostname accepted' >&2; exit 1
fi
echo 'disposable PostgreSQL 18 bootstrap, rerun, conflict, privilege and TLS smoke passed'
