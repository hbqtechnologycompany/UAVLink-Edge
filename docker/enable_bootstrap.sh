#!/bin/bash
# Bật drone-bootstrap + chạy container (gọi từ install.sh hoặc docker/setup.sh)
set -euo pipefail

DOCKER_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(dirname "$DOCKER_DIR")"
RUN_USER="${SUDO_USER:-$(logname 2>/dev/null || echo cm5drone6425)}"

if [ "$EUID" -ne 0 ]; then
    echo "❌ Chạy với sudo"
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "⚠️  Docker chưa cài — bỏ qua bootstrap container"
    exit 0
fi

systemctl enable docker 2>/dev/null || true

if [ ! -f "$DOCKER_DIR/.env" ]; then
    cp "$DOCKER_DIR/.env.example" "$DOCKER_DIR/.env" 2>/dev/null || touch "$DOCKER_DIR/.env"
fi
grep -q '^HOST_REPO_PATH=' "$DOCKER_DIR/.env" \
    && sed -i "s|^HOST_REPO_PATH=.*|HOST_REPO_PATH=$REPO_ROOT|" "$DOCKER_DIR/.env" \
    || echo "HOST_REPO_PATH=$REPO_ROOT" >> "$DOCKER_DIR/.env"
grep -q '^DRONE_RUN_USER=' "$DOCKER_DIR/.env" \
    && sed -i "s|^DRONE_RUN_USER=.*|DRONE_RUN_USER=$RUN_USER|" "$DOCKER_DIR/.env" \
    || echo "DRONE_RUN_USER=$RUN_USER" >> "$DOCKER_DIR/.env"

UNIT="/etc/systemd/system/drone-bootstrap.service"
sed "s|__DOCKER_DIR__|$DOCKER_DIR|g" "$DOCKER_DIR/drone-bootstrap.service" > "$UNIT"
systemctl daemon-reload
systemctl enable drone-bootstrap.service

chmod +x "$DOCKER_DIR/compose.sh"
cd "$DOCKER_DIR"
bash "$DOCKER_DIR/compose.sh" build -q 2>/dev/null || bash "$DOCKER_DIR/compose.sh" build
bash "$DOCKER_DIR/compose.sh" up -d --force-recreate
echo "✅ Docker bootstrap enabled (drone-bootstrap.service)"
