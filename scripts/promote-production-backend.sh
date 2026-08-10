#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backend_artifact="/tmp/reup_goals_backend"
runtime_artifact="/tmp/reup_goals_agent_runtime.tar.gz"
runtime_stage="$(mktemp -d /tmp/reup-goals-agent-runtime.XXXXXX)"
node_version="${AGENT_NODE_VERSION:-v22.17.0}"
release_id="production-$(git -C "$repo_root" rev-parse --short=12 HEAD)"

cleanup() {
  rm -rf "$runtime_stage"
}
trap cleanup EXIT

cd "$repo_root"

echo "Building production backend..."
GOCACHE=/private/tmp/reup-release-cache \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$backend_artifact" ./cmd/api

echo "Building production agent runtime..."
(
  cd "$repo_root/agent-runtime"
  npm ci
  npm run typecheck
  npm test
  npm run build
  cp -R dist package.json package-lock.json "$runtime_stage/"
)
(
  cd "$runtime_stage"
  npm ci --omit=dev --ignore-scripts
  # npm can install the optional macOS watcher while assembling the archive on
  # a Mac. The production archive is Linux-only and must not contain it.
  rm -rf node_modules/fsevents
)

if [[ "$(uname -s)" == "Linux" && "$(uname -m)" == "x86_64" ]]; then
  cp "$(command -v node)" "$runtime_stage/node"
else
  node_archive="node-${node_version}-linux-x64.tar.xz"
  node_download_dir="$runtime_stage/node-download"
  mkdir -p "$node_download_dir"
  curl --fail --show-error --silent --location \
    "https://nodejs.org/dist/${node_version}/${node_archive}" \
    -o "$node_download_dir/$node_archive"
  curl --fail --show-error --silent --location \
    "https://nodejs.org/dist/${node_version}/SHASUMS256.txt" \
    -o "$node_download_dir/SHASUMS256.txt"
  (
    cd "$node_download_dir"
    grep " ${node_archive}\$" SHASUMS256.txt > SHASUMS256.selected
    test -s SHASUMS256.selected
    shasum -a 256 -c SHASUMS256.selected
    tar -xJf "$node_archive"
  )
  cp "$node_download_dir/node-${node_version}-linux-x64/bin/node" "$runtime_stage/node"
  rm -rf "$node_download_dir"
fi
chmod 755 "$runtime_stage/node"
tar -czf "$runtime_artifact" -C "$runtime_stage" .

echo "Uploading backend and agent runtime..."
scp "$backend_artifact" "$runtime_artifact" reup:/tmp/

echo "Installing backend and agent runtime with automatic rollback..."
ssh reup bash -s -- "$release_id" <<'REMOTE'
set -Eeuo pipefail

release_id=$1
timestamp=$(date +%Y%m%d-%H%M%S)
backend_service=reup-goals.service
agent_service=reup-goals-agent-production.service
backend_binary=/opt/reup-goals-backend/reup_goals_backend
backend_rollback="${backend_binary}.rollback-${timestamp}"
agent_root=/opt/reup-goals-agent-production
agent_next="${agent_root}.next"
agent_previous="${agent_root}.previous"
agent_env=/etc/reup-goals/agent-production.env
agent_env_backup="${agent_env}.rollback-${timestamp}"
agent_unit=/etc/systemd/system/reup-goals-agent-production.service
agent_unit_backup="${agent_unit}.rollback-${timestamp}"
dropin_dir=/etc/systemd/system/reup-goals.service.d
agent_backend_config=${dropin_dir}/agent-runtime.conf
agent_backend_config_backup="${agent_backend_config}.rollback-${timestamp}"
web_config=${dropin_dir}/web-security.conf
web_config_backup="${web_config}.rollback-${timestamp}"
backend_changed=false
agent_changed=false

read_service_env() {
  local key=$1
  local main_pid
  main_pid=$(systemctl show "$backend_service" -p MainPID --value)
  if [ -z "$main_pid" ] || [ "$main_pid" = 0 ] || [ ! -r "/proc/${main_pid}/environ" ]; then
    return 0
  fi
  tr '\0' '\n' < "/proc/${main_pid}/environ" \
    | grep -E "^${key}=" \
    | tail -1 \
    | cut -d= -f2- || true
}

read_config_value() {
  local key=$1
  local value
  local candidate
  value=$(read_service_env "$key")
  if [ -n "$value" ]; then
    printf '%s' "$value"
    return 0
  fi
  for candidate in /etc/reup-goals/backend.env /opt/reup-goals-backend/.env; do
    if [ -f "$candidate" ]; then
      value=$(grep -E "^${key}=" "$candidate" | tail -1 | cut -d= -f2- || true)
      if [ -n "$value" ]; then
        printf '%s' "$value"
        return 0
      fi
    fi
  done
}

