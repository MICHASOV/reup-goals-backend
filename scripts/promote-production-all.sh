#!/usr/bin/env bash

set -Eeuo pipefail

backend_source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
frontend_source_root="${FRONTEND_REPO:-$(cd "$backend_source_root/.." && pwd)/reup-goals-landing}"
backend_root="$backend_source_root"
frontend_root="$frontend_source_root"
release_workspace=""
release_backend_root=""
release_frontend_root=""
frontend_deploy_path="${FRONTEND_DEPLOY_PATH:-/var/www/reupgoals.pro}"
production_host="${REUP_PRODUCTION_HOST:-167.233.230.212}"
production_user="${REUP_PRODUCTION_USER:-root}"
production_target="${REUP_PRODUCTION_SSH_TARGET:-${production_user}@${production_host}}"
production_key="${REUP_PRODUCTION_KEY:-$HOME/.ssh/reup_goals_staging_deploy}"
release_id="$(date -u +%Y%m%d-%H%M%S)"
frontend_backup="/var/backups/reup-goals/frontend-${release_id}.tar.gz"
frontend_deploy_started=false
dry_run=false

if [[ "${1:-}" == "--dry-run" ]]; then
  dry_run=true
elif [[ -n "${1:-}" ]]; then
  echo "Usage: $0 [--dry-run]" >&2
  exit 2
fi

required_commands=(curl git go npm rsync scp ssh)
for command_name in "${required_commands[@]}"; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Missing required command: $command_name" >&2
    exit 1
  }
done

SSH_ARGS=(-o BatchMode=yes -o ConnectTimeout=15 -o IdentitiesOnly=yes)
if [[ -f "$production_key" ]]; then
  SSH_ARGS+=(-i "$production_key")
fi

if ! ssh "${SSH_ARGS[@]}" "$production_target" true; then
  echo "German production server is unreachable over SSH: $production_target" >&2
  exit 1
fi
if ! ssh "${SSH_ARGS[@]}" "$production_target" test -s /etc/reup-goals-production/backend.env; then
  echo "German production is not initialized. Run scripts/migrate-production-to-germany.sh first." >&2
  exit 1
fi

if ! git -C "$frontend_source_root" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Frontend repository not found: $frontend_source_root" >&2
  exit 1
fi

assert_release_revision() {
  local repo=$1 label=$2 branch head_revision remote_revision
  git -C "$repo" fetch --quiet origin main
  branch="$(git -C "$repo" branch --show-current)"
  if [[ -n "$branch" && "$branch" != main ]]; then
    echo "$label must be on main, current branch: $branch" >&2
    exit 1
  fi
  head_revision="$(git -C "$repo" rev-parse HEAD)"
  remote_revision="$(git -C "$repo" rev-parse origin/main)"
  if [[ "$head_revision" != "$remote_revision" ]]; then
    echo "$label HEAD does not match origin/main." >&2
    exit 1
  fi
}

cleanup_release_workspace() {
  [[ -z "$release_backend_root" ]] || git -C "$backend_source_root" worktree remove --force "$release_backend_root" >/dev/null 2>&1 || true
  [[ -z "$release_frontend_root" ]] || git -C "$frontend_source_root" worktree remove --force "$release_frontend_root" >/dev/null 2>&1 || true
  [[ -z "$release_workspace" ]] || rm -rf "$release_workspace"
}
trap cleanup_release_workspace EXIT

wait_for_url() {
  local url=$1 expected_status=${2:-200} attempts=${3:-45} status
  for _ in $(seq 1 "$attempts"); do
    status="$(curl --max-time 15 -sS -o /dev/null -w '%{http_code}' "$url" || true)"
    [[ "$status" != "$expected_status" ]] || return 0
    sleep 2
  done
  echo "Production check failed: $url did not return $expected_status" >&2
  return 1
}

