#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
production_host="${REUP_PRODUCTION_HOST:-167.233.230.212}"
production_user="${REUP_PRODUCTION_USER:-root}"
production_target="${REUP_PRODUCTION_SSH_TARGET:-${production_user}@${production_host}}"
production_key="${REUP_PRODUCTION_KEY:-$HOME/.ssh/reup_goals_staging_deploy}"
install_only="${REUP_PRODUCTION_INSTALL_ONLY:-false}"
release_id="production-$(git -C "$repo_root" rev-parse --short=12 HEAD)"
backend_artifact="/tmp/reup_goals_backend"
runtime_artifact="/tmp/reup_goals_agent_runtime.tar.gz"
runtime_stage="$(mktemp -d /tmp/reup-goals-agent-runtime.XXXXXX)"
node_version="${AGENT_NODE_VERSION:-v22.17.0}"
current_stage="initialize production backend promotion"

SSH_ARGS=(
  -o BatchMode=yes
  -o ConnectTimeout=15
  -o ServerAliveInterval=10
  -o ServerAliveCountMax=12
  -o TCPKeepAlive=yes
  -o IdentitiesOnly=yes
)
if [[ -f "$production_key" ]]; then
  SSH_ARGS+=(-i "$production_key")
fi

cleanup() {
  local exit_code=$?
  if [[ "$exit_code" -ne 0 ]]; then
    printf '::error title=German production candidate failed::Stage: %s (exit %s)\n' \
      "$current_stage" "$exit_code" >&2
  fi
  rm -rf "$runtime_stage"
  return "$exit_code"
}
trap cleanup EXIT

cd "$repo_root"
current_stage="build German production API"
echo "Building production API..."
GOCACHE="${TMPDIR:-/tmp}/reup-release-cache" \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$backend_artifact" ./cmd/api

current_stage="build German production agent runtime"
echo "Building colocated production agent runtime..."
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
  rm -rf node_modules/fsevents
)

if [[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]]; then
  cp "$(command -v node)" "$runtime_stage/node"
else
  archive="node-${node_version}-linux-x64.tar.xz"
  curl --fail --show-error --silent --location \
    "https://nodejs.org/dist/${node_version}/${archive}" -o "$runtime_stage/$archive"
  curl --fail --show-error --silent --location \
    "https://nodejs.org/dist/${node_version}/SHASUMS256.txt" -o "$runtime_stage/SHASUMS256.txt"
  (
    cd "$runtime_stage"
    grep " ${archive}\$" SHASUMS256.txt > SHASUMS256.selected
    shasum -a 256 -c SHASUMS256.selected
    tar -xJf "$archive"
  )
  cp "$runtime_stage/node-${node_version}-linux-x64/bin/node" "$runtime_stage/node"
  rm -rf "$runtime_stage/node-${node_version}-linux-x64" "$runtime_stage/$archive" "$runtime_stage/SHASUMS256"*
fi
chmod 755 "$runtime_stage/node"
tar -czf "$runtime_artifact" -C "$runtime_stage" .

current_stage="upload German production artifacts"
echo "Uploading production API and agent runtime to Germany..."
scp "${SSH_ARGS[@]}" "$backend_artifact" "$runtime_artifact" "${production_target}:/tmp/"

current_stage="install German production services"
echo "Installing the production services with automatic rollback..."
ssh "${SSH_ARGS[@]}" "$production_target" bash -s -- "$release_id" "$install_only" <<'REMOTE'
set -Eeuo pipefail

release_id=$1
install_only=$2
remote_stage="validate production service configuration"
api_service=reup-goals-production.service
agent_service=reup-goals-agent-production.service
api_root=/opt/reup-goals-production/backend
agent_root=/opt/reup-goals-production/agent
api_next=${api_root}.next
agent_next=${agent_root}.next
api_previous=${api_root}.previous
agent_previous=${agent_root}.previous
api_env=/etc/reup-goals-production/backend.env
agent_env=/etc/reup-goals-production/agent.env

report_failure() {
  local exit_code=$?
  if [ "$exit_code" -ne 0 ]; then
    printf '::error title=German production service failed::Stage: %s (exit %s)\n' \
      "$remote_stage" "$exit_code" >&2
  fi
}
trap report_failure EXIT

read_env() {
  grep -E "^$2=" "$1" | tail -1 | cut -d= -f2- || true
}

for env_file in "$api_env" "$agent_env"; do
  if [ ! -s "$env_file" ]; then
    echo "$env_file is missing. Run scripts/migrate-production-to-germany.sh first." >&2
    exit 1
  fi
done

