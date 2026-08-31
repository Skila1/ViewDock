#!/usr/bin/env bash
# ViewDock installer. Whiptail TUI (Proxmox helper style). Do not apt install docker.
#
#   sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
#
# After install: sudo viewdock status|update|logs|uninstall|doctor
# Unattended:    sudo env VD_UNATTENDED=1 bash -c "$(curl -fsSL ...)"
#
set -euo pipefail

# Install into ./viewdock under the current directory (or here, if this folder is already named viewdock).
prefix_from_here() {
  local cwd
  cwd="$(pwd -L)"
  if [[ "$(basename "${cwd}")" == "viewdock" ]]; then
    printf '%s\n' "${cwd}"
  else
    printf '%s\n' "${cwd%/}/viewdock"
  fi
}
PREFIX="$(prefix_from_here)"
IMAGE="${VD_IMAGE:-ghcr.io/skila1/viewdock:latest}"
SCRIPT_SRC="${VD_INSTALL_URL:-https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh}"
COMPOSE="docker compose"
CMD="${1:-install}"
BACKTITLE="ViewDock Installer"
export TERM="${TERM:-xterm}"

YW=$'\033[33m'
GN=$'\033[1;92m'
BL=$'\033[36m'
RD=$'\033[1;31m'
CL=$'\033[m'
BOLD=$'\033[1m'

msg_info() { echo -e " ${YW}[*]${CL} $1"; }
msg_ok() { echo -e " ${GN}[ok]${CL} $1"; }
msg_err() { echo -e " ${RD}[err]${CL} $1" >&2; }

header_info() {
  if [[ -e /dev/tty ]]; then
    clear >/dev/tty 2>/dev/null || true
    cat >/dev/tty <<'EOF'

  ██╗   ██╗██╗███████╗██╗    ██╗██████╗  ██████╗  ██████╗██╗  ██╗
  ██║   ██║██║██╔════╝██║    ██║██╔══██╗██╔═══██╗██╔════╝██║ ██╔╝
  ██║   ██║██║█████╗  ██║ █╗ ██║██║  ██║██║   ██║██║     █████╔╝
  ╚██╗ ██╔╝██║██╔══╝  ██║███╗██║██║  ██║██║   ██║██║     ██╔═██╗
   ╚████╔╝ ██║███████╗╚███╔███╔╝██████╔╝╚██████╔╝╚██████╗██║  ██╗
    ╚═══╝  ╚═╝╚══════╝ ╚══╝╚══╝ ╚═════╝  ╚═════╝  ╚═════╝╚═╝  ╚═╝

  Your movies. Your server. Your way.

EOF
  fi
}

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "Run as root (sudo)." >&2
    exit 1
  fi
}

detect_os() {
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    echo "${ID}"
  else
    echo "unknown"
  fi
}

ensure_whiptail() {
  if command -v whiptail >/dev/null 2>&1; then
    return
  fi
  msg_info "Installing whiptail"
  case "$(detect_os)" in
    ubuntu|debian)
      apt-get update -y
      apt-get install -y whiptail ca-certificates curl
      ;;
    fedora|rhel|centos)
      dnf install -y newt ca-certificates curl || yum install -y newt ca-certificates curl
      ;;
    arch)
      pacman -Sy --noconfirm libnewt curl
      ;;
    *)
      msg_err "Install whiptail (newt), then re-run."
      exit 1
      ;;
  esac
  msg_ok "whiptail ready"
}

ui_ok() {
  [[ -e /dev/tty && "${VD_UNATTENDED:-0}" != "1" ]] && command -v whiptail >/dev/null 2>&1
}

# Leave newt/whiptail's alternate screen so the shell is usable again.
ui_restore() {
  [[ -e /dev/tty ]] || return 0
  {
    printf '\033[?1049l\033[?25h\033[0m'
    if command -v tput >/dev/null 2>&1; then
      tput rmcup || true
      tput sgr0 || true
      tput cnorm || true
    fi
    stty sane < /dev/tty || true
    clear
  } >/dev/tty 2>/dev/null || true
}

# Dialogs talk to the real terminal so curl|bash still works.
ui_yesno() {
  whiptail --backtitle "${BACKTITLE}" --title "$1" --yesno "$2" "${3:-12}" 72 </dev/tty >/dev/tty
}

ui_msg() {
  whiptail --backtitle "${BACKTITLE}" --title "$1" --msgbox "$2" "${3:-12}" 72 </dev/tty >/dev/tty
}

ui_input() {
  local out
  out=$(whiptail --backtitle "${BACKTITLE}" --title "$1" --inputbox "$2" 12 72 "$3" 3>&1 1>&2 2>&3 </dev/tty) || return 1
  printf '%s' "${out}"
}

