#!/bin/bash
# =============================================================================
# DroneBridge Docker — cài Docker (nếu thiếu), build image, chạy container
#
#   cd docker && sudo bash setup.sh
#
# Lưu ý trung thực (Pi CM5 + CSI camera):
#   - Container chạy privileged + host network để dùng camera, VPN, MAVLink UDP.
#   - OpenCV: pip opencv-contrib-python-headless 4.10 (ArUco); không dùng apt python3-opencv 4.6.
#   - Latency video vẫn phụ thuộc pipeline (xem camera_streamer.py), không phải Docker.
#   - Nếu đang chạy systemd dronebridge.service, script sẽ stop/disable để tránh trùng.
# =============================================================================
set -euo pipefail

DOCKER_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(dirname "$DOCKER_DIR")"
INSTALL_DIR="${DRONEBRIDGE_DIR:-/opt/dronebridge}"
RUN_USER="${SUDO_USER:-$(logname 2>/dev/null || echo cm5drone6425)}"

if [ "$EUID" -ne 0 ]; then
    echo "❌ Chạy với sudo: sudo bash setup.sh"
    exit 1
fi

echo "===================================================="
echo "   DroneBridge Docker Setup"
echo "   Repo:    $REPO_ROOT"
echo "   Data:    $INSTALL_DIR"
echo "===================================================="

# ── 1. Docker engine ───────────────────────────────────────────────────────────
bash "$DOCKER_DIR/install_docker.sh"

# ── 2. Seed host data dir (bind-mount vào container) ───────────────────────────
echo "▶ [2/5] Chuẩn bị $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"/{data,Find_landing,Module_4G}

if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
    cp -f "$REPO_ROOT/config.yaml" "$INSTALL_DIR/config.yaml"
    echo "   ✅ Copied config.yaml"
fi

# Python streamer + landing CV (không ghi đè camera_detected.json nếu đã có)
if [ -d "$REPO_ROOT/Find_landing" ]; then
    for f in "$REPO_ROOT/Find_landing"/*; do
        base="$(basename "$f")"
        if [ "$base" = "camera_detected.json" ] && [ -f "$INSTALL_DIR/Find_landing/camera_detected.json" ]; then
            continue
        fi
        cp -a "$f" "$INSTALL_DIR/Find_landing/"
    done
fi

if [ -d "$REPO_ROOT/Module_4G" ]; then
    cp -a "$REPO_ROOT/Module_4G/." "$INSTALL_DIR/Module_4G/"
fi

chown -R "$RUN_USER:$RUN_USER" "$INSTALL_DIR" 2>/dev/null || true

# ── 3. .env ───────────────────────────────────────────────────────────────────
if [ ! -f "$DOCKER_DIR/.env" ]; then
    cp -f "$DOCKER_DIR/.env.example" "$DOCKER_DIR/.env"
fi
if grep -q '^DRONEBRIDGE_DIR=' "$DOCKER_DIR/.env" 2>/dev/null; then
    sed -i "s|^DRONEBRIDGE_DIR=.*|DRONEBRIDGE_DIR=$INSTALL_DIR|" "$DOCKER_DIR/.env"
else
    echo "DRONEBRIDGE_DIR=$INSTALL_DIR" >> "$DOCKER_DIR/.env"
fi

# ── 4. Tránh trùng với systemd native ─────────────────────────────────────────
stop_native_dronebridge() {
    for unit in dronebridge.service dronebridge-netmon.service dronebridge-4g-init.service; do
        if systemctl is-active --quiet "$unit" 2>/dev/null; then
            systemctl stop "$unit" || true
        fi
        systemctl disable "$unit" 2>/dev/null || true
    done
    if command -v ss >/dev/null 2>&1; then
        if ss -ulnp 2>/dev/null | grep -q ':14550 '; then
            echo "   Port 14550 đang bận — dừng process dronebridge trên host..."
            pkill -f '/opt/dronebridge/dronebridge' 2>/dev/null || true
            sleep 2
        fi
    fi
}

echo "▶ [3/5] Tắt dronebridge systemd (nếu có) — Docker sẽ thay thế..."
stop_native_dronebridge

# ── 5. Build & run ────────────────────────────────────────────────────────────
echo "▶ [4/5] Build image dronebridge:local (có thể mất vài phút lần đầu)..."
chmod +x "$DOCKER_DIR/compose.sh" "$DOCKER_DIR/entrypoint-dronebridge.sh"
cd "$DOCKER_DIR"
export DRONEBRIDGE_DIR="$INSTALL_DIR"
bash "$DOCKER_DIR/compose.sh" build dronebridge

echo "▶ [5/5] Start container..."
bash "$DOCKER_DIR/compose.sh" up -d --force-recreate dronebridge

# systemd unit để container tự bật sau reboot
UNIT="/etc/systemd/system/dronebridge-docker.service"
cat > "$UNIT" << EOF
[Unit]
Description=DroneBridge Docker (compose)
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$DOCKER_DIR
Environment=DRONEBRIDGE_DIR=$INSTALL_DIR
ExecStartPre=/bin/bash -c 'systemctl stop dronebridge.service dronebridge-netmon.service dronebridge-4g-init.service 2>/dev/null || true; pkill -f "/opt/dronebridge/dronebridge" 2>/dev/null || true; sleep 1'
ExecStart=$DOCKER_DIR/compose.sh up -d dronebridge
ExecStop=$DOCKER_DIR/compose.sh stop dronebridge
TimeoutStartSec=600

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable dronebridge-docker.service

echo ""
echo "===================================================="
echo "✅ DroneBridge Docker đang chạy"
echo ""
echo "  docker ps"
echo "  docker logs -f dronebridge"
echo "  curl -s http://127.0.0.1:8080/api/camera/status | python3 -m json.tool"
echo "  curl -s http://127.0.0.1:8080/api/camera/detect | python3 -m json.tool"
echo ""
echo "Dừng:  cd docker && sudo bash compose.sh down"
echo "Bật lại: sudo systemctl start dronebridge-docker"
echo "===================================================="
