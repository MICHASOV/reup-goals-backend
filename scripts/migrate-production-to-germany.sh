#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
frontend_root="${FRONTEND_REPO:-$(cd "$repo_root/.." && pwd)/reup-goals-landing}"
legacy_target="${REUP_LEGACY_PRODUCTION_SSH_TARGET:-reup}"
legacy_key="${REUP_LEGACY_PRODUCTION_KEY:-}"
production_host="${REUP_PRODUCTION_HOST:-167.233.230.212}"
production_user="${REUP_PRODUCTION_USER:-root}"
production_target="${REUP_PRODUCTION_SSH_TARGET:-${production_user}@${production_host}}"
production_key="${REUP_PRODUCTION_KEY:-$HOME/.ssh/reup_goals_staging_deploy}"
letsencrypt_email="${LETSENCRYPT_EMAIL:-reupgoals@gmail.com}"
migration_phase="${REUP_MIGRATION_PHASE:-all}"
skip_frontend="${REUP_SKIP_FRONTEND:-false}"
current_stage="initialization"
temporary_base="${TMPDIR:-/tmp}"
temporary_root="$(mktemp -d "${temporary_base%/}/reup-germany-cutover.XXXXXX")"
legacy_env="$temporary_root/backend.env"
german_ai_env="$temporary_root/ai-production.env"

SSH_ARGS=(-o BatchMode=yes -o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o IdentitiesOnly=yes)
if [[ -f "$production_key" ]]; then
  SSH_ARGS+=(-i "$production_key")
fi
LEGACY_SSH_ARGS=(-o BatchMode=yes -o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3)
if [[ -n "$legacy_key" && -f "$legacy_key" ]]; then
  LEGACY_SSH_ARGS+=(-o IdentitiesOnly=yes -i "$legacy_key")
fi

case "$migration_phase" in
  all|prepare|activate) ;;
  *)
    echo "REUP_MIGRATION_PHASE must be all, prepare, or activate." >&2
    exit 1
    ;;
esac

retry() {
  local attempt
  for attempt in $(seq 1 6); do
    if "$@"; then
      return 0
    fi
    if [[ "$attempt" -eq 6 ]]; then
      return 1
    fi
    sleep $((attempt * 2))
  done
}

retry_capture() {
  local destination=$1 attempt candidate
  shift
  candidate="${destination}.candidate"
  rm -f "$candidate"
  for attempt in $(seq 1 3); do
    if "$@" > "$candidate"; then
      mv -f "$candidate" "$destination"
      return 0
    fi
    rm -f "$candidate"
    if [[ "$attempt" -eq 3 ]]; then
      return 1
    fi
    sleep $((attempt * 5))
  done
}

cleanup() {
  chmod -R u+rwX "$temporary_root" >/dev/null 2>&1 || true
  rm -rf "$temporary_root"
}
trap cleanup EXIT

report_failure() {
  local exit_code=$?
  printf '::error title=Production migration failed::Stage: %s (exit %s)\n' \
    "$current_stage" "$exit_code" >&2
  exit "$exit_code"
}
trap report_failure ERR

required_commands=(curl dig scp ssh)
if [[ "$migration_phase" != activate ]]; then
  required_commands+=(git go npm)
  if [[ "$skip_frontend" != true ]]; then
    required_commands+=(rsync)
  fi
fi
for command_name in "${required_commands[@]}"; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Missing required command: $command_name" >&2
    exit 1
  }
done
if [[ "$migration_phase" != activate && "$skip_frontend" != true && ! -d "$frontend_root" ]]; then
  echo "Frontend repository not found: $frontend_root" >&2
  exit 1
fi

