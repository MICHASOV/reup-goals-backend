#!/usr/bin/env bash

set -Eeuo pipefail

backend_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
frontend_root="${FRONTEND_REPO:-$(cd "$backend_root/.." && pwd)/reup-goals-landing}"
frontend_deploy_path="${FRONTEND_DEPLOY_PATH:-/var/www/reupgoals.pro}"
release_id="$(date -u +%Y%m%d-%H%M%S)"
frontend_backup="/var/backups/reup-goals/frontend-${release_id}.tar.gz"
frontend_csp_backup="/var/backups/reup-goals/frontend-${release_id}.csp.conf"
frontend_csp_missing_marker="/var/backups/reup-goals/frontend-${release_id}.no-csp"
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
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Missing required command: $command_name" >&2
    exit 1
  fi
done

if [[ ! -d "$frontend_root/.git" ]]; then
  echo "Frontend repository not found: $frontend_root" >&2
  exit 1
fi

assert_release_revision() {
  local repo="$1"
  local label="$2"
  shift 2
  local branch
  local head_revision
  local remote_revision

  git -C "$repo" fetch --quiet origin main
  branch="$(git -C "$repo" branch --show-current)"
  if [[ "$branch" != "main" ]]; then
    echo "$label must be on main, current branch: $branch" >&2
    exit 1
  fi

  head_revision="$(git -C "$repo" rev-parse HEAD)"
  remote_revision="$(git -C "$repo" rev-parse origin/main)"
  if [[ "$head_revision" != "$remote_revision" ]]; then
    echo "$label HEAD does not match origin/main." >&2
    exit 1
  fi

  if ! git -C "$repo" diff --quiet -- "$@"; then
    echo "$label has uncommitted release files." >&2
    exit 1
  fi
  if ! git -C "$repo" diff --cached --quiet -- "$@"; then
    echo "$label has staged but uncommitted release files." >&2
    exit 1
  fi
}

wait_for_url() {
  local url="$1"
  local expected_status="${2:-200}"
  local attempts="${3:-45}"
  local status

  for _ in $(seq 1 "$attempts"); do
    status="$(curl --max-time 15 -sS -o /dev/null -w '%{http_code}' "$url" || true)"
    if [[ "$status" == "$expected_status" ]]; then
      return 0
    fi
    sleep 2
  done
  echo "Production check failed: $url did not return $expected_status" >&2
  return 1
}

rollback_frontend() {
  if [[ "$frontend_deploy_started" != true ]]; then
    return
  fi
  echo "Frontend verification failed. Restoring the previous production build..." >&2
  ssh reup "set -euo pipefail
    test -f '$frontend_backup'
    rm -rf '$frontend_deploy_path'
    mkdir -p '$frontend_deploy_path'
    tar -xzf '$frontend_backup' -C '$(dirname "$frontend_deploy_path")'
    if [ -f '$frontend_csp_backup' ]; then
      install -m 644 '$frontend_csp_backup' /etc/nginx/snippets/reupgoals-production-csp.conf
    elif [ -f '$frontend_csp_missing_marker' ]; then
      rm -f /etc/nginx/snippets/reupgoals-production-csp.conf
    fi
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

assert_release_revision "$backend_root" "Backend" .
assert_release_revision "$frontend_root" "Frontend" . ":(exclude)next-env.d.ts"

backend_revision="$(git -C "$backend_root" rev-parse --short=12 HEAD)"
frontend_revision="$(git -C "$frontend_root" rev-parse --short=12 HEAD)"

echo "Release revisions:"
echo "  backend:  $backend_revision"
echo "  frontend: $frontend_revision"

echo "Checking staging..."
wait_for_url "https://api-staging.reupgoals.pro/healthz"
wait_for_url "https://staging.reupgoals.pro/cabinet-v2/"

echo "Running backend tests..."
(
  cd "$backend_root"
  GOCACHE=/private/tmp/reup-release-test-cache go test ./...
)

echo "Checking frontend..."
(
  cd "$frontend_root"
  npm run typecheck
  npm run lint
  npm audit --omit=dev --audit-level=high
)

if [[ "$dry_run" == true ]]; then
  echo "Dry run completed. Production was not changed."
  exit 0
fi

echo "Backing up the current production frontend..."
ssh reup "set -euo pipefail
  mkdir -p /var/backups/reup-goals
  test -d '$frontend_deploy_path'
  tar -czf '$frontend_backup' -C '$(dirname "$frontend_deploy_path")' '$(basename "$frontend_deploy_path")'
  if [ -f /etc/nginx/snippets/reupgoals-production-csp.conf ]; then
    cp /etc/nginx/snippets/reupgoals-production-csp.conf '$frontend_csp_backup'
  else
    touch '$frontend_csp_missing_marker'
  fi
  find /var/backups/reup-goals -type f -name 'frontend-*.tar.gz' -mtime +14 -delete"

echo "Promoting backend to production..."
"$backend_root/scripts/promote-production-backend.sh"

echo "Promoting frontend to production..."
frontend_deploy_started=true
(
  cd "$frontend_root"
  NEXT_PUBLIC_API_BASE_URL=https://api.reupgoals.pro \
    DEPLOY_PATH="$frontend_deploy_path" \
    "$frontend_root/scripts/deploy.sh"
)

echo "Running public production checks..."
wait_for_url "https://api.reupgoals.pro/healthz"
wait_for_url "https://api.reupgoals.pro/api/v2/privacy/legal-documents"
wait_for_url "https://reupgoals.pro/login/"
wait_for_url "https://reupgoals.pro/cabinet-v2/"

frontend_deploy_started=false
trap - ERR

echo
echo "Production release completed successfully."
echo "Backend:  $backend_revision"
echo "Frontend: $frontend_revision"