api_port=$(read_env "$api_env" HTTP_PORT)
runtime_url=$(read_env "$api_env" AGENT_RUNTIME_URL)
runtime_secret=$(read_env "$api_env" AGENT_RUNTIME_SECRET)
api_openai_key=$(read_env "$api_env" OPENAI_API_KEY)
db_sslmode=$(read_env "$api_env" DB_SSLMODE)
db_host=$(read_env "$api_env" DB_HOST)
agent_port=$(read_env "$agent_env" PORT)
go_internal_url=$(read_env "$agent_env" GO_INTERNAL_URL)
agent_secret=$(read_env "$agent_env" AGENT_RUNTIME_SECRET)
agent_openai_key=$(read_env "$agent_env" OPENAI_API_KEY)

if [ "$api_port" != 8082 ] || [ "$agent_port" != 8092 ]; then
  echo "Production ports must be HTTP_PORT=8082 and PORT=8092." >&2
  exit 1
fi
if [ "$runtime_url" != http://127.0.0.1:8092 ] || [ "$go_internal_url" != http://127.0.0.1:8082 ]; then
  echo "Production API and agent must communicate over German loopback." >&2
  exit 1
fi
if [ "${#runtime_secret}" -lt 32 ] || [ "$runtime_secret" != "$agent_secret" ]; then
  echo "Production agent transport secret is missing or inconsistent." >&2
  exit 1
fi
if [ -z "$api_openai_key" ] || [ -z "$agent_openai_key" ]; then
  echo "The German production services require a direct OpenAI key." >&2
  exit 1
fi
case "$db_host" in
  127.0.0.1|localhost|::1)
    if [ "$db_sslmode" != disable ]; then
      echo "Local PostgreSQL must use DB_SSLMODE=disable." >&2
      exit 1
    fi
    ;;
  *)
    case "$db_sslmode" in require|verify-ca|verify-full) ;; *)
      echo "Remote PostgreSQL must use TLS (DB_SSLMODE=require, verify-ca, or verify-full)." >&2
      exit 1
    esac
    ;;
esac

rollback() {
  local exit_code=$?
  trap - ERR
  echo "Production service update failed; restoring the previous German release." >&2
  systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
  rm -rf "$api_root" "$agent_root"
  [ -d "$api_previous" ] && mv "$api_previous" "$api_root"
  [ -d "$agent_previous" ] && mv "$agent_previous" "$agent_root"
  systemctl daemon-reload
  [ -d "$api_root" ] && systemctl start "$api_service" >/dev/null 2>&1 || true
  [ -d "$agent_root" ] && systemctl start "$agent_service" >/dev/null 2>&1 || true
  journalctl -u "$api_service" -u "$agent_service" -n 160 --no-pager || true
  exit "$exit_code"
}
trap rollback ERR

remote_stage="create German production service account"
echo "German candidate: creating the service account..."
id -u reupgoals >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin reupgoals
getent group reupgoals >/dev/null 2>&1 || groupadd --system reupgoals
usermod -g reupgoals reupgoals
remote_stage="install production service artifacts"
echo "German candidate: installing release artifacts..."
install -d -m 750 -o reupgoals -g reupgoals /opt/reup-goals-production
install -d -m 700 /etc/reup-goals-production

remote_stage="prepare German production release directories"
rm -rf "$api_next" "$agent_next"
install -d -m 750 -o reupgoals -g reupgoals "$api_next" "$agent_next"
remote_stage="install German production API artifact"
install -m 755 -o reupgoals -g reupgoals /tmp/reup_goals_backend "$api_next/reup_goals_backend"
remote_stage="extract German production agent artifact"
tar -xzf /tmp/reup_goals_agent_runtime.tar.gz -C "$agent_next"
chown -R reupgoals:reupgoals "$agent_next"
remote_stage="validate German production artifacts"
test -x "$agent_next/node"
test -f "$agent_next/dist/server.js"

remote_stage="write German production API service"
echo "German candidate: registering service units..."
cat > "/etc/systemd/system/${api_service}" <<'UNIT'
[Unit]
Description=REUP.goals Production API (Germany)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=reupgoals
Group=reupgoals
WorkingDirectory=/opt/reup-goals-production/backend
EnvironmentFile=/etc/reup-goals-production/backend.env
ExecStart=/opt/reup-goals-production/backend/reup_goals_backend
Restart=always
RestartSec=3
TimeoutStopSec=30
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6

[Install]
WantedBy=multi-user.target
UNIT

remote_stage="write German production agent service"
cat > "/etc/systemd/system/${agent_service}" <<'UNIT'
[Unit]
Description=REUP.goals Production Agent Runtime (Germany)
After=network-online.target reup-goals-production.service
Wants=network-online.target

