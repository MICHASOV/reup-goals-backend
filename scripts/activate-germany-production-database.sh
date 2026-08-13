#!/usr/bin/env bash

set -Eeuo pipefail

config_root=${REUP_PRODUCTION_CONFIG_ROOT:-/etc/reup-goals-production}
active_env=${REUP_PRODUCTION_BACKEND_ENV:-${config_root}/backend.env}
remote_env=${REUP_REMOTE_DATABASE_ENV:-${config_root}/backend.remote-db.env}
candidate_env=${REUP_DATABASE_CANDIDATE_ENV:-${config_root}/backend.local-db.env}
prepare_script=${REUP_DATABASE_PREPARE_SCRIPT:-/tmp/prepare-germany-production-database.sh}
mode=${1:-status}
api_service=${REUP_PRODUCTION_API_SERVICE:-reup-goals-production.service}
agent_service=${REUP_PRODUCTION_AGENT_SERVICE:-reup-goals-agent-production.service}
lock_file=/run/lock/reup-goals-production-database.lock

read_env() {
  grep -E "^$2=" "$1" | tail -1 | cut -d= -f2- || true
}

fail() {
  printf 'GERMANY_DATABASE_ACTIVATION_FAILED=%s\n' "$1" >&2
  exit 1
}

is_local_host() {
  case "$1" in 127.0.0.1|localhost|::1) return 0 ;; *) return 1 ;; esac
}

wait_ready() {
  local attempt api_code agent_code
  for attempt in $(seq 1 90); do
    api_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8082/readyz || true)
    agent_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8092/readyz || true)
    if [[ "$api_code" == 200 && "$agent_code" == 200 ]]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

[[ $EUID -eq 0 ]] || fail run_as_root
install -d -m 755 "$(dirname "$lock_file")"
exec 9>"$lock_file"
flock -n 9 || fail migration_already_running

case "$mode" in
  prepare)
    [[ -s "$active_env" ]] || fail missing_active_environment
    active_host=$(read_env "$active_env" DB_HOST)
    if ! is_local_host "$active_host"; then
      install -m 600 "$active_env" "$remote_env"
    elif [[ ! -s "$remote_env" ]]; then
      fail missing_remote_database_environment
    fi
    remote_host=$(read_env "$remote_env" DB_HOST)
    is_local_host "$remote_host" && fail invalid_remote_database_environment
    [[ -x "$prepare_script" ]] || fail missing_prepare_script
    REUP_SOURCE_ENV="$remote_env" \
      REUP_DATABASE_CANDIDATE_ENV="$candidate_env" \
      "$prepare_script"
    [[ "$(read_env "$candidate_env" DB_HOST)" == 127.0.0.1 ]] || fail invalid_candidate_environment
    printf 'GERMANY_DATABASE_CANDIDATE_READY=true\n'
    ;;

  activate)
    [[ -s "$candidate_env" ]] || fail missing_candidate_environment
    [[ "$(read_env "$candidate_env" DB_HOST)" == 127.0.0.1 ]] || fail invalid_candidate_environment
    timestamp=$(date -u +%Y%m%dT%H%M%SZ)
    rollback_env="${config_root}/backend.pre-local-db-${timestamp}.env"
    install -m 600 "$active_env" "$rollback_env"
    systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
    install -m 600 "$candidate_env" "$active_env"
    systemctl daemon-reload
    systemctl enable --now "$api_service" "$agent_service" >/dev/null
    if ! wait_ready; then
      systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
      install -m 600 "$rollback_env" "$active_env"
      systemctl start "$api_service" "$agent_service" >/dev/null 2>&1 || true
      journalctl -u "$api_service" -u "$agent_service" -n 120 --no-pager >&2 || true
      fail readiness_check_failed_and_environment_rolled_back
    fi
    printf 'GERMANY_LOCAL_DATABASE_ACTIVE=true\n'
    ;;

  rollback)
    [[ -s "$remote_env" ]] || fail missing_remote_database_environment
    systemctl stop "$agent_service" "$api_service" >/dev/null 2>&1 || true
    install -m 600 "$remote_env" "$active_env"
    systemctl start "$api_service" "$agent_service" >/dev/null
    wait_ready || fail remote_database_rollback_not_ready
    printf 'GERMANY_REMOTE_DATABASE_RESTORED=true\n'
    ;;

  status)
    [[ -s "$active_env" ]] || fail missing_active_environment
    active_host=$(read_env "$active_env" DB_HOST)
    if is_local_host "$active_host"; then
      printf 'ACTIVE_DATABASE=local-germany\n'
    else
      printf 'ACTIVE_DATABASE=remote\n'
    fi
    printf 'LOCAL_CANDIDATE=%s\n' "$([[ -s "$candidate_env" ]] && printf ready || printf missing)"
    printf 'API_SERVICE=%s\n' "$(systemctl is-active "$api_service" 2>/dev/null || true)"
    printf 'AGENT_SERVICE=%s\n' "$(systemctl is-active "$agent_service" 2>/dev/null || true)"
    ;;

  *) fail invalid_mode ;;
esac