ui_pass() {
  local out
  out=$(whiptail --backtitle "${BACKTITLE}" --title "$1" --passwordbox "$2" 12 72 3>&1 1>&2 2>&3 </dev/tty) || return 1
  printf '%s' "${out}"
}

install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    systemctl enable --now docker 2>/dev/null || true
    msg_ok "Docker already installed"
    return
  fi
  msg_info "Installing Docker Engine + Compose plugin"
  local os
  os="$(detect_os)"
  case "$os" in
    ubuntu|debian)
      apt-get update -y
      apt-get install -y ca-certificates curl
      curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
      sh /tmp/get-docker.sh
      rm -f /tmp/get-docker.sh
      ;;
    fedora|rhel|centos)
      dnf install -y docker docker-compose-plugin || yum install -y docker
      systemctl enable --now docker
      ;;
    arch)
      pacman -Sy --noconfirm docker docker-compose
      systemctl enable --now docker
      ;;
    *)
      msg_err "Install Docker Engine with the Compose plugin, then re-run."
      exit 1
      ;;
  esac
  systemctl enable --now docker 2>/dev/null || true
  if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    msg_err "Docker Engine or the Compose plugin is missing after install."
    exit 1
  fi
  msg_ok "Docker installed"
}

install_cloudflared_pkg() {
  if command -v cloudflared >/dev/null 2>&1; then
    return
  fi
  msg_info "Installing cloudflared"
  local os arch deb rpm bin
  os="$(detect_os)"
  arch="$(uname -m)"
  case "$arch" in
    aarch64|arm64) deb="cloudflared-linux-arm64.deb"; rpm="cloudflared-linux-aarch64.rpm"; bin="cloudflared-linux-arm64" ;;
    *) deb="cloudflared-linux-amd64.deb"; rpm="cloudflared-linux-x86_64.rpm"; bin="cloudflared-linux-amd64" ;;
  esac
  case "$os" in
    ubuntu|debian)
      curl -fsSL -o /tmp/cloudflared.deb "https://github.com/cloudflare/cloudflared/releases/latest/download/${deb}"
      dpkg -i /tmp/cloudflared.deb || apt-get install -y -f
      rm -f /tmp/cloudflared.deb
      ;;
    fedora|rhel|centos)
      curl -fsSL -o /tmp/cloudflared.rpm "https://github.com/cloudflare/cloudflared/releases/latest/download/${rpm}"
      rpm -i /tmp/cloudflared.rpm || dnf install -y /tmp/cloudflared.rpm || yum install -y /tmp/cloudflared.rpm
      rm -f /tmp/cloudflared.rpm
      ;;
    *)
      curl -fsSL -o /usr/local/bin/cloudflared "https://github.com/cloudflare/cloudflared/releases/latest/download/${bin}"
      chmod 0755 /usr/local/bin/cloudflared
      ;;
  esac
  if ! command -v cloudflared >/dev/null 2>&1; then
    msg_err "cloudflared is not on PATH after install."
    exit 1
  fi
  msg_ok "cloudflared installed"
}

install_cloudflared_service() {
  local token="${1:-}"
  if cloudflared_service_present; then
    msg_ok "cloudflared systemd service already exists, leaving it as-is"
    return
  fi
  if [[ -z "${token}" ]]; then
    return
  fi
  install_cloudflared_pkg
  msg_info "Installing cloudflared systemd service"
  cloudflared service install "${token}"
  systemctl enable --now cloudflared
  msg_ok "cloudflared is running as a systemd service (origin: http://localhost:8080)"
}

cloudflared_service_present() {
  command -v systemctl >/dev/null 2>&1 || return 1
  if systemctl is-active --quiet cloudflared 2>/dev/null; then
    return 0
  fi
  if systemctl is-enabled --quiet cloudflared 2>/dev/null; then
    return 0
  fi
  systemctl list-unit-files --type=service --no-legend 2>/dev/null | grep -q '^cloudflared\.service'
}

# Filled by collect_config
CFG_MEDIAHOST=""
CFG_CFTOK=""

