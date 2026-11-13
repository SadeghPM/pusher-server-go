#!/bin/bash
set -euo pipefail

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

die() {
  echo -e "${RED}Error: $*${NC}" >&2
  exit 1
}

echo -e "${GREEN}Starting Pusher Clone Installation...${NC}"

# 1. Check for root
if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  die "Please run as root (use sudo)"
fi

# When the script is piped through curl, stdin is the script itself. Read
# prompts from the controlling terminal on a separate fd; reassigning stdin
# would make bash continue reading the script from the terminal.
if [ -t 0 ]; then
  PROMPT_FD=0
elif exec 3</dev/tty 2>/dev/null; then
  PROMPT_FD=3
else
  die "This installer requires an interactive terminal."
fi

# 2. Determine OS and Architecture
OS="$(uname -s)"
if [ "${OS}" != "Linux" ]; then
  die "This installer supports Linux only. Build from source for other platforms."
fi

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64) ARCH="x86_64" ;;
  *)
    die "Prebuilt releases are currently available for Linux x86_64 only. Build from source for ${ARCH}."
    ;;
esac

REPO="SadeghPM/pusher-server-go"
INSTALL_DIR="/opt/pusher-clone"
USER="pusher-clone"
GROUP="pusher-clone"
SERVICE_NAME="pusher-clone"
CONFIG_FILE="${INSTALL_DIR}/config.yaml"

# 3. Stop an existing installation so port checks do not see our own service.
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "${SERVICE_NAME}.service" 2>/dev/null; then
  echo -e "${YELLOW}Stopping the existing ${SERVICE_NAME} service...${NC}"
  systemctl stop "${SERVICE_NAME}.service"
fi

# 4. Create user and group
if ! getent group "${GROUP}" >/dev/null 2>&1; then
  groupadd --system "${GROUP}" 2>/dev/null || true
fi
if ! getent passwd "${USER}" >/dev/null 2>&1; then
  useradd --system --gid "${GROUP}" --no-create-home --shell /bin/false "${USER}" 2>/dev/null || true
fi

# 5. Create directories
mkdir -p "${INSTALL_DIR}"

# 6. Download and Extract
echo -e "${YELLOW}Fetching latest release info...${NC}"
RELEASES_JSON="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" || true)"
LATEST_RELEASE_URL="$(printf '%s' "${RELEASES_JSON}" | grep -o "https://[^\"]*${OS}_${ARCH}\\.tar\\.gz" | head -n 1 || true)"

if [ -n "${LATEST_RELEASE_URL}" ]; then
  echo -e "${GREEN}Downloading from ${LATEST_RELEASE_URL}...${NC}"
  TMP_ARCHIVE="$(mktemp)"
  trap 'rm -f "${TMP_ARCHIVE:-}"' EXIT
  curl -fL "${LATEST_RELEASE_URL}" -o "${TMP_ARCHIVE}" || die "Failed to download the release archive."
  tar -xzf "${TMP_ARCHIVE}" -C "${INSTALL_DIR}"
  rm -f "${TMP_ARCHIVE}"
  trap - EXIT
else
  echo -e "${YELLOW}No release archive found; looking for a locally built binary...${NC}"
  if [ -f "./pusher-clone" ]; then
    install -m 0755 "./pusher-clone" "${INSTALL_DIR}/pusher-clone"
  elif [ -f "./pusher-server-go" ]; then
    install -m 0755 "./pusher-server-go" "${INSTALL_DIR}/pusher-clone"
  else
    die "pusher-clone binary not found. Compile it first with 'go build .'."
  fi
fi

chmod +x "${INSTALL_DIR}/pusher-clone"

# 7. Generate YAML config
echo ""
echo -e "${GREEN}--- Configuration ---${NC}"

WRITE_CONFIG=1
if [ -f "${CONFIG_FILE}" ]; then
  echo -e "${YELLOW}An existing config was found at ${CONFIG_FILE}.${NC}"
  read -r -u "${PROMPT_FD}" -p "Overwrite it? A backup will be kept. [y/N]: " OVERWRITE
  case "${OVERWRITE}" in
    y|Y) cp -p "${CONFIG_FILE}" "${CONFIG_FILE}.bak.$(date +%Y%m%d%H%M%S)" ;;
    *) WRITE_CONFIG=0 ;;
  esac
fi

