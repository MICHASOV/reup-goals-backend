#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
production_host="${REUP_PRODUCTION_HOST:-167.233.230.212}"
production_user="${REUP_PRODUCTION_USER:-root}"
production_target="${REUP_PRODUCTION_SSH_TARGET:-${production_user}@${production_host}}"
production_key="${REUP_PRODUCTION_KEY:-$HOME/.ssh/reup_goals_staging_deploy}"
release_id="production-$(git -C "$repo_root" rev-parse --short=12 HEAD)"
backend_artifact="/tmp/reup_goals_backend"
runtime_artifact="/tmp/reup_goals_agent_runtime.tar.gz"
runtime_stage="$(mktemp -d /tmp/reup-goals-agent-runtime.XXXXXX)"
node_version="${AGENT_NODE_VERSION:-v22.17.0}"

SSH_ARGS=(-o BatchMode=yes -o ConnectTimeout=15 -o IdentitiesOnly=yes)
if [[ -f "$production_key" ]]; then
  SSH_ARGS+=(-i "$production_key")
fi

cleanup() { rm -rf "$runtime_stage"; }
trap cleanup EXIT

cd "$repo_root"
echo "Building production API..."
GOCACHE=/private/tmp/reup-release-cache \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$backend_artifact" ./cmd/api

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

echo "Uploading production API and agent runtime to Germany..."
scp "${SSH_ARGS[@]}" "$backend_artifact" "$runtime_artifact" "${production_target}:/tmp/"

echo "Installing the production services with automatic rollback..."
ssh "${SSH_ARGS[@]}" "$production_target" bash -s -- "$release_id" <<'REMOTE'
set -Eeuo pipefail

release_id=$1
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
case "$db_sslmode" in require|verify-ca|verify-full) ;; *)
  echo "Remote PostgreSQL must use TLS (DB_SSLMODE=require, verify-ca, or verify-full)." >&2
  exit 1
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

id -u reupgoals >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin reupgoals
install -d -m 750 -o reupgoals -g reupgoals /opt/reup-goals-production
install -d -m 700 /etc/reup-goals-production

rm -rf "$api_next" "$agent_next"
install -d -m 750 -o reupgoals -g reupgoals "$api_next" "$agent_next"
install -m 755 -o reupgoals -g reupgoals /tmp/reup_goals_backend "$api_next/reup_goals_backend"
tar -xzf /tmp/reup_goals_agent_runtime.tar.gz -C "$agent_next"
chown -R reupgoals:reupgoals "$agent_next"
test -x "$agent_next/node"
test -f "$agent_next/dist/server.js"

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

systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
rm -rf "$api_previous" "$agent_previous"
[ -d "$api_root" ] && mv "$api_root" "$api_previous"
[ -d "$agent_root" ] && mv "$agent_root" "$agent_previous"
mv "$api_next" "$api_root"
mv "$agent_next" "$agent_root"

systemctl daemon-reload
systemctl enable "$api_service" "$agent_service" >/dev/null
systemctl reset-failed "$api_service" "$agent_service" || true
systemctl start "$api_service"
systemctl start "$agent_service"

for attempt in $(seq 1 60); do
  api_health=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8082/readyz || true)
  agent_health=$(curl -sS http://127.0.0.1:8092/healthz || true)
  agent_ready=$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8092/readyz || true)
  if [ "$api_health" = 200 ] && [[ "$agent_health" == *'"runtime":true'* ]] && [[ "$agent_health" == *'"openai":true'* ]] && [ "$agent_ready" = 200 ]; then
    break
  fi
  if [ "$attempt" -eq 60 ]; then
    echo "The German production cell did not become ready." >&2
    exit 1
  fi
  sleep 2
done

trap - ERR
rm -rf "$api_previous" "$agent_previous"
systemctl status "$api_service" "$agent_service" --no-pager
echo "PRODUCTION API AND AGENT DEPLOYED IN GERMANY (${release_id})"
REMOTE

echo "Production API and agent promotion completed."
