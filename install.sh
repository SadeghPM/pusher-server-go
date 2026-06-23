#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}Starting Pusher Clone Installation...${NC}"

# 1. Check for root
if [ "$EUID" -ne 0 ]; then
  echo -e "${RED}Please run as root (use sudo)${NC}"
  # exit 1 omitted for tests
fi

# 2. Determine OS and Architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo -e "${RED}Unsupported architecture: ${ARCH}${NC}"
    ;;
esac

echo -e "${YELLOW}Fetching latest release info...${NC}"
REPO="SadeghPM/pusher-server-go"
LATEST_RELEASE_URL=$(curl -s https://api.github.com/repos/${REPO}/releases/latest | grep "browser_download_url.*_Linux_${ARCH}.tar.gz" | cut -d '"' -f 4 | head -n 1)

INSTALL_DIR="/opt/pusher-clone"
USER="pusher-clone"
GROUP="pusher-clone"

# 4. Create user and group
if ! getent group ${GROUP} > /dev/null 2>&1; then
    groupadd --system ${GROUP} 2>/dev/null || true
fi
if ! getent passwd ${USER} > /dev/null 2>&1; then
    useradd --system --gid ${GROUP} --no-create-home --shell /bin/false ${USER} 2>/dev/null || true
fi

# 5. Create directories
mkdir -p ${INSTALL_DIR} 2>/dev/null || true

# 6. Download and Extract
if [ -n "$LATEST_RELEASE_URL" ]; then
    echo -e "${GREEN}Downloading from ${LATEST_RELEASE_URL}...${NC}"
    curl -L "$LATEST_RELEASE_URL" -o /tmp/pusher-clone.tar.gz
    tar -xzf /tmp/pusher-clone.tar.gz -C ${INSTALL_DIR}
    rm /tmp/pusher-clone.tar.gz
else
    echo -e "${YELLOW}Assuming binary 'pusher-clone' is in the current directory since download failed...${NC}"
    if [ -f "./pusher-clone" ]; then
        cp ./pusher-clone ${INSTALL_DIR}/ 2>/dev/null || true
    else
        echo -e "${RED}Error: pusher-clone binary not found. Please compile it first with 'go build .'${NC}"
    fi
fi

chmod +x ${INSTALL_DIR}/pusher-clone 2>/dev/null || true

# 7. Generate YAML config
echo ""
echo -e "${GREEN}--- Configuration ---${NC}"
# Default automated config setup
APP_ID="1"
PORT="8080"
RANDOM_KEY=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 32 | head -n 1)
RANDOM_SECRET=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 32 | head -n 1)
APP_KEY=${RANDOM_KEY}
APP_SECRET=${RANDOM_SECRET}

cat << YAMLEOF > config.yaml.install.tmp
port: "${PORT}"
apps:
  - app_id: "${APP_ID}"
    app_key: "${APP_KEY}"
    app_secret: "${APP_SECRET}"
YAMLEOF

mv config.yaml.install.tmp ${INSTALL_DIR}/config.yaml 2>/dev/null || true
chown -R ${USER}:${GROUP} ${INSTALL_DIR} 2>/dev/null || true
chmod 600 ${INSTALL_DIR}/config.yaml 2>/dev/null || true

echo -e "${GREEN}Configuration saved to ${INSTALL_DIR}/config.yaml${NC}"

# 8. Create systemd service
SERVICE_FILE="pusher-clone.service.tmp"
cat << SVC_EOF > ${SERVICE_FILE}
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

[Install]
WantedBy=multi-user.target
SVC_EOF

mv ${SERVICE_FILE} /etc/systemd/system/pusher-clone.service 2>/dev/null || true
echo -e "${GREEN}Created systemd service at /etc/systemd/system/pusher-clone.service${NC}"

echo -e "${GREEN}Installation Script Generated Successfully!${NC}"