if [[ "$migration_phase" != activate ]]; then
  current_stage="release checks"
  echo "Running local release checks before touching production..."
  (
    cd "$repo_root"
    GOCACHE="${temporary_base%/}/reup-germany-cutover-go-cache" go test ./...
    cd agent-runtime
    npm ci
    npm run typecheck
    npm test
    npm run build
  )
  if [[ "$skip_frontend" != true ]]; then
    (
      cd "$frontend_root"
      npm ci
      npm run typecheck
      npm run lint
      NEXT_PUBLIC_API_BASE_URL=https://api.reupgoals.pro npm run build
    )
  fi

  current_stage="read Russian production configuration"
  echo "Reading production configuration without printing secrets..."
  if ! retry_capture "$legacy_env" ssh "${LEGACY_SSH_ARGS[@]}" "$legacy_target" \
    'sudo sh -c '\''for file in /opt/reup-goals-backend/.env /etc/reup-goals/backend.env; do [ -s "$file" ] && cat "$file"; done'\'''; then
    echo "Could not connect to the current Russian production API host." >&2
    exit 1
  fi
  if ! retry_capture "$german_ai_env" ssh "${SSH_ARGS[@]}" "$production_target" \
    'for file in /etc/reup-goals/ai-production.env /etc/reup-goals-production/agent.env; do [ -s "$file" ] && cat "$file"; done'; then
    echo "Could not connect to the German production host." >&2
    exit 1
  fi
  chmod 600 "$legacy_env" "$german_ai_env"
  if ! grep -q '^DB_HOST=' "$legacy_env"; then
    echo "The active Russian production configuration does not contain DB_HOST." >&2
    exit 1
  fi
  if ! grep -q '^OPENAI_API_KEY=' "$german_ai_env"; then
    echo "The active German AI configuration does not contain OPENAI_API_KEY." >&2
    exit 1
  fi

  current_stage="upload protected configuration to Germany"
  echo "Uploading protected configuration inputs to the German host..."
  retry scp "${SSH_ARGS[@]}" "$legacy_env" "$german_ai_env" "${production_target}:/tmp/"

  current_stage="prepare German production services"
  echo "Preparing an isolated German production cell..."
  ssh "${SSH_ARGS[@]}" "$production_target" bash -s -- "$letsencrypt_email" <<'REMOTE'
set -Eeuo pipefail

letsencrypt_email=$1
legacy_env=/tmp/backend.env
legacy_ai_env=/tmp/ai-production.env
config_root=/etc/reup-goals-production
backend_env=${config_root}/backend.env
agent_env=${config_root}/agent.env

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
delete_env() {
  sed -i -E "/^$2=/d" "$1"
}

install -d -m 700 "$config_root"
install -m 600 "$legacy_env" "$backend_env"
install -m 600 "$legacy_ai_env" "$agent_env"
openai_key=$(read_env "$legacy_ai_env" OPENAI_API_KEY)
openai_model=$(read_env "$legacy_env" OPENAI_ADVISOR_MODEL)
if [ -z "$openai_model" ]; then
  openai_model=$(read_env "$legacy_env" OPENAI_MODEL)
fi
openai_model=${openai_model:-gpt-5.6-luna}
runtime_secret=$(read_env "$legacy_ai_env" AGENT_RUNTIME_SECRET)
if [ -z "$runtime_secret" ] || [ "${#runtime_secret}" -lt 32 ]; then
  runtime_secret=$(openssl rand -hex 32)
fi
if [ -z "$openai_key" ]; then
  echo "OPENAI_API_KEY is missing on the German host." >&2
  exit 1
fi

set_env "$backend_env" APP_ENV production
set_env "$backend_env" HTTP_PORT 8082
db_sslmode=$(read_env "$backend_env" DB_SSLMODE)
case "$db_sslmode" in
  require|verify-ca|verify-full) ;;
  *) db_sslmode=require ;;
esac
set_env "$backend_env" DB_SSLMODE "$db_sslmode"
set_env "$backend_env" DB_APPLICATION_NAME reup-goals-production
set_env "$backend_env" DB_MAX_OPEN_CONNS 6
set_env "$backend_env" DB_MAX_IDLE_CONNS 2
set_env "$backend_env" DB_CONN_MAX_IDLE_TIME 1m
set_env "$backend_env" JOB_QUEUE_NAMESPACE production
set_env "$backend_env" CORS_ALLOWED_ORIGINS https://reupgoals.pro,https://www.reupgoals.pro
set_env "$backend_env" FRONTEND_BASE_URL https://reupgoals.pro
set_env "$backend_env" OPENAI_BASE_URL https://api.openai.com/v1
set_env "$backend_env" OPENAI_API_KEY "$openai_key"
delete_env "$backend_env" OPENAI_PROXY_URL
delete_env "$backend_env" OPENAI_GATEWAY_SECRET
set_env "$backend_env" AGENT_RUNTIME_ENABLED true
set_env "$backend_env" AGENT_RUNTIME_URL http://127.0.0.1:8092
set_env "$backend_env" AGENT_RUNTIME_SECRET "$runtime_secret"
set_env "$backend_env" AGENT_RUNTIME_MAX_TURNS 120
set_env "$backend_env" AGENT_RUNTIME_TIMEOUT 45m
set_env "$backend_env" HTTP_READ_TIMEOUT 10m
set_env "$backend_env" HTTP_WRITE_TIMEOUT 0s
set_env "$backend_env" AI_JOB_WORKERS 4
set_env "$backend_env" AI_AGENT_JOB_WORKERS 4

