#!/usr/bin/env bash

set -Eeuo pipefail

russia_target="${REUP_LEGACY_PRODUCTION_SSH_TARGET:-reup}"
key_path="${REUP_GITHUB_MIGRATION_KEY:-$HOME/.ssh/reup_goals_github_production_migration}"
secret_name=PRODUCTION_RUSSIA_SSH_KEY_B64
github_secrets_url="https://github.com/MICHASOV/reup-goals-backend/settings/secrets/actions"

for command_name in ssh ssh-keygen base64 pbcopy; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Missing required command: $command_name" >&2
    exit 1
  }
done

install -d -m 700 "$(dirname "$key_path")"
if [[ ! -s "$key_path" || ! -s "${key_path}.pub" ]]; then
  ssh-keygen -q -t ed25519 -N '' \
    -C reup-goals-github-production-migration \
    -f "$key_path"
fi
chmod 600 "$key_path"
chmod 644 "${key_path}.pub"

public_key_b64="$(base64 < "${key_path}.pub" | tr -d '\n')"

echo "Installing a restricted one-time migration key on the Russian production host..."
ssh -o BatchMode=yes -o ConnectTimeout=20 "$russia_target" \
  sudo bash -s -- "$public_key_b64" <<'REMOTE'
set -Eeuo pipefail

public_key="$(printf '%s' "$1" | base64 -d)"
install -d -m 700 /root/.ssh
touch /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
sed -i '/reup-goals-github-production-migration$/d' /root/.ssh/authorized_keys
printf 'restrict %s\n' "$public_key" >> /root/.ssh/authorized_keys
REMOTE

base64 < "$key_path" | tr -d '\n' | pbcopy

cat <<EOF

The one-time key is installed and copied to the clipboard.

Open:
  $github_secrets_url

Create repository secret:
  Name:  $secret_name
  Value: paste from clipboard

The server authorization is removed automatically after successful activation.
EOF
