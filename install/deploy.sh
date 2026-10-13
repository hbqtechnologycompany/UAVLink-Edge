#!/bin/bash
# =============================================================================
# DroneBridge Deployer — Ubuntu → Pi CM5
# Cách dùng: ./deploy.sh pi@cm5gw
# =============================================================================

set -e

TARGET_HOST=${1:?"Usage: ./deploy.sh user@hostname"}
INSTALL_DIR="/opt/dronebridge"
STAGING_DIR="/tmp/dronebridge_staging"   # Thư mục tạm — user pi có quyền ghi

echo "===================================================="
echo "   DroneBridge Deployer -> $TARGET_HOST"
echo "===================================================="

# ── 1. Build ARM64 ─────────────────────────────────────────────────────────────
echo "▶ [1/4] Building binary for ARM64..."
GOOS=linux GOARCH=arm64 go build -mod=vendor -o dronebridge .
echo "✅ Build complete."

# ── 2. Chuẩn bị thư mục staging trên Pi ───────────────────────────────────────
echo "▶ [2/4] Preparing staging directory on Pi..."
ssh "$TARGET_HOST" "rm -rf $STAGING_DIR && mkdir -p $STAGING_DIR"

# ── 3. Rsync vào /tmp (pi có quyền, không cần sudo) ───────────────────────────
echo "▶ [3/4] Syncing files to Pi (staging: $STAGING_DIR)..."
rsync -avz --progress \
    dronebridge \
    config.yaml \
    install \
    Find_landing \
    Module_4G \
    etc \
    "$TARGET_HOST:$STAGING_DIR/"

echo "✅ Sync complete."

# ── 4. Chạy installer từ staging (dùng sudo để cài vào /opt) ──────────────────
echo "▶ [4/4] Running installer on Pi..."
ssh "$TARGET_HOST" "cd $STAGING_DIR && sudo bash install/install.sh"

echo ""
echo "===================================================="
echo "✅ DEPLOY HOÀN TẤT!"
echo ""
echo "Lệnh hữu ích:"
echo "  ssh $TARGET_HOST 'journalctl -u dronebridge -f'"
echo "  ssh $TARGET_HOST 'journalctl -u dronebridge-netmon -f'"
echo "  ssh $TARGET_HOST 'ip route show table dronebridge'"
echo "===================================================="
