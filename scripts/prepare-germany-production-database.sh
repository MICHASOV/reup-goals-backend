#!/usr/bin/env bash

set -Eeuo pipefail

source_env=${REUP_SOURCE_ENV:-/etc/reup-goals-production/backend.env}
config_root=${REUP_PRODUCTION_CONFIG_ROOT:-/etc/reup-goals-production}
candidate_env=${REUP_DATABASE_CANDIDATE_ENV:-${config_root}/backend.local-db.env}
database_env=${REUP_LOCAL_DATABASE_ENV:-${config_root}/database.env}
backup_root=${REUP_DATABASE_BACKUP_ROOT:-/var/backups/reup-goals/database-migration}
local_database=${REUP_LOCAL_DATABASE_NAME:-reup_goals_production}
local_user=${REUP_LOCAL_DATABASE_USER:-reup_goals_production}

read_env() {
  grep -E "^$2=" "$1" | tail -1 | cut -d= -f2- || true
}

set_env() {
  local file=$1 key=$2 value=$3 escaped
  escaped=$(printf '%s' "$value" | sed 's/[&|]/\\&/g')
  if grep -qE "^${key}=" "$file"; then
    sed -i -E "s|^${key}=.*$|${key}=${escaped}|" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

fail() {
  printf 'DATABASE_PREPARATION_FAILED=%s\n' "$1" >&2
  exit 1
}

[[ $EUID -eq 0 ]] || fail "run_as_root"
[[ -s "$source_env" ]] || fail "missing_source_environment"

source_host=$(read_env "$source_env" DB_HOST)
source_port=$(read_env "$source_env" DB_PORT)
source_user=$(read_env "$source_env" DB_USER)
source_password=$(read_env "$source_env" DB_PASSWORD)
source_database=$(read_env "$source_env" DB_NAME)
source_sslmode=$(read_env "$source_env" DB_SSLMODE)

[[ -n "$source_host" && -n "$source_user" && -n "$source_password" && -n "$source_database" ]] || \
  fail "incomplete_source_database_configuration"
source_port=${source_port:-5432}
source_sslmode=${source_sslmode:-require}

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl postgresql-common >/dev/null
install -d -m 755 /usr/share/postgresql-common/pgdg
curl --fail --silent --show-error \
  https://www.postgresql.org/media/keys/ACCC4CF8.asc \
  -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
printf 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' \
  "$(. /etc/os-release && printf '%s' "$VERSION_CODENAME")" \
  > /etc/apt/sources.list.d/pgdg.list
apt-get update -qq
apt-get install -y -qq postgresql-18 postgresql-client-18 >/dev/null

if ! pg_lsclusters --no-header | awk '$1 == "18" && $2 == "main" {found=1} END {exit !found}'; then
  pg_createcluster 18 main --start >/dev/null
fi
systemctl enable --now postgresql >/dev/null

local_port=$(pg_lsclusters --no-header | awk '$1 == "18" && $2 == "main" {print $3; exit}')
[[ -n "$local_port" ]] || fail "postgresql_18_cluster_missing"
[[ $(pg_lsclusters --no-header | awk '$1 == "18" && $2 == "main" {print $4; exit}') == online ]] || \
  pg_ctlcluster 18 main start

install -d -m 700 "$config_root" "$backup_root"
if [[ -s "$database_env" ]]; then
  local_password=$(read_env "$database_env" DB_PASSWORD)
else
  local_password="$(openssl rand -hex 32)"
fi
[[ -n "$local_password" ]] || fail "local_database_password_missing"

cat > "$database_env" <<EOF
DB_HOST=127.0.0.1
DB_PORT=${local_port}
DB_USER=${local_user}
DB_PASSWORD=${local_password}
DB_NAME=${local_database}
DB_SSLMODE=disable
EOF
chmod 600 "$database_env"

runuser -u postgres -- psql --port "$local_port" -v ON_ERROR_STOP=1 \
  --set=role_name="$local_user" --set=role_password="$local_password" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'role_name', :'role_password') \gexec
SQL

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
dump_file="${backup_root}/production-${timestamp}.dump"
source_manifest="${backup_root}/production-${timestamp}.source.tsv"
local_manifest="${backup_root}/production-${timestamp}.local.tsv"

export PGHOST="$source_host"
export PGPORT="$source_port"
export PGUSER="$source_user"
export PGPASSWORD="$source_password"
export PGDATABASE="$source_database"
export PGSSLMODE="$source_sslmode"
export PGCONNECT_TIMEOUT=15