restore_file_or_remove() {
  local backup=$1
  local target=$2
  if [ -f "$backup" ]; then
    mv -f "$backup" "$target"
  else
    rm -f "$target"
  fi
}

rollback() {
  local exit_code=$?
  trap - ERR
  echo "Production deployment failed; restoring the previous backend and agent runtime." >&2

  systemctl stop "$agent_service" >/dev/null 2>&1 || true
  if [ "$agent_changed" = true ]; then
    rm -rf "$agent_root"
    if [ -d "$agent_previous" ]; then
      mv "$agent_previous" "$agent_root"
    fi
    restore_file_or_remove "$agent_env_backup" "$agent_env"
    restore_file_or_remove "$agent_unit_backup" "$agent_unit"
  fi

  if [ "$backend_changed" = true ]; then
    systemctl stop "$backend_service" >/dev/null 2>&1 || true
    if [ -f "$backend_rollback" ]; then
      install -m 755 "$backend_rollback" "$backend_binary"
    fi
    restore_file_or_remove "$agent_backend_config_backup" "$agent_backend_config"
    restore_file_or_remove "$web_config_backup" "$web_config"
  fi

  systemctl daemon-reload
  systemctl reset-failed "$backend_service" "$agent_service" >/dev/null 2>&1 || true
  systemctl start "$backend_service" >/dev/null 2>&1 || true
  if [ -d "$agent_root" ] && [ -f "$agent_unit" ]; then
    systemctl start "$agent_service" >/dev/null 2>&1 || true
  fi
  journalctl -u "$backend_service" -u "$agent_service" -n 120 --no-pager || true
  exit "$exit_code"
}
trap rollback ERR

service_user=$(systemctl show "$backend_service" -p User --value)
service_group=$(systemctl show "$backend_service" -p Group --value)
service_user=${service_user:-root}
service_group=${service_group:-$service_user}

openai_api_key=$(read_config_value OPENAI_API_KEY)
if [ -z "$openai_api_key" ]; then
  echo "OPENAI_API_KEY is missing from the running production backend." >&2
  exit 1
fi
runtime_secret=$(read_config_value AGENT_RUNTIME_SECRET)
if [ "${#runtime_secret}" -lt 32 ]; then
  runtime_secret=$(openssl rand -hex 32)
fi

# Production can still run on the legacy host where direct OpenAI egress is
# unavailable. Select the route by making an authenticated provider request;
# a process-only health check cannot detect this failure mode.
openai_status() {
  local route=$1
  local -a proxy_args=()
  if [ "$route" != direct ]; then
    proxy_args=(--socks5-hostname 127.0.0.1:10808)
  fi
  curl --silent --show-error --output /dev/null \
    --connect-timeout 10 --max-time 30 \
    --retry 1 --retry-all-errors \
    "${proxy_args[@]}" \
    --header "Authorization: Bearer ${openai_api_key}" \
    --write-out '%{http_code}' \
    https://api.openai.com/v1/models/gpt-5.6-luna || true
}

direct_status=$(openai_status direct)
if [ "$direct_status" = 200 ]; then
  openai_proxy_url=direct
  echo "OpenAI production egress: direct"
else
  proxy_status=$(openai_status proxy)
  if [ "$proxy_status" != 200 ]; then
    echo "OpenAI is unavailable from production (direct HTTP ${direct_status:-000}, protected route HTTP ${proxy_status:-000})." >&2
    exit 1
  fi
  openai_proxy_url=socks5://127.0.0.1:10808
  echo "OpenAI production egress: protected route"
fi

mkdir -p /etc/reup-goals "$dropin_dir"
rm -rf "$agent_next"
mkdir -p "$agent_next"
tar -xzf /tmp/reup_goals_agent_runtime.tar.gz -C "$agent_next"
test -x "$agent_next/node"
test -f "$agent_next/dist/server.js"
chown -R "$service_user:$service_group" "$agent_next"

rm -rf "$agent_previous"
if [ -d "$agent_root" ]; then
  mv "$agent_root" "$agent_previous"
fi
mv "$agent_next" "$agent_root"
agent_changed=true

if [ -f "$agent_env" ]; then cp -a "$agent_env" "$agent_env_backup"; fi
if [ -f "$agent_unit" ]; then cp -a "$agent_unit" "$agent_unit_backup"; fi

