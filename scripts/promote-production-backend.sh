#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backend_artifact="/tmp/reup_goals_backend"
release_id="production-$(git -C "$repo_root" rev-parse --short=12 HEAD)"
production_target="${REUP_PRODUCTION_SSH_TARGET:-reup}"

cd "$repo_root"
echo "Building production backend..."
GOCACHE=/private/tmp/reup-release-cache \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$backend_artifact" ./cmd/api

echo "Uploading backend..."
scp "$backend_artifact" "${production_target}:/tmp/"

echo "Installing backend with automatic rollback..."
ssh "$production_target" bash -s -- "$release_id" <<'REMOTE'
set -Eeuo pipefail

release_id=$1
timestamp=$(date +%Y%m%d-%H%M%S)
service=reup-goals.service
binary=/opt/reup-goals-backend/reup_goals_backend
rollback_binary="${binary}.rollback-${timestamp}"
dropin_dir=/etc/systemd/system/reup-goals.service.d
release_config=${dropin_dir}/ai-split.conf
release_config_backup="${release_config}.rollback-${timestamp}"
env_files=(/etc/reup-goals/backend.env /opt/reup-goals-backend/.env)
env_backups=()
changed=false

read_config_value() {
  local key=$1
  local file value
  for file in "${env_files[@]}"; do
    if [ -f "$file" ]; then
      value=$(grep -E "^${key}=" "$file" | tail -1 | cut -d= -f2- || true)
      if [ -n "$value" ]; then
        printf '%s' "$value"
        return 0
      fi
    fi
  done
}

restore() {
  local exit_code=$?
  trap - ERR
  echo "Backend deployment failed; restoring the previous production configuration." >&2
  if [ "$changed" = true ]; then
    systemctl stop "$service" >/dev/null 2>&1 || true
    [ -f "$rollback_binary" ] && install -m 755 "$rollback_binary" "$binary"
    if [ -f "$release_config_backup" ]; then
      mv -f "$release_config_backup" "$release_config"
    else
      rm -f "$release_config"
    fi
    local index=0 file backup
    for file in "${env_files[@]}"; do
      backup=${env_backups[$index]:-}
      [ -n "$backup" ] && [ -f "$backup" ] && mv -f "$backup" "$file"
      index=$((index + 1))
    done
    systemctl daemon-reload
    systemctl start "$service" >/dev/null 2>&1 || true
  fi
  journalctl -u "$service" -n 120 --no-pager || true
  exit "$exit_code"
}
trap restore ERR

runtime_url=$(read_config_value AGENT_RUNTIME_URL)
runtime_secret=$(read_config_value AGENT_RUNTIME_SECRET)
gateway_base_url=$(read_config_value OPENAI_BASE_URL)
gateway_secret=$(read_config_value OPENAI_GATEWAY_SECRET)

if [[ "$runtime_url" != https://* ]] || [[ "$gateway_base_url" != https://*/openai/v1 ]]; then
  echo "Remote AGENT_RUNTIME_URL and OPENAI_BASE_URL must be configured with HTTPS before deployment." >&2
  exit 1
fi
if [ "${#runtime_secret}" -lt 32 ] || [ "${#gateway_secret}" -lt 32 ]; then
  echo "AGENT_RUNTIME_SECRET and OPENAI_GATEWAY_SECRET must each contain at least 32 characters." >&2
  exit 1
fi

runtime_health_payload=$(curl --fail --silent --show-error --max-time 20 "${runtime_url}/healthz" || true)
gateway_auth=$(curl --silent --show-error --max-time 20 --output /dev/null --write-out '%{http_code}' \
  --header "Authorization: Bearer ${gateway_secret}" "${gateway_base_url}/models" || true)
if [[ "$runtime_health_payload" != *'"runtime":true'* ]] || [[ "$runtime_health_payload" != *'"gateway":true'* ]] || [ "$gateway_auth" != 404 ]; then
  echo "German AI service is unavailable or its credentials do not match (health=${runtime_health_payload}, gateway=${gateway_auth})." >&2
  exit 1
fi

mkdir -p "$dropin_dir"
cp -a "$binary" "$rollback_binary"
if [ -f "$release_config" ]; then cp -a "$release_config" "$release_config_backup"; fi
for file in "${env_files[@]}"; do
  if [ -f "$file" ]; then
    backup="${file}.rollback-${timestamp}"
    cp -a "$file" "$backup"
    env_backups+=("$backup")
    sed -i '/^OPENAI_API_KEY=/d' "$file"
  else
    env_backups+=("")
  fi
done
changed=true

cat > "$release_config" <<EOF
[Service]
Environment="AGENT_RUNTIME_ENABLED=true"
Environment="AGENT_RUNTIME_MAX_TURNS=120"
Environment="AGENT_RELEASE_ID=${release_id}"
Environment="OPENAI_PROXY_URL=direct"
EOF
chmod 600 "$release_config"

install -m 755 /tmp/reup_goals_backend "$binary"
systemctl daemon-reload
systemctl reset-failed "$service" || true
systemctl restart "$service"

for attempt in $(seq 1 45); do
  health=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz || true)
  privacy=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v2/privacy/legal-documents || true)
  if [ "$health" = 200 ] && [ "$privacy" = 200 ]; then break; fi
  if [ "$attempt" -eq 45 ]; then
    echo "Production backend failed its health check." >&2
    exit 1
  fi
  sleep 2
done

trap - ERR
rm -f "$rollback_binary" "$release_config_backup"
for backup in "${env_backups[@]}"; do [ -n "$backup" ] && rm -f "$backup"; done

# The old colocated runtime is removed only after the remote path is healthy.
systemctl disable --now reup-goals-agent-production.service >/dev/null 2>&1 || true
systemctl status "$service" --no-pager
echo "BACKEND DEPLOYED WITH REMOTE AI RUNTIME"
REMOTE

echo "Checking public production endpoints..."
curl --fail --show-error --silent https://api.reupgoals.pro/healthz
echo
curl --fail --show-error --silent https://api.reupgoals.pro/api/v2/privacy/legal-documents
echo
echo "Production backend promotion completed."