[Service]
Type=simple
User=reupgoals
Group=reupgoals
WorkingDirectory=/opt/reup-goals-production/agent
Environment=NODE_ENV=production
EnvironmentFile=/etc/reup-goals-production/agent.env
ExecStart=/opt/reup-goals-production/agent/node dist/server.js
Restart=always
RestartSec=3
TimeoutStopSec=30
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectHome=true
ProtectSystem=strict
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT

remote_stage="stop previous German production services"
systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
remote_stage="switch German production release directories"
rm -rf "$api_previous" "$agent_previous"
[ -d "$api_root" ] && mv "$api_root" "$api_previous"
[ -d "$agent_root" ] && mv "$agent_root" "$agent_previous"
mv "$api_next" "$api_root"
mv "$agent_next" "$agent_root"

remote_stage="register German production services"
systemctl daemon-reload
if [ "$install_only" = true ]; then
  systemctl disable --now "$api_service" "$agent_service" >/dev/null 2>&1 || true
  systemctl reset-failed "$api_service" "$agent_service" >/dev/null 2>&1 || true
  trap - ERR
  rm -rf "$api_previous" "$agent_previous"
  echo "PRODUCTION API AND AGENT INSTALLED INACTIVE IN GERMANY (${release_id})"
  exit 0
fi
systemctl enable "$api_service" "$agent_service" >/dev/null
systemctl reset-failed "$api_service" "$agent_service" || true
remote_stage="start German production API"
echo "German candidate: starting the API..."
systemctl start "$api_service"
remote_stage="start German production agent runtime"
echo "German candidate: starting the agent runtime..."
systemctl start "$agent_service"

remote_stage="wait for German production readiness"
echo "German candidate: waiting for readiness..."
for attempt in $(seq 1 60); do
  api_health=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8082/readyz || true)
  agent_health=$(curl -sS http://127.0.0.1:8092/healthz || true)
  agent_ready=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8092/readyz || true)
  if [ "$api_health" = 200 ] && [[ "$agent_health" == *'"runtime":true'* ]] && [[ "$agent_health" == *'"openai":true'* ]] && [ "$agent_ready" = 200 ]; then
    break
  fi
  if [ "$attempt" -eq 60 ]; then
    api_service_state=$(systemctl is-active "$api_service" 2>/dev/null || true)
    agent_service_state=$(systemctl is-active "$agent_service" 2>/dev/null || true)
    api_service_details=$(systemctl show "$api_service" \
      --property=SubState,Result,ExecMainCode,ExecMainStatus,NRestarts \
      --no-pager 2>/dev/null | tr '\n' ' ' || true)
    api_startup_log=$(journalctl -u "$api_service" -n 8 --no-pager -o cat 2>/dev/null | tr '\n' ' ' || true)
    api_startup_log=$(printf '%s' "$api_startup_log" | sed -E \
      -e 's/(password|secret|token|api[_-]?key)=[^ ]+/\1=[redacted]/Ig' \
      -e 's/sk-[A-Za-z0-9_-]+/sk-[redacted]/g' | cut -c1-1600)
    api_health_code=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8082/healthz || true)
    api_ready_body=$(curl -sS http://127.0.0.1:8082/readyz || true)
    agent_ready_body=$(curl -sS http://127.0.0.1:8092/readyz || true)
    api_ready_body=${api_ready_body//$'\n'/ }
    agent_health=${agent_health//$'\n'/ }
    agent_ready_body=${agent_ready_body//$'\n'/ }
    printf '::error title=German candidate readiness diagnostics::API service=%s (%s); agent service=%s; API health HTTP=%s; API ready=%s; agent health=%s; agent ready HTTP=%s; agent ready=%s; API startup=%s\n' \
      "${api_service_state:-unknown}" "${api_service_details:-unknown}" \
      "${agent_service_state:-unknown}" \
      "${api_health_code:-network_error}" "${api_ready_body:-unavailable}" \
      "${agent_health:-unavailable}" "${agent_ready:-network_error}" \
      "${agent_ready_body:-unavailable}" "${api_startup_log:-unavailable}" >&2
    echo "The German production cell did not become ready." >&2
    exit 1
  fi
  if [ $((attempt % 5)) -eq 0 ]; then
    echo "German candidate: readiness check ${attempt}/60..."
  fi
  sleep 2
done

trap - ERR
rm -rf "$api_previous" "$agent_previous"
systemctl status "$api_service" "$agent_service" --no-pager
echo "PRODUCTION API AND AGENT DEPLOYED IN GERMANY (${release_id})"
REMOTE

echo "Production API and agent promotion completed."