{
  printf 'PORT=8091\n'
  printf 'GO_INTERNAL_URL=http://127.0.0.1:8080\n'
  printf 'AGENT_RUNTIME_SECRET=%s\n' "$runtime_secret"
  printf 'OPENAI_API_KEY=%s\n' "$openai_api_key"
  printf 'OPENAI_PROXY_URL=%s\n' "$openai_proxy_url"
  printf 'AGENT_COMPACT_THRESHOLD=100000\n'
  printf 'AGENT_REASONING_EFFORT=max\n'
} > "$agent_env"
chown "$service_user:$service_group" "$agent_env"
chmod 600 "$agent_env"

cat > "$agent_unit" <<SERVICE
[Unit]
Description=REUP Goals Agent Runtime Production
After=network-online.target ${backend_service}
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${agent_root}
Environment=NODE_ENV=production
EnvironmentFile=${agent_env}
ExecStart=${agent_root}/node dist/server.js
Restart=always
RestartSec=3
StandardOutput=journal
StandardError=journal
User=${service_user}
Group=${service_group}
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
UMask=0077
ProtectClock=true
ProtectHostname=true
RestrictNamespaces=true
LockPersonality=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6

[Install]
WantedBy=multi-user.target
SERVICE
chmod 644 "$agent_unit"

systemctl daemon-reload
systemctl enable "$agent_service"
systemctl restart "$agent_service"
for attempt in $(seq 1 45); do
  if curl --fail --silent http://127.0.0.1:8091/healthz >/dev/null; then
    break
  fi
  if [ "$attempt" -eq 45 ]; then
    echo "Production agent runtime failed its health check." >&2
    exit 1
  fi
  sleep 1
done

cp -a "$backend_binary" "$backend_rollback"
if [ -f "$agent_backend_config" ]; then cp -a "$agent_backend_config" "$agent_backend_config_backup"; fi
if [ -f "$web_config" ]; then cp -a "$web_config" "$web_config_backup"; fi
backend_changed=true

cat > "$agent_backend_config" <<EOF
[Service]
Environment="AGENT_RUNTIME_SECRET=${runtime_secret}"
Environment="AGENT_RUNTIME_URL=http://127.0.0.1:8091"
Environment="AGENT_RUNTIME_MAX_TURNS=120"
Environment="AGENT_RELEASE_ID=${release_id}"
Environment="AGENT_RUNTIME_ENABLED=true"
EOF
chmod 600 "$agent_backend_config"

cat > "$web_config" <<'EOF'
[Service]
Environment="BROWSER_AUTH_ONLY=true"
Environment="COOKIE_SECURE=true"
Environment="CORS_ALLOWED_ORIGINS=https://reupgoals.pro,https://www.reupgoals.pro"
Environment="BILLING_PAYMENTS_ENABLED=true"
Environment="HTTP_WRITE_TIMEOUT=0s"
Environment="OPENAI_MODEL=gpt-5.6-luna"
Environment="OPENAI_AUDITOR_MODEL=gpt-5.6-luna"
Environment="OPENAI_ADVISOR_MODEL=gpt-5.6-luna"
Environment="OPENAI_TASK_MODEL=gpt-5.6-luna"
Environment="OPENAI_PROXY_URL=${openai_proxy_url}"
Environment="OPENAI_AUDITOR_COMPACT_THRESHOLD=24000"
Environment="OPENAI_ADVISOR_COMPACT_THRESHOLD=24000"
EOF
chmod 600 "$web_config"

install -m 755 /tmp/reup_goals_backend "$backend_binary"
systemctl daemon-reload
systemctl reset-failed "$backend_service" || true
systemctl restart "$backend_service"

for attempt in $(seq 1 45); do
  health=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz || true)
  privacy=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v2/privacy/legal-documents || true)
  if [ "$health" = 200 ] && [ "$privacy" = 200 ]; then
    break
  fi
  if [ "$attempt" -eq 45 ]; then
    echo "Production backend failed its health check." >&2
    exit 1
  fi
  sleep 2
done

trap - ERR
rm -f "$agent_backend_config_backup" "$web_config_backup" "$agent_env_backup" "$agent_unit_backup"
rm -rf "$agent_previous"
systemctl status "$backend_service" "$agent_service" --no-pager
echo "BACKEND AND AGENT RUNTIME DEPLOYED"
REMOTE

echo "Checking public production endpoints..."
curl --fail --show-error --silent https://api.reupgoals.pro/healthz
echo
curl --fail --show-error --silent https://api.reupgoals.pro/api/v2/privacy/legal-documents
echo
echo "Production backend and agent runtime promotion completed."
