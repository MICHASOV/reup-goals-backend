#!/usr/bin/env bash

set -Eeuo pipefail

production_target="${REUP_PRODUCTION_SSH_TARGET:-root@109.73.198.164}"
germany_target="${REUP_AI_SSH_TARGET:-root@167.233.230.212}"
germany_host="${REUP_AI_HOST:-${germany_target#*@}}"
remote_port="${REUP_AI_RETURN_PORT:-18080}"

echo "Preparing the private production API return channel..."
public_key="$(ssh "$production_target" 'bash -s' <<'RUSSIA_KEY'
set -Eeuo pipefail
install -d -m 700 /etc/reup-goals/ssh
key=/etc/reup-goals/ssh/ai-return-tunnel
if [ ! -f "$key" ]; then
  ssh-keygen -q -t ed25519 -N '' -C reup-goals-ai-return-tunnel -f "$key"
fi
chmod 600 "$key"
chmod 644 "${key}.pub"
cat "${key}.pub"
RUSSIA_KEY
)"

if [[ "$public_key" != ssh-ed25519\ * ]]; then
  echo "Could not obtain the return-tunnel public key." >&2
  exit 1
fi
public_key_base64="$(printf '%s' "$public_key" | base64 | tr -d '\n')"

echo "Authorizing the return channel on the German AI host..."
ssh "$germany_target" bash -s -- "$public_key_base64" "$remote_port" <<'GERMANY_AUTH'
set -Eeuo pipefail
public_key="$(printf '%s' "$1" | base64 -d)"
remote_port=$2
if [[ "$public_key" != ssh-ed25519\ * ]]; then
  echo "The return-tunnel public key is invalid." >&2
  exit 1
fi
install -d -m 700 /root/.ssh
touch /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
sed -i '/reup-goals-ai-return-tunnel$/d' /root/.ssh/authorized_keys
printf 'restrict,port-forwarding,permitlisten="127.0.0.1:%s" %s\n' "$remote_port" "$public_key" >> /root/.ssh/authorized_keys
GERMANY_AUTH

echo "Starting the persistent return channel from Russia to Germany..."
ssh "$production_target" bash -s -- "$germany_host" "$remote_port" <<'RUSSIA_TUNNEL'
set -Eeuo pipefail
germany_host=$1
remote_port=$2
key=/etc/reup-goals/ssh/ai-return-tunnel
known_hosts=/etc/reup-goals/ssh/known_hosts
unit=/etc/systemd/system/reup-goals-ai-return-tunnel.service

ssh-keyscan -H "$germany_host" > "${known_hosts}.next"
test -s "${known_hosts}.next"
mv "${known_hosts}.next" "$known_hosts"
chmod 600 "$known_hosts"

cat > "$unit" <<EOF
[Unit]
Description=REUP.goals private return channel for German AI runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/ssh -NT \\
  -i ${key} \\
  -o BatchMode=yes \\
  -o ExitOnForwardFailure=yes \\
  -o ServerAliveInterval=20 \\
  -o ServerAliveCountMax=3 \\
  -o ConnectTimeout=10 \\
  -o StrictHostKeyChecking=yes \\
  -o UserKnownHostsFile=${known_hosts} \\
  -R 127.0.0.1:${remote_port}:127.0.0.1:8080 \\
  root@${germany_host}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "$unit"
systemctl daemon-reload
systemctl enable --now reup-goals-ai-return-tunnel.service
systemctl restart reup-goals-ai-return-tunnel.service

for attempt in $(seq 1 30); do
  if systemctl is-active --quiet reup-goals-ai-return-tunnel.service; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    journalctl -u reup-goals-ai-return-tunnel.service -n 120 --no-pager || true
    echo "The private return channel did not become active." >&2
    exit 1
  fi
  sleep 1
done
RUSSIA_TUNNEL

echo "Switching the German runtime to the private callback..."
ssh "$germany_target" bash -s -- "$remote_port" <<'GERMANY_RUNTIME'
set -Eeuo pipefail
remote_port=$1
env_file=/etc/reup-goals/ai-production.env
service=reup-goals-ai.service

set_env() {
  key=$1
  value=$2
  if grep -q "^${key}=" "$env_file"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$env_file"
  else
    printf '\n%s=%s\n' "$key" "$value" >> "$env_file"
  fi
}

for attempt in $(seq 1 30); do
  if curl --fail --silent --max-time 3 "http://127.0.0.1:${remote_port}/healthz" >/dev/null; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    echo "The private callback did not reach the production API." >&2
    exit 1
  fi
  sleep 1
done

set_env GO_INTERNAL_URL "http://127.0.0.1:${remote_port}"
set_env AGENT_PROVIDER_TIMEOUT 45m
set_env OPENAI_GATEWAY_TIMEOUT 45m
chmod 600 "$env_file"
systemctl restart "$service"

for attempt in $(seq 1 45); do
  ready=$(curl --silent --max-time 5 "http://127.0.0.1:8092/readyz" || true)
  if [[ "$ready" == *'"callback":true'* ]]; then
    break
  fi
  if [ "$attempt" -eq 45 ]; then
    journalctl -u "$service" -n 120 --no-pager || true
    echo "German AI runtime is not ready to call the production API." >&2
    exit 1
  fi
  sleep 1
done
printf '%s\n' "$ready"
GERMANY_RUNTIME

echo "Production AI return channel is ready."