if [ "${WRITE_CONFIG}" -eq 1 ]; then
  check_port() {
    local port=$1
    if command -v ss >/dev/null 2>&1; then
      if ss -tuln | awk '{print $5}' | grep -q -E ":${port}$"; then return 1; fi
    elif command -v netstat >/dev/null 2>&1; then
      if netstat -tuln | awk '{print $4}' | grep -q -E ":${port}$"; then return 1; fi
    else
      if (echo >"/dev/tcp/127.0.0.1/${port}") >/dev/null 2>&1; then return 1; fi
    fi
    return 0
  }

  read_port() {
    local label=$1 default=$2
    local input port
    while true; do
      read -r -u "${PROMPT_FD}" -p "Enter port for ${label} [${default}]: " input
      port="${input:-${default}}"
      if ! [[ "${port}" =~ ^[0-9]+$ ]] || [ "${port}" -lt 1 ] || [ "${port}" -gt 65535 ]; then
        echo -e "${RED}Enter a port between 1 and 65535.${NC}" >&2
        continue
      fi
      if ! check_port "${port}"; then
        echo -e "${RED}Port ${port} is currently in use.${NC}" >&2
        continue
      fi
      printf '%s' "${port}"
      return
    done
  }

  PORT="$(read_port "Pusher Clone" "6001")"
  echo -e "${GREEN}Using port ${PORT}.${NC}"
  METRICS_PORT="$(read_port "Prometheus Metrics" "9601")"
  echo -e "${GREEN}Using metrics port ${METRICS_PORT}.${NC}"
  DASHBOARD_PORT="$(read_port "Dashboard" "5174")"
  echo -e "${GREEN}Using dashboard port ${DASHBOARD_PORT}.${NC}"

  read -r -u "${PROMPT_FD}" -p "Enter public Pusher URL for third-party apps [http://localhost:${PORT}]: " INPUT_HOST
  HOST="${INPUT_HOST:-http://localhost:${PORT}}"
  HOST="${HOST%/}"
  while ! [[ "${HOST}" =~ ^https?://[A-Za-z0-9._-]+(:[0-9]{1,5})?$ ]]; do
    echo -e "${RED}Enter a full http(s) URL with no path, e.g. https://pusher.example.com.${NC}" >&2
    read -r -u "${PROMPT_FD}" -p "Public Pusher URL: " HOST
    HOST="${HOST%/}"
  done
  echo -e "${GREEN}Using public Pusher URL ${HOST}.${NC}"

  random_hex() {
    local bytes=$1
    if command -v openssl >/dev/null 2>&1; then
      openssl rand -hex "${bytes}"
    else
      od -An -N"${bytes}" -tx1 /dev/urandom | tr -d ' \n'
    fi
  }

  APP_ID="$(random_hex 3)"
  APP_KEY="$(random_hex 16)"
  APP_SECRET="$(random_hex 24)"
  ADMIN_TOKEN="$(random_hex 24)"

  CONFIG_TMP="$(mktemp "${INSTALL_DIR}/.config.yaml.XXXXXX")"
  cat <<YAMLEOF >"${CONFIG_TMP}"
port: "${PORT}"
metrics_port: "${METRICS_PORT}"
dashboard_port: "${DASHBOARD_PORT}"
host: "${HOST}"
admin_token: "${ADMIN_TOKEN}"
apps:
  - app_id: "${APP_ID}"
    app_key: "${APP_KEY}"
    app_secret: "${APP_SECRET}"
YAMLEOF
  chmod 600 "${CONFIG_TMP}"
  mv "${CONFIG_TMP}" "${CONFIG_FILE}"

  echo -e "${GREEN}Configuration saved to ${CONFIG_FILE}${NC}"
else
  echo -e "${GREEN}Keeping existing configuration at ${CONFIG_FILE}${NC}"
fi

chown -R "${USER}:${GROUP}" "${INSTALL_DIR}"
chmod 600 "${CONFIG_FILE}"

# 8. Create systemd service
if ! command -v systemctl >/dev/null 2>&1; then
  die "systemd is required to install the service."
fi

SERVICE_FILE="$(mktemp)"
trap 'rm -f "${SERVICE_FILE}"' EXIT
cat <<SVC_EOF >"${SERVICE_FILE}"
[Unit]
Description=Pusher Clone Server
After=network.target

[Service]
Type=simple
User=${USER}
Group=${GROUP}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/pusher-clone
Restart=always
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
SVC_EOF

install -m 0644 "${SERVICE_FILE}" "/etc/systemd/system/${SERVICE_NAME}.service"
rm -f "${SERVICE_FILE}"
trap - EXIT

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}.service"
systemctl restart "${SERVICE_NAME}.service"

if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  echo -e "${GREEN}Service ${SERVICE_NAME} is running.${NC}"
else
  echo -e "${RED}Service ${SERVICE_NAME} failed to start. Check: journalctl -u ${SERVICE_NAME}${NC}" >&2
  exit 1
fi

DASHBOARD_DISPLAY_PORT="$(awk -F': *' '/^dashboard_port:/ {gsub(/"/, "", $2); print $2; exit}' "${CONFIG_FILE}")"
DASHBOARD_DISPLAY_PORT="${DASHBOARD_DISPLAY_PORT:-5174}"

echo -e "${GREEN}Installation complete.${NC}"
echo -e "Dashboard: http://localhost:${DASHBOARD_DISPLAY_PORT}/ (admin token is in ${CONFIG_FILE})"