collect_config() {
  PREFIX="$(prefix_from_here)"
  CFG_MEDIAHOST="${PREFIX}/media"
  CFG_CFTOK="${CLOUDFLARE_TUNNEL_TOKEN:-}"

  if ! ui_ok; then
    msg_info "No TUI (set VD_UNATTENDED=1 or no /dev/tty). Installing in ${PREFIX}"
    return
  fi

  ui_msg "ViewDock" "Use Tab and Enter to move.\n\nInstalls into a viewdock folder in this directory.\nIf you are already in a folder named viewdock, it installs here.\nDocker publishes port 8080. Cloudflare Tunnel (optional) is a systemd service. Discord is configured later in Admin." 14 || true

  if ! ui_yesno "Install ViewDock" "Install here:\n${PREFIX}\n\nWrites docker-compose.yml and .env.\nPort 8080 on the host.\nThen: cd ${PREFIX} && docker compose ps"; then
    ui_restore
    echo "Cancelled."
    exit 0
  fi

  if cloudflared_service_present; then
    msg_ok "cloudflared is already installed as a systemd service"
  elif ui_yesno "Cloudflare Tunnel" "Install cloudflared as a systemd service now?\n\nNot Docker. Point the tunnel at http://localhost:8080 (Docker publishes that port on the host)." 13; then
    CFG_CFTOK="$(ui_pass "Tunnel token" "Cloudflare Tunnel token from Zero Trust.")" || {
      ui_restore
      echo "Cancelled."
      exit 0
    }
  fi

  local cfstatus="no"
  if cloudflared_service_present; then
    cfstatus="already installed"
  elif [[ -n "${CFG_CFTOK}" ]]; then
    cfstatus="yes"
  fi
  local summary
  summary="Compose project: ${PREFIX}\nFiles: docker-compose.yml and .env\nHost port: 8080 (change VD_PORT in .env if you want)\nMedia: ${CFG_MEDIAHOST}\ncloudflared systemd: ${cfstatus}\n\nFirst visit: create a local administrator in the browser.\nPublic URL, Discord, and TMDB are set in Admin."
  if ! ui_yesno "Ready" "${summary}\n\nStart install?" 16; then
    ui_restore
    echo "Cancelled."
    exit 0
  fi
  ui_restore
}

write_cli() {
  cat > /usr/local/bin/viewdock <<EOF
#!/usr/bin/env bash
set -euo pipefail
PREFIX="${PREFIX}"
COMPOSE="docker compose"
need_root() {
  if [[ "\${EUID}" -ne 0 ]]; then
    echo "Run as root (sudo viewdock ...)." >&2
    exit 1
  fi
}
need_install() {
  if [[ ! -f "\${PREFIX}/docker-compose.yml" ]]; then
    echo "ViewDock is not installed. Run:" >&2
    echo '  sudo bash -c "\$(curl -fsSL ${SCRIPT_SRC})"' >&2
    exit 1
  fi
}
cd_prefix() { need_install; cd "\${PREFIX}"; }
cmd="\${1:-}"
case "\$cmd" in
  status) cd_prefix; \${COMPOSE} ps ;;
  logs) cd_prefix; \${COMPOSE} logs -f --tail=200 ;;
	update)
    if [[ -f "\${PREFIX}/install.sh" ]] && grep -q 'ensure_compose()' "\${PREFIX}/install.sh"; then
      exec bash "\${PREFIX}/install.sh" update
    fi
    cd_prefix
    \${COMPOSE} pull
    \${COMPOSE} up -d --remove-orphans
    ;;
  uninstall)
    need_root
    cd_prefix
    if [[ "\${2:-}" == "--purge" ]]; then
      \${COMPOSE} down -v
      rm -rf "\${PREFIX}"
      rm -f /usr/local/bin/viewdock
      systemctl disable --now viewdock-update.path >/dev/null 2>&1 || true
      systemctl disable --now viewdock-update.timer >/dev/null 2>&1 || true
      rm -f /etc/systemd/system/viewdock-update.service /etc/systemd/system/viewdock-update.path /etc/systemd/system/viewdock-update.timer /usr/local/lib/viewdock/update.sh /usr/local/lib/viewdock/host-update.sh
      systemctl daemon-reload >/dev/null 2>&1 || true
    else
      \${COMPOSE} down
      echo "Data kept in \${PREFIX}/config (pass --purge to delete)."
    fi
    ;;
  doctor)
    command -v docker
    docker compose version
    curl -fsS http://127.0.0.1:8080/healthz || true
    echo
    if [[ -f "\${PREFIX}/docker-compose.yml" ]]; then
      cd "\${PREFIX}" && \${COMPOSE} ps
    else
      echo "Missing \${PREFIX}/docker-compose.yml"
    fi
    if command -v cloudflared >/dev/null 2>&1; then
      systemctl is-active cloudflared || true
    fi
    if command -v systemctl >/dev/null 2>&1; then
      systemctl is-enabled viewdock-update.path 2>/dev/null || true
      systemctl is-active viewdock-update.path 2>/dev/null || true
    fi
    ;;
  install)
    echo "Re-run the installer:"
    echo '  sudo bash -c "\$(curl -fsSL ${SCRIPT_SRC})"'
    ;;
  *)
    echo "Usage: sudo viewdock status|update|logs|uninstall|doctor"
    echo "Or:    cd ${PREFIX} && docker compose ps|logs|down|up -d"
    exit 1
    ;;