rollback_frontend() {
  [[ "$frontend_deploy_started" == true ]] || return 0
  echo "Frontend verification failed. Restoring the previous German production build..." >&2
  ssh "${SSH_ARGS[@]}" "$production_target" "set -euo pipefail
    test -f '$frontend_backup'
    rm -rf '$frontend_deploy_path'
    mkdir -p '$frontend_deploy_path'
    tar -xzf '$frontend_backup' -C '$(dirname "$frontend_deploy_path")'
    nginx -t
    systemctl reload nginx"
}

on_error() {
  local exit_code=$?
  trap - ERR
  rollback_frontend || true
  echo "Production release stopped with exit code $exit_code." >&2
  exit "$exit_code"
}
trap on_error ERR

assert_release_revision "$backend_source_root" Backend
assert_release_revision "$frontend_source_root" Frontend

backend_revision="$(git -C "$backend_source_root" rev-parse --short=12 HEAD)"
frontend_revision="$(git -C "$frontend_source_root" rev-parse --short=12 HEAD)"
release_workspace="$(mktemp -d /private/tmp/reup-production-release.XXXXXX)"
release_backend_root="$release_workspace/reup-goals-backend"
release_frontend_root="$release_workspace/reup-goals-landing"
git -C "$backend_source_root" worktree add --detach --quiet "$release_backend_root" origin/main
git -C "$frontend_source_root" worktree add --detach --quiet "$release_frontend_root" origin/main
backend_root="$release_backend_root"
frontend_root="$release_frontend_root"

echo "Release revisions:"
echo "  backend:  $backend_revision"
echo "  frontend: $frontend_revision"
echo "Local uncommitted files are excluded from this release."

echo "Checking staging..."
wait_for_url https://api-staging.reupgoals.pro/readyz
wait_for_url https://staging.reupgoals.pro/cabinet-v2/

echo "Running backend and agent tests..."
(
  cd "$backend_root"
  GOCACHE=/private/tmp/reup-release-test-cache go test ./...
  cd agent-runtime
  npm ci
  npm run typecheck
  npm test
)

echo "Checking frontend..."
(
  cd "$frontend_root"
  npm ci
  npm run typecheck
  npm run lint
  npm audit --omit=dev --audit-level=high
)

if [[ "$dry_run" == true ]]; then
  echo "Dry run completed. Production was not changed."
  exit 0
fi

echo "Backing up the current German production frontend..."
ssh "${SSH_ARGS[@]}" "$production_target" "set -euo pipefail
  mkdir -p /var/backups/reup-goals
  if [ -d '$frontend_deploy_path' ]; then
    tar -czf '$frontend_backup' -C '$(dirname "$frontend_deploy_path")' '$(basename "$frontend_deploy_path")'
  else
    mkdir -p '$frontend_deploy_path'
    tar -czf '$frontend_backup' -C '$(dirname "$frontend_deploy_path")' '$(basename "$frontend_deploy_path")'
  fi
  find /var/backups/reup-goals -type f -name 'frontend-*.tar.gz' -mtime +14 -delete"

echo "Promoting the colocated production API and agent..."
REUP_PRODUCTION_HOST="$production_host" \
REUP_PRODUCTION_USER="$production_user" \
REUP_PRODUCTION_SSH_TARGET="$production_target" \
REUP_PRODUCTION_KEY="$production_key" \
  "$backend_root/scripts/promote-production-backend.sh"

echo "Promoting frontend to German production..."
frontend_deploy_started=true
(
  cd "$frontend_root"
  NEXT_PUBLIC_API_BASE_URL=https://api.reupgoals.pro \
    DEPLOY_HOST="$production_host" \
    DEPLOY_USER="$production_user" \
    DEPLOY_KEY="$production_key" \
    DEPLOY_PATH="$frontend_deploy_path" \
    bash "$frontend_root/scripts/deploy.sh"
)

echo "Running public production checks..."
wait_for_url https://api.reupgoals.pro/readyz
wait_for_url https://api.reupgoals.pro/api/v2/privacy/legal-documents
wait_for_url https://reupgoals.pro/login/
wait_for_url https://reupgoals.pro/cabinet-v2/

frontend_deploy_started=false
trap - ERR
echo
echo "Production release completed successfully in Germany."
echo "Backend:  $backend_revision"
echo "Frontend: $frontend_revision"
