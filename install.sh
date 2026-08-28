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

# Dialogs talk to the real terminal so curl|bash still works.
ui_yesno() {
  whiptail --backtitle "${BACKTITLE}" --title "$1" --yesno "$2" "${3:-12}" 72 </dev/tty
}

ui_msg() {
  whiptail --backtitle "${BACKTITLE}" --title "$1" --msgbox "$2" "${3:-12}" 72 </dev/tty
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
    echo "Cancelled."
    exit 0
  fi

  if cloudflared_service_present; then
    msg_ok "cloudflared is already installed as a systemd service"
  elif ui_yesno "Cloudflare Tunnel" "Install cloudflared as a systemd service now?\n\nNot Docker. Point the tunnel at http://localhost:8080 (Docker publishes that port on the host)." 13; then
    CFG_CFTOK="$(ui_pass "Tunnel token" "Cloudflare Tunnel token from Zero Trust.")" || exit 0
  fi

  local cfstatus="no"
  if cloudflared_service_present; then
    cfstatus="already installed"
  elif [[ -n "${CFG_CFTOK}" ]]; then
    cfstatus="yes"
  fi
  local summary
  summary="Compose project: ${PREFIX}\nFiles: docker-compose.yml and .env\nHost port: 8080\nMedia: ${CFG_MEDIAHOST}\ncloudflared systemd: ${cfstatus}\n\nFirst visit: create a local administrator in the browser.\nDiscord is set up later under Admin."
  if ! ui_yesno "Ready" "${summary}\n\nStart install?" 16; then
    echo "Cancelled."
    exit 0
  fi
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
  img=""
  if [[ -f .env ]]; then
    img="$(grep -E '^VD_IMAGE=' .env | tail -1 | cut -d= -f2- | tr -d '"' || true)"
  fi
  img="${img:-ghcr.io/skila1/viewdock:latest}"

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

write_compose() {
  mkdir -p "${PREFIX}"
  cat > "${PREFIX}/docker-compose.yml" <<EOF
name: viewdock

services:
  viewdock:
    image: \${VD_IMAGE:-ghcr.io/skila1/viewdock:latest}
    container_name: viewdock
    restart: unless-stopped
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
      - "\${VD_DOCKER_GID:-0}"
    volumes:
      - ./config:/config
      - ./cache:/cache
      - ./transcode:/transcode
      - \${VD_MEDIA_HOST:-./media}:/media:ro
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
EOF
  chmod 0644 "${PREFIX}/docker-compose.yml"
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
  collect_config
  PREFIX="$(prefix_from_here)"
  CFG_MEDIAHOST="${PREFIX}/media"

  install_docker
  mkdir -p "${PREFIX}/config" "${PREFIX}/cache" "${PREFIX}/transcode" "${PREFIX}/media" "${PREFIX}/update"
  chmod 0777 "${PREFIX}/update" || true
  mkdir -p "${CFG_MEDIAHOST}"
  write_compose
  write_cli
  save_installer
  install_update_helper

  local cookie="false"
  if [[ -n "${CFG_CFTOK}" ]] || cloudflared_service_present; then
    cookie="true"
  fi
  local dockergid="0"
  if [[ -S /var/run/docker.sock ]]; then
    dockergid="$(stat -c %g /var/run/docker.sock 2>/dev/null || echo 0)"
  fi
  if [[ -f "${PREFIX}/.env" ]] && grep -q VD_IMAGE "${PREFIX}/.env"; then
    msg_info "Keeping existing ${PREFIX}/.env"
    grep -q '^VD_IMAGE=' "${PREFIX}/.env" || echo "VD_IMAGE=${IMAGE}" >> "${PREFIX}/.env"
    grep -q '^VD_MEDIA_HOST=' "${PREFIX}/.env" || echo "VD_MEDIA_HOST=${CFG_MEDIAHOST}" >> "${PREFIX}/.env"
    grep -q '^VD_DOCKER_GID=' "${PREFIX}/.env" || echo "VD_DOCKER_GID=${dockergid}" >> "${PREFIX}/.env"
    grep -q '^VD_COMPOSE_PROJECT=' "${PREFIX}/.env" || echo "VD_COMPOSE_PROJECT=viewdock" >> "${PREFIX}/.env"
  else
    cat > "${PREFIX}/.env" <<EOF
VD_HTTP_ADDR=:8080
VD_CONFIG_DIR=/config
VD_CACHE_DIR=/cache
VD_TRANSCODE_DIR=/transcode
VD_MEDIA_DIR=/media
VD_LOG_LEVEL=info
VD_COOKIE_SECURE=${cookie}
VD_TRUSTED_PROXIES=127.0.0.1/32,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
VD_IMAGE=${IMAGE}
VD_MEDIA_HOST=${CFG_MEDIAHOST}
VD_PORT=8080
VD_DOCKER_GID=${dockergid}
VD_COMPOSE_PROJECT=viewdock
VD_UPDATE_DIR=/update
PUID=1000
PGID=1000
TZ=UTC
EOF
    chmod 0600 "${PREFIX}/.env"
  fi

  if [[ ! -f "${PREFIX}/docker-compose.yml" || ! -f "${PREFIX}/.env" ]]; then
    msg_err "Failed to write ${PREFIX}/docker-compose.yml and ${PREFIX}/.env"
    exit 1
  fi
  msg_ok "Wrote ${PREFIX}/docker-compose.yml and ${PREFIX}/.env"

  cd "${PREFIX}"
  msg_info "Pulling ${IMAGE} in ${PREFIX}"
  ${COMPOSE} pull
  ${COMPOSE} up -d
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

  local extra=""
  if [[ -n "${CFG_CFTOK}" ]]; then
    extra="\ncloudflared: systemd (sudo systemctl status cloudflared)\nTunnel origin: http://localhost:8080"
  fi
  local done="Compose project: ${PREFIX}\n  docker-compose.yml\n  .env\nHost port: 8080${extra}\n\nOpen http://<this-host>:8080 and create the first administrator.\nFirst-run needs the token from config/setup.token.\n\ncd ${PREFIX}\ndocker compose ps"
  if ui_ok; then
    ui_msg "Installed" "${done}" 18 || true
  fi
  echo
  echo -e " ${BOLD}${BL}ViewDock files are in ${PREFIX}${CL}"
  echo "  ${PREFIX}/docker-compose.yml"
  echo "  ${PREFIX}/.env"
  echo "  Open http://<this-host>:8080 and create the first administrator."
  echo "  First-run token: ${PREFIX}/config/setup.token (not logged, not returned by the API)."
  echo "  Discord is configured in Admin after you log in."
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
  cd "${PREFIX}"
  ${COMPOSE} pull
  ${COMPOSE} up -d
}
cmd_uninstall() {
  need_root
  cd "${PREFIX}"
  ${COMPOSE} down
  if [[ "${1:-}" == "--purge" ]]; then
    ${COMPOSE} down -v
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