esac
EOF
  chmod 0755 /usr/local/bin/viewdock
}

install_update_helper() {
  need_root
  mkdir -p "${PREFIX}/update" /usr/local/lib/viewdock
  chmod 0777 "${PREFIX}/update" || true
  printf '1\n' > "${PREFIX}/update/helper"
  cat > /usr/local/lib/viewdock/host-update.sh <<'HOST'
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
  docker compose up -d --remove-orphans
  digest="$(docker image inspect "${img}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
  if [[ -n "${digest}" ]]; then
    printf '%s\n' "${digest}" > "${APPLIED}"
  fi
  progress_write 100 "done" "Update complete"
  echo "done"
} >>"${LOG}" 2>&1
HOST
  chmod 0755 /usr/local/lib/viewdock/host-update.sh
  cat > /usr/local/lib/viewdock/update.sh <<EOF
#!/usr/bin/env bash
set -euo pipefail
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/snap/bin:\${PATH:-}"
export VD_UPDATE_PREFIX="${PREFIX}"
if [[ -f "${PREFIX}/update/run.sh" ]]; then
  exec /bin/bash "${PREFIX}/update/run.sh"
fi
exec /bin/bash /usr/local/lib/viewdock/host-update.sh
EOF
  chmod 0755 /usr/local/lib/viewdock/update.sh
  cat > /etc/systemd/system/viewdock-update.service <<EOF
[Unit]
Description=ViewDock host image update
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
WorkingDirectory=${PREFIX}
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/snap/bin
Environment=VD_UPDATE_PREFIX=${PREFIX}
ExecStart=/usr/local/lib/viewdock/update.sh
Nice=5
TimeoutStartSec=30min
EOF
  cat > /etc/systemd/system/viewdock-update.path <<EOF
[Unit]
Description=Watch for ViewDock update requests

[Path]
PathExists=${PREFIX}/update/request
PathChanged=${PREFIX}/update/request
PathModified=${PREFIX}/update/request
Unit=viewdock-update.service
MakeDirectory=true

[Install]
WantedBy=multi-user.target
EOF
  cat > /etc/systemd/system/viewdock-update.timer <<EOF
[Unit]
Description=Poll ViewDock update requests (inotify misses container bind-mount writes)

[Timer]
OnBootSec=20s
OnUnitInactiveSec=5s
AccuracySec=1s
Unit=viewdock-update.service

[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now viewdock-update.path
  systemctl enable --now viewdock-update.timer
  msg_ok "Host update helper enabled (viewdock-update.path + timer)"
}

remove_update_helper() {
  if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now viewdock-update.path >/dev/null 2>&1 || true
    systemctl disable --now viewdock-update.timer >/dev/null 2>&1 || true
    systemctl stop viewdock-update.service >/dev/null 2>&1 || true
  fi
  rm -f /etc/systemd/system/viewdock-update.service /etc/systemd/system/viewdock-update.path /etc/systemd/system/viewdock-update.timer /usr/local/lib/viewdock/update.sh /usr/local/lib/viewdock/host-update.sh
  if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
  fi
}

docker_sock_gid() {
  if [[ -S /var/run/docker.sock ]]; then
    stat -c %g /var/run/docker.sock 2>/dev/null || echo 0
  else
    echo 0
  fi
}

env_get() {
  local key="$1"
  [[ -f "${PREFIX}/.env" ]] || return 1
  local line
  line="$(grep -E "^${key}=" "${PREFIX}/.env" 2>/dev/null | tail -1 || true)"
  [[ -n "${line}" ]] || return 1
  printf '%s\n' "${line#*=}" | tr -d '"' | tr -d '\r'
}

env_has() {
  [[ -f "${PREFIX}/.env" ]] && grep -q "^${1}=" "${PREFIX}/.env"
}