set_env "$agent_env" PORT 8092
set_env "$agent_env" LISTEN_HOST 127.0.0.1
set_env "$agent_env" GO_INTERNAL_URL http://127.0.0.1:8082
set_env "$agent_env" AGENT_RUNTIME_SECRET "$runtime_secret"
set_env "$agent_env" OPENAI_API_KEY "$openai_key"
delete_env "$agent_env" AGENT_MODEL
set_env "$agent_env" AGENT_COMPACT_THRESHOLD 100000
set_env "$agent_env" AGENT_REASONING_EFFORT max
set_env "$agent_env" AGENT_PROVIDER_TIMEOUT 45m
delete_env "$agent_env" AI_GATEWAY_SECRET
delete_env "$agent_env" AI_GATEWAY_MAX_REQUEST_BYTES
delete_env "$agent_env" OPENAI_UPSTREAM_URL
delete_env "$agent_env" OPENAI_GATEWAY_TIMEOUT
delete_env "$agent_env" OPENAI_PROXY_URL

chmod 600 "$backend_env" "$agent_env"
rm -f "$legacy_env" "$legacy_ai_env"

openai_probe=$(mktemp)
openai_status=$(curl --silent --show-error --max-time 45 \
  --output "$openai_probe" --write-out '%{http_code}' \
  --header "Authorization: Bearer ${openai_key}" \
  "https://api.openai.com/v1/models/${openai_model}" || true)
if [ "$openai_status" != 200 ]; then
  echo "The German host cannot access the configured OpenAI model (HTTP ${openai_status:-network_error})." >&2
  sed -E 's/(sk-[A-Za-z0-9_-]{8})[A-Za-z0-9_-]+/\1.../g' "$openai_probe" >&2 || true
  rm -f "$openai_probe"
  exit 1
fi
rm -f "$openai_probe"

command -v nginx >/dev/null 2>&1 || {
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y nginx certbot python3-certbot-nginx fonts-dejavu-core
}
command -v certbot >/dev/null 2>&1 || {
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y certbot python3-certbot-nginx
}

install -d -m 755 /var/www/reupgoals.pro
cat > /etc/nginx/sites-available/reupgoals-production <<'NGINX'
server {
    listen 80;
    listen [::]:80;
    server_name reupgoals.pro www.reupgoals.pro;
    server_tokens off;
    root /var/www/reupgoals.pro;
    index index.html;
    location / { try_files $uri $uri/ =404; }
}
NGINX
cat > /etc/nginx/sites-available/reupgoals-api-production <<'NGINX'
server {
    listen 80;
    listen [::]:80;
    server_name api.reupgoals.pro;
    server_tokens off;
    client_max_body_size 90m;
    client_body_timeout 3600s;
    client_header_timeout 30s;
    keepalive_timeout 30s;
    location / {
        proxy_pass http://127.0.0.1:8082;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_connect_timeout 15s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_hide_header Server;
    }
}
NGINX
ln -sfn /etc/nginx/sites-available/reupgoals-production /etc/nginx/sites-enabled/reupgoals-production
ln -sfn /etc/nginx/sites-available/reupgoals-api-production /etc/nginx/sites-enabled/reupgoals-api-production
nginx -t
systemctl reload nginx

