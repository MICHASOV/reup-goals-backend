#!/usr/bin/env bash

set -Eeuo pipefail

backend_source="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
frontend_source="${FRONTEND_REPO:-$(cd "$backend_source/.." && pwd)/reup-goals-landing}"
release_root="$(mktemp -d "${TMPDIR:-/tmp}/reup-production.XXXXXX")"
backend_release="$release_root/backend"
frontend_release="$release_root/frontend"

cleanup() {
  git -C "$backend_source" worktree remove --force "$backend_release" >/dev/null 2>&1 || true
  git -C "$frontend_source" worktree remove --force "$frontend_release" >/dev/null 2>&1 || true
  rm -rf "$release_root"
}
trap cleanup EXIT

echo "Fetching committed production candidates..."
git -C "$backend_source" fetch origin main
git -C "$frontend_source" fetch origin main

echo "Preparing clean release worktrees..."
git -C "$backend_source" worktree add --detach "$backend_release" origin/main
git -C "$frontend_source" worktree add --detach "$frontend_release" origin/main

FRONTEND_REPO="$frontend_release" \
  "$backend_release/scripts/promote-production-all.sh" "$@"
