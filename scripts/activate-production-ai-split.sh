#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
germany_target="${REUP_AI_SSH_TARGET:-root@167.233.230.212}"
production_target="${REUP_PRODUCTION_SSH_TARGET:-reup}"
secrets_file="$(mktemp)"
release_worktree_parent="$(mktemp -d)"
release_worktree="${release_worktree_parent}/release"

cleanup() {
  rm -f "$secrets_file"
  if [ -d "$release_worktree" ]; then
    git -C "$repo_root" worktree remove --force "$release_worktree" >/dev/null 2>&1 || true
  fi
  rmdir "$release_worktree_parent" >/dev/null 2>&1 || true
}
trap cleanup EXIT
chmod 600 "$secrets_file"

echo "Reading production transport secrets from the German AI host..."
ssh "$germany_target" 'bash -s' > "$secrets_file" <<'GERMANY'
set -Eeuo pipefail

env_file=/etc/reup-goals/ai-production.env
if [ ! -f "$env_file" ]; then
  echo "German production AI environment is missing." >&2
  exit 1
fi

read_env() {
  grep -E "^${1}=" "$env_file" | tail -1 | cut -d= -f2- || true
}

runtime_secret="$(read_env AGENT_RUNTIME_SECRET)"
gateway_secret="$(read_env AI_GATEWAY_SECRET)"
if [ "${#runtime_secret}" -lt 32 ] || [ "${#gateway_secret}" -lt 32 ]; then
  echo "German production transport secrets are invalid." >&2
  exit 1
fi
if [ "$runtime_secret" = "$gateway_secret" ]; then
  echo "German production transport secrets must be different." >&2
  exit 1
fi

printf '%s\n%s\n' "$runtime_secret" "$gateway_secret"
GERMANY

runtime_secret="$(sed -n '1p' "$secrets_file")"
gateway_secret="$(sed -n '2p' "$secrets_file")"
if [ "${#runtime_secret}" -lt 32 ] || [ "${#gateway_secret}" -lt 32 ]; then
  echo "Could not retrieve valid production transport secrets." >&2
  exit 1
fi

echo "Configuring the Russian production API for the German AI runtime..."
{
  printf 'runtime_secret=%q\n' "$runtime_secret"
  printf 'gateway_secret=%q\n' "$gateway_secret"
  cat <<'RUSSIA'
set -Eeuo pipefail

primary_env=/etc/reup-goals/backend.env
legacy_env=/opt/reup-goals-backend/.env
mkdir -p /etc/reup-goals
touch "$primary_env"
chmod 600 "$primary_env"

set_env() {
  file=$1
  key=$2
  value=$3
  if grep -q "^${key}=" "$file"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$file"
  else
    printf '\n%s=%s\n' "$key" "$value" >> "$file"
  fi
}

for env_file in "$primary_env" "$legacy_env"; do
  if [ "$env_file" = "$primary_env" ] || [ -f "$env_file" ]; then
    set_env "$env_file" AGENT_RUNTIME_URL https://ai.reupgoals.pro
    set_env "$env_file" AGENT_RUNTIME_SECRET "$runtime_secret"
    set_env "$env_file" OPENAI_BASE_URL https://ai.reupgoals.pro/openai/v1
    set_env "$env_file" OPENAI_GATEWAY_SECRET "$gateway_secret"
    chmod 600 "$env_file"
  fi
done

echo "Russian production AI transport configuration saved."
RUSSIA
} | ssh "$production_target" 'bash -s'

runtime_secret=
gateway_secret=
: > "$secrets_file"

echo "Promoting the Russian backend and switching production AI traffic..."
git -C "$repo_root" worktree add --detach "$release_worktree" HEAD >/dev/null
REUP_PRODUCTION_SSH_TARGET="$production_target" \
  "$release_worktree/scripts/promote-production-backend.sh"

echo "Production AI split is active."