env_gpu_truthy() {
  local v
  v="$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
  case "${v}" in
    true|1|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

# Append KEY=VAL only when the key is missing. Never changes an existing value.
env_ensure_key() {
  local key="$1" val="$2" envf="${PREFIX}/.env"
  [[ -f "${envf}" ]] || return 0
  if grep -q "^${key}=" "${envf}"; then
    return 0
  fi
  if [[ -s "${envf}" ]] && [[ "$(tail -c1 "${envf}" | wc -c)" -gt 0 ]]; then
    printf '\n' >> "${envf}"
  fi
  printf '%s=%s\n' "${key}" "${val}" >> "${envf}"
  chmod 0600 "${envf}" || true
  msg_ok "Added ${key} to .env"
}

# Derived keys only (COMPOSE_PROFILES). User-facing values go through env_ensure_key.
env_set_key() {
  local key="$1" val="$2" envf="${PREFIX}/.env"
  [[ -f "${envf}" ]] || return 0
  local tmp
  tmp="$(mktemp)"
  if grep -q "^${key}=" "${envf}"; then
    awk -v k="${key}" -v v="${val}" 'index($0, k "=") == 1 { print k "=" v; next } { print }' "${envf}" > "${tmp}"
  else
    cat "${envf}" > "${tmp}"
    if [[ -s "${tmp}" ]] && [[ "$(tail -c1 "${tmp}" | wc -c)" -gt 0 ]]; then
      printf '\n' >> "${tmp}"
    fi
    printf '%s=%s\n' "${key}" "${val}" >> "${tmp}"
  fi
  mv "${tmp}" "${envf}"
  chmod 0600 "${envf}" || true
}

env_sync_profiles() {
  local profile="cpu"
  if env_has VD_GPU && env_gpu_truthy "$(env_get VD_GPU || true)"; then
    profile="gpu"
  fi
  if env_has COMPOSE_PROFILES; then
    local cur
    cur="$(env_get COMPOSE_PROFILES || true)"
    if [[ "${cur}" != "${profile}" ]]; then
      env_set_key COMPOSE_PROFILES "${profile}"
      msg_ok "Set COMPOSE_PROFILES=${profile} to match VD_GPU"
    fi
  else
    env_ensure_key COMPOSE_PROFILES "${profile}"
  fi
}

ensure_env_defaults() {
  if [[ ! -f "${PREFIX}/.env" ]]; then
    cat > "${PREFIX}/.env" <<EOF
# Host port. The container always listens on 8080.
VD_PORT=8080

# Public origin for Discord OAuth and share links. Example: https://app.viewdock.dev
# You can also set this later under Admin → Settings.
VD_PUBLIC_URL=

EOF
    chmod 0600 "${PREFIX}/.env"
    return 0
  fi
  msg_info "Keeping existing ${PREFIX}/.env (adding missing keys only)"
  env_ensure_key VD_PORT 8080
  env_ensure_key VD_PUBLIC_URL ""
}

# Older installs used docker-compose.gpu.yml / docker-compose.local.yml / COMPOSE_FILE.
# Fold that intent into .env, then drop the extra files. Existing VD_* values stay.
migrate_legacy_install() {
  local envf="${PREFIX}/.env"
  local had_gpu=0

  if [[ -f "${PREFIX}/docker-compose.gpu.yml" ]]; then
    had_gpu=1
    msg_info "Detected older docker-compose.gpu.yml"
  fi
  if [[ -f "${envf}" ]] && grep -qE '^COMPOSE_FILE=.*gpu' "${envf}"; then
    had_gpu=1
    msg_info "Detected older COMPOSE_FILE GPU overlay"
  fi
  if [[ "${had_gpu}" -eq 1 ]] && ! env_has VD_GPU; then
    env_ensure_key VD_GPU true
  fi

  if [[ -f "${PREFIX}/docker-compose.local.yml" ]]; then
    msg_info "Detected older docker-compose.local.yml"
    if ! env_has VD_IMAGE && grep -qE 'image:[[:space:]]*viewdock:local' "${PREFIX}/docker-compose.local.yml"; then
      env_ensure_key VD_IMAGE viewdock:local
    fi
    if ! env_has VD_RESTART && grep -qE 'restart:[[:space:]]*"?no"?' "${PREFIX}/docker-compose.local.yml"; then
      env_ensure_key VD_RESTART no
    fi
  fi

  if [[ -f "${envf}" ]] && grep -q '^COMPOSE_FILE=' "${envf}"; then
    local tmp
    tmp="$(mktemp)"
    grep -v '^COMPOSE_FILE=' "${envf}" > "${tmp}" || true
    mv "${tmp}" "${envf}"
    chmod 0600 "${envf}" || true
    msg_ok "Removed obsolete COMPOSE_FILE from .env"
  fi

  if [[ -f "${PREFIX}/docker-compose.gpu.yml" || -f "${PREFIX}/docker-compose.local.yml" ]]; then
    rm -f "${PREFIX}/docker-compose.gpu.yml" "${PREFIX}/docker-compose.local.yml"
    msg_ok "Removed leftover compose overlay files"
  fi
}

compose_is_current() {
  local f="${PREFIX}/docker-compose.yml"
  [[ -f "${f}" ]] || return 1
  grep -q 'viewdock-gpu:' "${f}" || return 1
  grep -q 'profiles: \["cpu"\]' "${f}" || return 1
  grep -q 'profiles: \["gpu"\]' "${f}" || return 1
  grep -q 'gpus: all' "${f}" || return 1
}

write_compose() {
  local dockergid="${1:-0}"
  mkdir -p "${PREFIX}"
  cat > "${PREFIX}/docker-compose.yml" <<EOF
# GPU is selected from .env (VD_GPU=true|false). Compose profiles follow that flag:
#   VD_GPU=false  →  COMPOSE_PROFILES=cpu
#   VD_GPU=true   →  COMPOSE_PROFILES=gpu
name: viewdock

x-viewdock: &viewdock
  image: \${VD_IMAGE:-ghcr.io/skila1/viewdock:latest}
  container_name: viewdock
  restart: \${VD_RESTART:-unless-stopped}
  stop_grace_period: 60s
  env_file: [.env]
  environment:
    VD_HTTP_ADDR: ":8080"
    VD_CONFIG_DIR: /config
    VD_CACHE_DIR: /cache
    VD_TRANSCODE_DIR: /transcode
    VD_MEDIA_DIR: /media
    VD_COMPOSE_PROJECT: viewdock
    VD_UPDATE_DIR: /update
  group_add:
    - "${dockergid}"
  volumes:
    - ./config:/config
    - ./cache:/cache
    - ./transcode:/transcode
    - ./media:/media
    - ./update:/update
    - /var/run/docker.sock:/var/run/docker.sock
  ports:
    - "\${VD_PORT:-8080}:8080"
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
    interval: 10s
    timeout: 5s
    retries: 8
    start_period: 25s

services:
  viewdock:
    <<: *viewdock
    profiles: ["cpu"]

  viewdock-gpu:
    <<: *viewdock
    profiles: ["gpu"]
    gpus: all
    environment:
      VD_HTTP_ADDR: ":8080"
      VD_CONFIG_DIR: /config
      VD_CACHE_DIR: /cache
      VD_TRANSCODE_DIR: /transcode
      VD_MEDIA_DIR: /media
      VD_COMPOSE_PROJECT: viewdock
      VD_UPDATE_DIR: /update
      NVIDIA_VISIBLE_DEVICES: all
      NVIDIA_DRIVER_CAPABILITIES: compute,video,utility
EOF
  chmod 0644 "${PREFIX}/docker-compose.yml"
}

# The cpu and gpu services share container_name: viewdock. A profile switch or
# an upgrade from the old single-service file leaves that name (and the project
# network) held by the previous container. Compose down with only one profile
# enabled will not stop the other service.
clear_viewdock_runtime() {
  cd "${PREFIX}" || return 0
  command -v docker >/dev/null 2>&1 || return 0
  msg_info "Stopping existing ViewDock containers"

  COMPOSE_PROFILES=cpu,gpu ${COMPOSE} down --remove-orphans --timeout 30 || true
  ${COMPOSE} down --remove-orphans --timeout 30 || true

  local ids name cid net
  ids="$(docker ps -aq --filter "label=com.docker.compose.project=viewdock" 2>/dev/null || true)"
  if [[ -n "${ids}" ]]; then
    msg_info "Removing leftover compose project containers"
    # shellcheck disable=SC2086
    docker stop -t 20 ${ids} >/dev/null 2>&1 || true
    # shellcheck disable=SC2086
    docker rm -f ${ids} >/dev/null 2>&1 || true
  fi

  for name in viewdock viewdock-gpu viewdock-local; do
    cid="$(docker ps -aq --filter "name=^/${name}$" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      msg_info "Removing leftover container ${name}"
      docker stop -t 20 "${cid}" >/dev/null 2>&1 || true
      docker rm -f "${cid}" >/dev/null 2>&1 || true
    fi
  done

  for net in viewdock_default viewdock; do
    docker network inspect "${net}" >/dev/null 2>&1 || continue
    ids="$(docker network inspect "${net}" -f '{{range $id, $e := .Containers}}{{println $id}}{{end}}' 2>/dev/null || true)"
    if [[ -n "${ids}" ]]; then
      msg_info "Network ${net} still has active endpoints"
      while IFS= read -r cid; do
        [[ -z "${cid}" ]] && continue
        name="$(docker inspect -f '{{.Name}}' "${cid}" 2>/dev/null || echo "${cid}")"
        msg_info "Stopping ${name} so ${net} can close"
        docker stop -t 10 "${cid}" >/dev/null 2>&1 || true
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done <<< "${ids}"
    fi
    if docker network rm "${net}" >/dev/null 2>&1; then
      msg_ok "Removed leftover network ${net}"
    fi
  done

  COMPOSE_PROFILES=cpu,gpu ${COMPOSE} down --remove-orphans --timeout 15 || true
}

compose_recreate() {
  cd "${PREFIX}"
  clear_viewdock_runtime
  msg_info "Pulling ${IMAGE} in ${PREFIX}"
  ${COMPOSE} pull
  if ${COMPOSE} up -d --remove-orphans; then
    return 0
  fi
  msg_info "Compose up failed; clearing leftovers and retrying"
  clear_viewdock_runtime
  ${COMPOSE} up -d --remove-orphans
}

# Write compose only when missing or still on the old single-service / overlay layout.
# An already-migrated file is left alone (custom volumes, image, group_add stay).
ensure_compose() {
  local dockergid="${1:-0}"
  mkdir -p "${PREFIX}"
  if compose_is_current; then
    msg_info "Keeping existing ${PREFIX}/docker-compose.yml"
    return 0
  fi
  if [[ -f "${PREFIX}/docker-compose.yml" ]]; then
    local oldgid
    oldgid="$(grep -A2 'group_add:' "${PREFIX}/docker-compose.yml" | grep -oE '[0-9]+' | head -1 || true)"
    if [[ -n "${oldgid}" ]]; then
      dockergid="${oldgid}"
    fi
    cp -a "${PREFIX}/docker-compose.yml" "${PREFIX}/docker-compose.yml.bak"
    msg_info "Older docker-compose.yml detected; upgrading to the GPU-profile layout (backup: docker-compose.yml.bak)"
  fi
  write_compose "${dockergid}"
}

# Detect NVIDIA GPU + Docker runtime. Never installs drivers. Never fails install.
# Never overwrites an existing VD_GPU.
detect_nvidia_docker() {
  local gpu_hint=0
  local runtime_ok=0
  local info=""

  if command -v nvidia-smi >/dev/null 2>&1 || [[ -e /dev/nvidia0 ]]; then
    gpu_hint=1
  fi

  if command -v docker >/dev/null 2>&1; then
    info="$(docker info 2>/dev/null || true)"
    if printf '%s\n' "${info}" | grep -qi 'nvidia'; then
      runtime_ok=1
    fi
  fi

  if env_has VD_GPU; then
    if [[ "${gpu_hint}" -eq 1 && "${runtime_ok}" -eq 0 ]] && ! env_gpu_truthy "$(env_get VD_GPU || true)"; then
      echo "NVIDIA GPU detected but Docker GPU runtime is unavailable. ViewDock will use CPU transcoding."
    fi
  else
    if [[ "${gpu_hint}" -eq 1 && "${runtime_ok}" -eq 0 ]]; then
      echo "NVIDIA GPU detected but Docker GPU runtime is unavailable. ViewDock will use CPU transcoding."
      env_ensure_key VD_GPU false
    elif [[ "${runtime_ok}" -eq 1 ]]; then
      env_ensure_key VD_GPU true
      msg_ok "NVIDIA Docker runtime detected; VD_GPU=true"
    else
      env_ensure_key VD_GPU false
    fi
  fi
  env_sync_profiles
  return 0
}

save_installer() {
  local src="${BASH_SOURCE[0]:-}"
  if [[ -n "${src}" && -f "${src}" && "${src}" != *bash ]]; then
    cp "${src}" "${PREFIX}/install.sh"
  else
    curl -fsSL "${SCRIPT_SRC}" -o "${PREFIX}/install.sh" || true
  fi
  if [[ -f "${PREFIX}/install.sh" ]]; then
    chmod 0755 "${PREFIX}/install.sh"
  fi
}

cmd_install() {
  need_root
  header_info
  ensure_whiptail
  if ui_ok; then
    trap 'ui_restore; exit 130' INT TERM
  fi
  collect_config
  trap - INT TERM
  PREFIX="$(prefix_from_here)"
  CFG_MEDIAHOST="${PREFIX}/media"

  install_docker
  mkdir -p "${PREFIX}/config" "${PREFIX}/config/uploads" "${PREFIX}/cache" "${PREFIX}/transcode" "${PREFIX}/media" "${PREFIX}/update"
  chmod 0777 "${PREFIX}/update" "${PREFIX}/media" "${PREFIX}/config/uploads" || true
  chown 1000:1000 "${PREFIX}/media" "${PREFIX}/config/uploads" 2>/dev/null || true
  mkdir -p "${CFG_MEDIAHOST}"
  local dockergid
  dockergid="$(docker_sock_gid)"
  migrate_legacy_install
  ensure_env_defaults
  detect_nvidia_docker
  ensure_compose "${dockergid}"
  write_cli
  save_installer
  install_update_helper

  if [[ ! -f "${PREFIX}/docker-compose.yml" || ! -f "${PREFIX}/.env" ]]; then
    msg_err "Failed to write ${PREFIX}/docker-compose.yml and ${PREFIX}/.env"
    exit 1
  fi
  msg_ok "Compose project ready in ${PREFIX}"

  compose_recreate
  digest="$(docker image inspect "${IMAGE}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
  if [[ -n "${digest}" ]]; then
    printf '%s\n' "${digest}" > "${PREFIX}/update/applied"
  fi
  msg_ok "ViewDock is starting"

  local i
  for i in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      break
    fi
    sleep 2
  done

  install_cloudflared_service "${CFG_CFTOK}"

  echo
  echo -e " ${BOLD}${BL}ViewDock files are in ${PREFIX}${CL}"
  echo "  ${PREFIX}/docker-compose.yml"
  echo "  ${PREFIX}/.env"
  if env_has VD_GPU; then
    echo "  GPU: VD_GPU=$(env_get VD_GPU || echo false)"
  fi
  echo "  Open http://<this-host>:8080 and create the first administrator."
  echo "  First-run token: printed in docker compose logs after ViewDock starts (8 characters)."
  echo "  Public URL: ${PREFIX}/.env (VD_PUBLIC_URL) or Admin → Settings after login."
  echo "  Discord and TMDB are configured in Admin."
  if [[ -n "${CFG_CFTOK}" ]]; then
    echo "  cloudflared: systemctl status cloudflared"
  fi
  echo
  echo "  cd ${PREFIX}"
  echo "  docker compose ps"
  echo "  docker compose logs -f"
  echo "  docker compose down"
  echo "  docker compose pull && docker compose up -d"
  echo
  echo "  Optional helper: sudo viewdock status|update|logs|doctor|uninstall"
}

cmd_status() { [[ -f "${PREFIX}/docker-compose.yml" ]] || { echo "Not installed." >&2; exit 1; }; cd "${PREFIX}" && ${COMPOSE} ps; }
cmd_logs() { cd "${PREFIX}" && ${COMPOSE} logs -f --tail=200; }
cmd_update() {
  local dockergid
  dockergid="$(docker_sock_gid)"
  migrate_legacy_install
  ensure_env_defaults
  detect_nvidia_docker
  ensure_compose "${dockergid}"
  compose_recreate
}
cmd_uninstall() {
  need_root
  cd "${PREFIX}"
  clear_viewdock_runtime
  if [[ "${1:-}" == "--purge" ]]; then
    COMPOSE_PROFILES=cpu,gpu ${COMPOSE} down -v --remove-orphans || true
    rm -rf "${PREFIX}"
    rm -f /usr/local/bin/viewdock
    remove_update_helper
  else
    echo "Data kept in ${PREFIX}/config (pass --purge to delete)."
  fi
}
cmd_doctor() {
  command -v docker
  docker compose version
  curl -fsS http://127.0.0.1:8080/healthz || true
  echo
  if [[ -d "${PREFIX}/config" ]]; then
    mkdir -p "${PREFIX}/config/uploads" "${PREFIX}/media"
    if [[ ! -w "${PREFIX}/config/uploads" ]]; then
      echo "upload staging not writable: ${PREFIX}/config/uploads"
    else
      echo "upload staging ok: ${PREFIX}/config/uploads"
    fi
    if [[ ! -w "${PREFIX}/media" ]]; then
      echo "media folder not writable: ${PREFIX}/media (Admin uploads need a read-write /media mount)"
    else
      echo "media folder writable: ${PREFIX}/media"
    fi
    if grep -q '/media:ro' "${PREFIX}/docker-compose.yml" 2>/dev/null; then
      echo "docker-compose.yml still mounts /media:ro — change to /media so uploads can write"
    fi
  fi
  echo
  if [[ -f "${PREFIX}/docker-compose.yml" ]]; then
    cd "${PREFIX}" && ${COMPOSE} ps
  else
    echo "Missing ${PREFIX}/docker-compose.yml"
  fi
  if command -v cloudflared >/dev/null 2>&1; then
    systemctl is-active cloudflared || true
  fi
  if command -v systemctl >/dev/null 2>&1; then
    systemctl is-enabled viewdock-update.path 2>/dev/null || echo "viewdock-update.path: not enabled"
    systemctl is-enabled viewdock-update.timer 2>/dev/null || echo "viewdock-update.timer: not enabled"
  fi
}

case "$CMD" in
  install) cmd_install ;;
  status) cmd_status ;;
  update) cmd_update ;;
  uninstall) cmd_uninstall "${2:-}" ;;
  logs) cmd_logs ;;
  doctor) cmd_doctor ;;
  *) echo "Usage: $0 install|status|update|logs|uninstall|doctor" ;;
esac
