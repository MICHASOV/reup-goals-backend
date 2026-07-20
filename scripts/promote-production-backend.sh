#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact="/tmp/reup_goals_backend"

cd "$repo_root"

echo "Building production backend..."
GOCACHE=/private/tmp/reup-release-cache \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$artifact" ./cmd/api

echo "Uploading backend..."
scp "$artifact" reup:/tmp/reup_goals_backend

echo "Installing backend with automatic rollback..."
ssh reup 'bash -s' <<'REMOTE'
set -uo pipefail

dropin_dir=/etc/systemd/system/reup-goals.service.d
web_config=${dropin_dir}/web-security.conf
web_config_backup=${dropin_dir}/web-security.conf.rollback
current=/opt/reup-goals-backend/reup_goals_backend
rollback="${current}.jwt-rollback-$(date +%Y%m%d-%H%M%S)"

mkdir -p "$dropin_dir"
cp -a "$current" "$rollback"
if [ -f "$web_config" ]; then
  cp -a "$web_config" "$web_config_backup"
else
  rm -f "$web_config_backup"
fi

cat > "$web_config" <<'EOF'
[Service]
Environment="BROWSER_AUTH_ONLY=true"
Environment="COOKIE_SECURE=true"
Environment="CORS_ALLOWED_ORIGINS=https://reupgoals.pro,https://www.reupgoals.pro"
EOF
chmod 600 "$web_config"

systemctl daemon-reload
systemctl reset-failed reup-goals.service || true
systemctl stop reup-goals.service
install -m 755 /tmp/reup_goals_backend "$current"
systemctl start reup-goals.service

deployed=false
for attempt in $(seq 1 45); do
  health="$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz || true)"
  privacy="$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v2/privacy/legal-documents || true)"

  if [ "$health" = "200" ] && [ "$privacy" = "200" ]; then
    deployed=true
    break
  fi

  sleep 2
done

if [ "$deployed" = true ]; then
  echo "BACKEND DEPLOYED"
  rm -f "$web_config_backup"
  systemctl status reup-goals.service --no-pager
else
  echo "DEPLOY FAILED"
  journalctl -u reup-goals.service -n 80 --no-pager > /tmp/reup-goals-deploy-failure.log
  systemctl stop reup-goals.service
  install -m 755 "$rollback" "$current"
  if [ -f "$web_config_backup" ]; then
    mv "$web_config_backup" "$web_config"
  else
    rm -f "$web_config"
  fi
  systemctl daemon-reload
  systemctl reset-failed reup-goals.service || true
  systemctl start reup-goals.service
  sleep 3
  cat /tmp/reup-goals-deploy-failure.log
  curl --fail --show-error --silent http://127.0.0.1:8080/healthz || true
  exit 1
fi
REMOTE

echo "Checking public production endpoints..."
curl --fail --show-error --silent https://api.reupgoals.pro/healthz
echo
curl --fail --show-error --silent https://api.reupgoals.pro/api/v2/privacy/legal-documents
echo
echo "Production backend promotion completed."
