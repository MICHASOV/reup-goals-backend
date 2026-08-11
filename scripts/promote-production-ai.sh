#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_artifact="/tmp/reup_goals_ai_runtime.tar.gz"
runtime_stage="$(mktemp -d /tmp/reup-goals-ai-runtime.XXXXXX)"
node_version="${AGENT_NODE_VERSION:-v22.17.0}"
ai_target="${REUP_AI_SSH_TARGET:-reup-ai}"
ai_public_url="${REUP_AI_PUBLIC_URL:-https://ai.reupgoals.pro}"

cleanup() { rm -rf "$runtime_stage"; }
trap cleanup EXIT

echo "Building German AI runtime..."
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
  curl --fail --show-error --silent --location "https://nodejs.org/dist/${node_version}/${archive}" -o "$runtime_stage/$archive"
  curl --fail --show-error --silent --location "https://nodejs.org/dist/${node_version}/SHASUMS256.txt" -o "$runtime_stage/SHASUMS256.txt"
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

echo "Uploading German AI runtime..."
scp "$runtime_artifact" "${ai_target}:/tmp/"

echo "Installing German AI runtime with automatic rollback..."
ssh "$ai_target" bash -s <<'REMOTE'
set -Eeuo pipefail

service=reup-goals-ai.service
root=/opt/reup-goals-ai
next=${root}.next
previous=${root}.previous
env_file=/etc/reup-goals/ai-production.env
unit=/etc/systemd/system/${service}

if [ ! -f "$env_file" ]; then
  echo "$env_file is missing; install it from deploy/production/ai-runtime.env.example first." >&2
  exit 1
fi
read_env() { grep -E "^$1=" "$env_file" | tail -1 | cut -d= -f2- || true; }
runtime_port=$(read_env PORT)
openai_key=$(read_env OPENAI_API_KEY)
runtime_secret=$(read_env AGENT_RUNTIME_SECRET)
gateway_secret=$(read_env AI_GATEWAY_SECRET)
go_internal_url=$(read_env GO_INTERNAL_URL)
runtime_port=${runtime_port:-8092}
if ! [[ "$runtime_port" =~ ^[0-9]+$ ]] || [ "$runtime_port" -lt 1 ] || [ "$runtime_port" -gt 65535 ]; then
  echo "German AI PORT is invalid." >&2
  exit 1
fi
if [ -z "$openai_key" ] || [ "${#runtime_secret}" -lt 32 ] || [ "${#gateway_secret}" -lt 32 ] || [ "$runtime_secret" = "$gateway_secret" ] || [[ "$go_internal_url" != https://* ]]; then
  echo "German AI environment is incomplete or unsafe." >&2
  exit 1
fi

rollback() {
  local exit_code=$?
  trap - ERR
  systemctl stop "$service" >/dev/null 2>&1 || true
  rm -rf "$root"
  [ -d "$previous" ] && mv "$previous" "$root"
  systemctl daemon-reload
  [ -d "$root" ] && systemctl start "$service" >/dev/null 2>&1 || true
  journalctl -u "$service" -n 120 --no-pager || true
  exit "$exit_code"
}
trap rollback ERR

rm -rf "$next"
mkdir -p "$next"
tar -xzf /tmp/reup_goals_ai_runtime.tar.gz -C "$next"
test -x "$next/node"
test -f "$next/dist/server.js"
rm -rf "$previous"
[ -d "$root" ] && mv "$root" "$previous"
mv "$next" "$root"

cat > "$unit" <<'UNIT'
[Unit]
Description=REUP.goals German AI Runtime and Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/opt/reup-goals-ai
Environment=NODE_ENV=production
EnvironmentFile=/etc/reup-goals/ai-production.env
ExecStart=/opt/reup-goals-ai/node dist/server.js
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
chmod 644 "$unit"
chown -R www-data:www-data "$root"
chmod 600 "$env_file"
systemctl daemon-reload
systemctl enable "$service"
systemctl restart "$service"
for attempt in $(seq 1 45); do
  health=$(curl --fail --silent "http://127.0.0.1:${runtime_port}/healthz" || true)
  if [[ "$health" == *'"runtime":true'* ]] && [[ "$health" == *'"gateway":true'* ]]; then break; fi
  if [ "$attempt" -eq 45 ]; then exit 1; fi
  sleep 1
done

trap - ERR
rm -rf "$previous"
systemctl status "$service" --no-pager
REMOTE

echo "Checking public German AI endpoint..."
public_health=$(curl --fail --show-error --silent "${ai_public_url}/healthz")
if [[ "$public_health" != *'"runtime":true'* ]] || [[ "$public_health" != *'"gateway":true'* ]]; then
  echo "German AI public health check is incomplete: ${public_health}" >&2
  exit 1
fi
printf '%s\n' "$public_health"
echo "German AI runtime promotion completed."