# The old public gateway occupied production port 8092. It is replaced by the
# isolated colocated service installed by promote-production-backend.sh.
systemctl disable --now reup-goals-ai.service >/dev/null 2>&1 || true
printf '%s' "$letsencrypt_email" > "$config_root/letsencrypt-email"
chmod 600 "$config_root/letsencrypt-email"
REMOTE

  current_stage="deploy German backend candidate"
  echo "Deploying the candidate API and agent locally on the German host..."
  REUP_PRODUCTION_HOST="$production_host" \
  REUP_PRODUCTION_USER="$production_user" \
  REUP_PRODUCTION_SSH_TARGET="$production_target" \
  REUP_PRODUCTION_KEY="$production_key" \
    "$repo_root/scripts/promote-production-backend.sh"

  if [[ "$skip_frontend" != true ]]; then
    current_stage="deploy German frontend candidate"
    echo "Building and uploading the production frontend..."
    (
      cd "$frontend_root"
      NEXT_PUBLIC_API_BASE_URL=https://api.reupgoals.pro \
        DEPLOY_HOST="$production_host" \
        DEPLOY_USER="$production_user" \
        DEPLOY_KEY="$production_key" \
        DEPLOY_PATH=/var/www/reupgoals.pro \
        bash scripts/deploy.sh
    )
  else
    echo "Frontend deployment is delegated to its production GitHub workflow."
  fi

  echo
  echo "German production candidate is ready. Update these DNS records now:"
  echo "  reupgoals.pro      A  $production_host"
  echo "  www.reupgoals.pro  A  $production_host"
  echo "  api.reupgoals.pro  A  $production_host"
  echo "Do not change staging.reupgoals.pro or api-staging.reupgoals.pro."

  if [[ "$migration_phase" == prepare ]]; then
    echo
    echo "Preparation phase completed. Deploy the production frontend, update DNS, then run the activation phase."
    exit 0
  fi
fi
echo
current_stage="wait for production DNS"
echo "Waiting for production DNS to point to Germany..."
deadline=$((SECONDS + ${DNS_WAIT_SECONDS:-25200}))
while true; do
  apex="$(dig +short A reupgoals.pro | sort -u | tr '\n' ' ')"
  www="$(dig +short A www.reupgoals.pro | sort -u | tr '\n' ' ')"
  api="$(dig +short A api.reupgoals.pro | sort -u | tr '\n' ' ')"
  if [[ " $apex " == *" $production_host "* ]] && \
     [[ " $www " == *" $production_host "* ]] && \
     [[ " $api " == *" $production_host "* ]]; then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "DNS is not ready. Point reupgoals.pro, www.reupgoals.pro, and api.reupgoals.pro to $production_host and run this command again." >&2
    exit 1
  fi
  sleep 2
done

current_stage="activate HTTPS in Germany"
echo "Activating HTTPS in Germany..."
ssh "${SSH_ARGS[@]}" "$production_target" bash -s -- "$letsencrypt_email" <<'REMOTE'
set -Eeuo pipefail

email=$1

test -s /var/www/reupgoals.pro/index.html || {
  echo "Production frontend is missing: /var/www/reupgoals.pro/index.html" >&2
  exit 1
}
test -s /var/www/reupgoals.pro/login/index.html || {
  echo "Production login page is missing: /var/www/reupgoals.pro/login/index.html" >&2
  exit 1
}

for attempt in $(seq 1 60); do
  api=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8082/readyz || true)
  agent=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8092/readyz || true)
  [ "$api" = 200 ] && [ "$agent" = 200 ] && break
  [ "$attempt" -lt 60 ] || exit 1
  sleep 2
done

certbot --nginx --non-interactive --agree-tos --redirect --email "$email" \
  -d reupgoals.pro -d www.reupgoals.pro
certbot --nginx --non-interactive --agree-tos --redirect --email "$email" \
  -d api.reupgoals.pro
nginx -t
systemctl reload nginx
REMOTE

current_stage="production smoke checks"
echo "Running production smoke checks..."
curl --fail --show-error --silent --max-time 30 https://api.reupgoals.pro/readyz >/dev/null
curl --fail --show-error --silent --max-time 30 https://api.reupgoals.pro/api/v2/privacy/legal-documents >/dev/null
curl --fail --show-error --silent --max-time 30 https://reupgoals.pro/login/ >/dev/null
curl --fail --show-error --silent --max-time 30 https://reupgoals.pro/cabinet-v2/ >/dev/null

