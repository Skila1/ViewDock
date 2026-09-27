#!/usr/bin/env bash
# Host-side ViewDock updater. The app only writes update/request.
# This script runs on the host: docker compose pull, then docker compose up -d.
set -euo pipefail

export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/snap/bin:${PATH:-}"

if [[ -n "${VD_UPDATE_PREFIX:-}" ]]; then
  PREFIX="${VD_UPDATE_PREFIX}"
elif [[ "$(basename "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)")" == "update" ]]; then
  PREFIX="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
else
  echo "VD_UPDATE_PREFIX is not set" >&2
  exit 1
fi

UPDATE="${PREFIX}/update"
REQ="${UPDATE}/request"
LOG="${UPDATE}/last.log"
APPLIED="${UPDATE}/applied"
PROG="${UPDATE}/progress.json"
mkdir -p "${UPDATE}"

exec 9>"${UPDATE}/.lock"
if command -v flock >/dev/null 2>&1; then
  if ! flock -n 9; then
    echo "update already running" >>"${LOG}"
    exit 0
  fi
fi

progress_write() {
  local percent="$1" stage="$2" detail="$3"
  detail="${detail//$'\r'/}"
  detail="${detail//\\/\\\\}"
  detail="${detail//\"/\\\"}"
  detail="${detail//$'\n'/ }"
  printf '{"percent":%s,"stage":"%s","detail":"%s"}\n' "${percent}" "${stage}" "${detail}" > "${PROG}.tmp"
  mv -f "${PROG}.tmp" "${PROG}"
}

{
  echo "---- $(date -u +%Y-%m-%dT%H:%M:%SZ) ----"
  if [[ ! -f "${REQ}" ]]; then
    echo "no request"
    exit 0
  fi
  rm -f "${REQ}"
  if ! command -v docker >/dev/null 2>&1; then
    progress_write 0 "error" "docker not found on host PATH"
    echo "docker not found"
    exit 127
  fi
  progress_write 5 "queued" "Host received update request"
  sleep 1
  cd "${PREFIX}"
  rm -f docker-compose.gpu.yml docker-compose.local.yml
  if [[ -f .env ]]; then
    if grep -q '^COMPOSE_FILE=' .env; then
      _tmp="$(mktemp)"
      grep -v '^COMPOSE_FILE=' .env > "${_tmp}" || true
      mv "${_tmp}" .env
    fi
    _gpu="$(grep -E '^VD_GPU=' .env | tail -1 | cut -d= -f2- | tr -d '"' | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]' || true)"
    _profile=cpu
    case "${_gpu}" in true|1|yes|on) _profile=gpu ;; esac
    if grep -q '^COMPOSE_PROFILES=' .env; then
      _cur="$(grep -E '^COMPOSE_PROFILES=' .env | tail -1 | cut -d= -f2- | tr -d '"' | tr -d '[:space:]' || true)"
      if [[ "${_cur}" != "${_profile}" ]]; then
        _tmp="$(mktemp)"
        awk -v v="${_profile}" 'index($0, "COMPOSE_PROFILES=") == 1 { print "COMPOSE_PROFILES=" v; next } { print }' .env > "${_tmp}"
        mv "${_tmp}" .env
      fi
    else
      printf '\nCOMPOSE_PROFILES=%s\n' "${_profile}" >> .env
    fi
  fi
  img="ghcr.io/skila1/viewdock:latest"
  if [[ -f docker-compose.yml ]]; then
    from_compose="$(grep -E 'image:[[:space:]]' docker-compose.yml | head -1 | awk '{print $2}' | tr -d '"' || true)"
    if [[ -n "${from_compose}" && "${from_compose}" != *'$'* ]]; then
      img="${from_compose}"
    fi
  fi

  progress_write 10 "pulling" "Pulling ${img}"
  set +e
  docker compose pull >>"${LOG}" 2>&1 &
  pull_pid=$!
  pct=10
  while kill -0 "${pull_pid}" 2>/dev/null; do
    sleep 2
    if (( pct < 72 )); then
      pct=$((pct + 2))
    fi
    last="$(tail -n 1 "${LOG}" 2>/dev/null | tr -d '\r' || true)"
    if [[ "${last}" =~ ([0-9]{1,3})% ]]; then
      mapped=$((10 + BASH_REMATCH[1] * 62 / 100))
      if (( mapped > pct )); then
        pct="${mapped}"
      fi
    fi
    progress_write "${pct}" "pulling" "${last:-Downloading layers}"
  done
  wait "${pull_pid}"
  pull_st=$?
  set -e
  if [[ "${pull_st}" -ne 0 ]]; then
    progress_write 0 "error" "Image pull failed"
    echo "pull failed: ${pull_st}"
    exit "${pull_st}"
  fi

  progress_write 80 "restarting" "Starting updated containers"
  COMPOSE_PROFILES=cpu,gpu docker compose down --remove-orphans --timeout 30 || true
  leftovers="$(docker ps -aq --filter "label=com.docker.compose.project=viewdock" 2>/dev/null || true)"
  if [[ -n "${leftovers}" ]]; then
    # shellcheck disable=SC2086
    docker stop -t 20 ${leftovers} >/dev/null 2>&1 || true
    # shellcheck disable=SC2086
    docker rm -f ${leftovers} >/dev/null 2>&1 || true
  fi
  for _name in viewdock viewdock-gpu viewdock-local; do
    _cid="$(docker ps -aq --filter "name=^/${_name}$" 2>/dev/null || true)"
    if [[ -n "${_cid}" ]]; then
      docker stop -t 20 "${_cid}" >/dev/null 2>&1 || true
      docker rm -f "${_cid}" >/dev/null 2>&1 || true
    fi
  done
  for _net in viewdock_default viewdock; do
    docker network inspect "${_net}" >/dev/null 2>&1 || continue
    _eps="$(docker network inspect "${_net}" -f '{{range $id, $e := .Containers}}{{println $id}}{{end}}' 2>/dev/null || true)"
    if [[ -n "${_eps}" ]]; then
      while IFS= read -r _cid; do
        [[ -z "${_cid}" ]] && continue
        docker stop -t 10 "${_cid}" >/dev/null 2>&1 || true
        docker rm -f "${_cid}" >/dev/null 2>&1 || true
      done <<< "${_eps}"
    fi
    docker network rm "${_net}" >/dev/null 2>&1 || true
  done
  docker compose up -d --remove-orphans
  digest="$(docker image inspect "${img}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
  if [[ -n "${digest}" ]]; then
    printf '%s\n' "${digest}" > "${APPLIED}"
  fi
  progress_write 100 "done" "Update complete"
  echo "done"
} >>"${LOG}" 2>&1
