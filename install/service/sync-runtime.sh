#!/bin/bash
# Đồng bộ code từ repo dev → /opt/dronebridge (runtime Docker bind-mount).
#
# Vì sao cần script này:
#   - Container mount /opt/dronebridge:/opt/dronebridge → chạy code trên host, KHÔNG phải repo dev.
#   - UI lưu config.yaml / camera_config_*.json vào /opt/dronebridge — phần đó đã hoạt động.
#   - Sửa Python/Go trong repo KHÔNG tự vào /opt nếu không sync.
#
# Cách dùng (trên Pi):
#   cd /home/.../Pi_CM5_DroneBridgeService_push
#   sudo bash install/service/sync-runtime.sh
#   sudo bash install/service/sync-runtime.sh --restart
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
INSTALL_DIR="${DRONEBRIDGE_DIR:-/opt/dronebridge}"
DO_RESTART=0
DO_BUILD_GO=1

for arg in "$@"; do
    case "$arg" in
        --restart) DO_RESTART=1 ;;
        --no-build) DO_BUILD_GO=0 ;;
        -h|--help)
            echo "Usage: sudo bash install/service/sync-runtime.sh [--restart] [--no-build]"
            exit 0
            ;;
        *) echo "Unknown option: $arg" >&2; exit 1 ;;
    esac
done

if [ "$(id -u)" -ne 0 ]; then
    if [ ! -w "$INSTALL_DIR" ]; then
        echo "Chạy với sudo: sudo bash install/service/sync-runtime.sh" >&2
        exit 1
    fi
    RUN_USER="$(id -un)"
else
    RUN_USER="${SUDO_USER:-$(logname 2>/dev/null || echo cm5drone6425)}"
fi

echo "===================================================="
echo " Sync runtime: $REPO_ROOT"
echo "           →   $INSTALL_DIR"
echo "===================================================="

mkdir -p "$INSTALL_DIR"/{Find_landing,Module_4G,lcd_monitor,data}

# Python camera + landing — giữ JSON runtime do UI tạo
if [ -d "$REPO_ROOT/Find_landing" ]; then
    echo "▶ Find_landing..."
    rsync -a --delete \
        --exclude 'camera_detected.json' \
        --exclude 'camera_config.json' \
        --exclude 'camera_config_0.json' \
        --exclude 'camera_config_1.json' \
        --exclude '__pycache__/' \
        --exclude '*.pyc' \
        "$REPO_ROOT/Find_landing/" "$INSTALL_DIR/Find_landing/"
fi

# Module 4G / LCD
for dir in Module_4G lcd_monitor; do
    if [ -d "$REPO_ROOT/$dir" ]; then
        echo "▶ $dir..."
        rsync -a "$REPO_ROOT/$dir/" "$INSTALL_DIR/$dir/"
    fi
done

# Cây install/ — script cài và script chạy lúc boot
if [ -d "$REPO_ROOT/install" ]; then
    echo "▶ install/..."
    mkdir -p "$INSTALL_DIR/install"
    rsync -a "$REPO_ROOT/install/" "$INSTALL_DIR/install/"
    find "$INSTALL_DIR/install" -name '*.sh' -exec chmod 755 {} \;
fi

# Docker compose files (nếu repo có docker/ đầy đủ hơn)
if [ -d "$REPO_ROOT/docker" ]; then
    echo "▶ docker/..."
    mkdir -p "$INSTALL_DIR/docker"
    rsync -a "$REPO_ROOT/docker/" "$INSTALL_DIR/docker/"
    # Giữ .env runtime
    if [ ! -f "$INSTALL_DIR/docker/.env" ] && [ -f "$INSTALL_DIR/docker/.env.example" ]; then
        cp -n "$INSTALL_DIR/docker/.env.example" "$INSTALL_DIR/docker/.env" 2>/dev/null || true
    fi
fi

# Go binary (UI embed trong binary — phải build lại khi đổi web/)
if [ "$DO_BUILD_GO" = "1" ]; then
    echo "▶ go build → $INSTALL_DIR/dronebridge"
    if command -v go >/dev/null 2>&1; then
        (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o "$INSTALL_DIR/dronebridge" .)
    else
        echo "   ⚠ go không có trên PATH — bỏ qua build (dùng --no-build nếu chỉ sync Python)"
    fi
fi

chown -R "$RUN_USER:$RUN_USER" "$INSTALL_DIR" 2>/dev/null || true

echo ""
echo "✅ Sync xong. KHÔNG ghi đè:"
echo "   - $INSTALL_DIR/config.yaml"
echo "   - $INSTALL_DIR/Find_landing/camera_config_*.json"
echo "   - $INSTALL_DIR/Find_landing/camera_detected.json"

if [ "$DO_RESTART" = "1" ]; then
    echo ""
    echo "▶ Restart Docker + camera stream..."
    if docker ps --format '{{.Names}}' 2>/dev/null | grep -qx dronebridge; then
        docker restart dronebridge
        sleep 8
        docker exec dronebridge curl -sf -X POST http://127.0.0.1:8080/api/camera/restart >/dev/null \
            && echo "   ✓ camera stream restarted" \
            || echo "   ⚠ camera restart API failed — thử: docker restart dronebridge"
    elif systemctl is-active --quiet dronebridge-docker.service 2>/dev/null; then
        systemctl restart dronebridge-docker.service
    elif systemctl is-active --quiet dronebridge.service 2>/dev/null; then
        systemctl restart dronebridge.service
        if systemctl is-active --quiet dronebridge-netmon.service 2>/dev/null; then
            bash "$INSTALL_DIR/install/network/setup_pbr.sh" 2>/dev/null || true
            systemctl restart dronebridge-netmon.service
        fi
    else
        echo "   ⚠ Không tìm thấy dronebridge container/service"
    fi
else
    echo ""
    echo "Tiếp theo: sudo bash install/service/sync-runtime.sh --restart"
    echo "  hoặc: docker restart dronebridge && curl -X POST http://127.0.0.1:8080/api/camera/restart"
fi

echo "===================================================="