current_stage="configure Russian DNS drain bridge"
echo "Draining cached API traffic through a temporary compatibility bridge..."
if ssh "${LEGACY_SSH_ARGS[@]}" "$legacy_target" sudo bash -s -- "$production_host" <<'REMOTE'
set -Eeuo pipefail

production_host=$1
bridge_available=/etc/nginx/sites-available/reupgoals-api-germany-drain
bridge_enabled=/etc/nginx/sites-enabled/reupgoals-api-germany-drain
backup_root="/var/backups/reup-goals/dns-drain-$(date +%Y%m%d-%H%M%S)"
mapfile -t previous_sites < <(grep -lE 'server_name[[:space:]]+api\.reupgoals\.pro([[:space:]]|;)' /etc/nginx/sites-enabled/* 2>/dev/null || true)

test -s /etc/letsencrypt/live/api.reupgoals.pro/fullchain.pem
test -s /etc/letsencrypt/live/api.reupgoals.pro/privkey.pem
mkdir -p "$backup_root"
for site in "${previous_sites[@]}"; do
  [ "$site" = "$bridge_enabled" ] && continue
  mv "$site" "$backup_root/"
done

restore_nginx() {
  local exit_code=$?
  trap - ERR
  rm -f "$bridge_enabled" "$bridge_available"
  for site in "$backup_root"/*; do
    [ -e "$site" ] || [ -L "$site" ] || continue
    mv "$site" /etc/nginx/sites-enabled/
  done
  nginx -t >/dev/null 2>&1 && systemctl reload nginx || true
  exit "$exit_code"
}
trap restore_nginx ERR

cat > "$bridge_available" <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name api.reupgoals.pro;
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name api.reupgoals.pro;
    ssl_certificate /etc/letsencrypt/live/api.reupgoals.pro/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.reupgoals.pro/privkey.pem;
    client_max_body_size 90m;
    client_body_timeout 3600s;

    location / {
        proxy_pass https://${production_host};
        proxy_http_version 1.1;
        proxy_set_header Host api.reupgoals.pro;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_ssl_server_name on;
        proxy_ssl_name api.reupgoals.pro;
        proxy_ssl_verify on;
        proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
        proxy_connect_timeout 15s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;
        proxy_request_buffering off;
        proxy_buffering off;
    }
}
NGINX

ln -sfn "$bridge_available" "$bridge_enabled"
nginx -t
systemctl reload nginx
curl --fail --silent --show-error --max-time 30 \
  --resolve api.reupgoals.pro:443:127.0.0.1 \
  https://api.reupgoals.pro/readyz >/dev/null

# No application process remains on the Russian server. The nginx bridge only
# serves clients whose DNS cache still contains the old address.
systemctl disable --now reup-goals.service reup-goals-ai-return-tunnel.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/reup-goals-ai-return-tunnel.service
rm -rf /etc/reup-goals/ssh/ai-return-tunnel /etc/reup-goals/ssh/ai-return-tunnel.pub
systemctl daemon-reload

trap - ERR
systemd-run --quiet --unit=reup-goals-remove-api-drain --on-active=24h \
  /bin/bash -c "rm -f '$bridge_enabled' '$bridge_available'; nginx -t && systemctl reload nginx" || true
echo "Russian application stopped; 24-hour DNS drain bridge is active."
REMOTE
then
  echo "Legacy traffic is safely draining to Germany."
else
  echo "WARNING: the Russian host could not install the DNS drain bridge." >&2
  echo "German production is healthy, but the old Russian services were left untouched for cached clients." >&2
fi

current_stage="remove obsolete German AI gateway"
echo "Removing the obsolete public AI gateway..."
retry ssh "${SSH_ARGS[@]}" "$production_target" \
  'rm -f /etc/nginx/sites-enabled/ai.reupgoals.pro /etc/nginx/sites-enabled/reup-goals-ai; nginx -t; systemctl reload nginx'

echo
echo "Production now runs in Germany; PostgreSQL remains in Russia over TLS."
echo "Staging remains isolated on the German host and uses its existing domains."