source_version=$(psql -Atqc "SHOW server_version")
source_size=$(psql -Atqc "SELECT pg_database_size(current_database())")
source_tables=$(psql -Atqc "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')")
source_extensions=$(psql -Atqc "SELECT string_agg(extname || ':' || extversion, ',' ORDER BY extname) FROM pg_extension")

/usr/lib/postgresql/18/bin/pg_dump \
  --format=custom \
  --compress=zstd:6 \
  --no-owner \
  --no-acl \
  --file "$dump_file"
chmod 600 "$dump_file"

psql -AtF $'\t' <<'SQL' > "$source_manifest"
SELECT n.nspname || '.' || c.relname,
       COALESCE(s.n_live_tup, 0)::bigint,
       pg_total_relation_size(c.oid)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1;
SQL

unset PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE PGSSLMODE PGCONNECT_TIMEOUT

runuser -u postgres -- dropdb --port "$local_port" --if-exists "$local_database"
runuser -u postgres -- createdb --port "$local_port" --owner "$local_user" "$local_database"
runuser -u postgres -- /usr/lib/postgresql/18/bin/pg_restore \
  --port "$local_port" \
  --dbname "$local_database" \
  --role "$local_user" \
  --no-owner \
  --no-acl \
  --exit-on-error \
  "$dump_file"
runuser -u postgres -- psql --port "$local_port" --dbname "$local_database" \
  -v ON_ERROR_STOP=1 -c 'ANALYZE' >/dev/null

local_size=$(runuser -u postgres -- psql --port "$local_port" --dbname "$local_database" -Atqc \
  "SELECT pg_database_size(current_database())")
local_tables=$(runuser -u postgres -- psql --port "$local_port" --dbname "$local_database" -Atqc \
  "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')")
local_extensions=$(runuser -u postgres -- psql --port "$local_port" --dbname "$local_database" -Atqc \
  "SELECT string_agg(extname || ':' || extversion, ',' ORDER BY extname) FROM pg_extension")
runuser -u postgres -- psql --port "$local_port" --dbname "$local_database" -AtF $'\t' <<'SQL' > "$local_manifest"
SELECT n.nspname || '.' || c.relname,
       COALESCE(s.n_live_tup, 0)::bigint,
       pg_total_relation_size(c.oid)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1;
SQL
chmod 600 "$source_manifest" "$local_manifest"

[[ "$local_tables" == "$source_tables" ]] || fail "table_count_mismatch"
[[ "$local_extensions" == "$source_extensions" ]] || fail "extension_mismatch"

cp "$source_env" "$candidate_env"
chmod 600 "$candidate_env"
set_env "$candidate_env" DB_HOST 127.0.0.1
set_env "$candidate_env" DB_PORT "$local_port"
set_env "$candidate_env" DB_USER "$local_user"
set_env "$candidate_env" DB_PASSWORD "$local_password"
set_env "$candidate_env" DB_NAME "$local_database"
set_env "$candidate_env" DB_SSLMODE disable
set_env "$candidate_env" DB_APPLICATION_NAME reup-goals-production-germany
set_env "$candidate_env" DATA_RESIDENCY_REGION eu-de
set_env "$candidate_env" CROSS_BORDER_TRANSFER_REGISTERED true
set_env "$candidate_env" DB_MAX_OPEN_CONNS 20
set_env "$candidate_env" DB_MAX_IDLE_CONNS 5
set_env "$candidate_env" DB_CONN_MAX_IDLE_TIME 5m

find "$backup_root" -maxdepth 1 -type f -name 'production-*.dump' -printf '%T@ %p\n' \
  | sort -nr | awk 'NR > 5 {print $2}' | xargs -r rm -f

printf 'SOURCE_POSTGRES_VERSION=%s\n' "$source_version"
printf 'SOURCE_DATABASE_SIZE_BYTES=%s\n' "$source_size"
printf 'LOCAL_DATABASE_SIZE_BYTES=%s\n' "$local_size"
printf 'SOURCE_TABLES=%s\n' "$source_tables"
printf 'LOCAL_TABLES=%s\n' "$local_tables"
printf 'DATABASE_EXTENSIONS=%s\n' "$local_extensions"
printf 'LOCAL_POSTGRES_PORT=%s\n' "$local_port"
printf 'SNAPSHOT_PATH=%s\n' "$dump_file"
printf 'DATABASE_PREPARATION_READY=true\n'
