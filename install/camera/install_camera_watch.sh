#!/bin/bash
# Bật tự reboot CM5 khi lưu loại camera từ web UI (không cần deploy binary mới).
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "Chạy: sudo bash install_camera_watch.sh" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"

apt-get install -y inotify-tools 2>/dev/null || true

chmod +x "$SCRIPT_DIR/watch_camera_overlay.sh" "$SCRIPT_DIR/apply_camera_overlay.sh"

bash "$SCRIPT_DIR/install_camera_sudoers.sh" "${SUDO_USER:-cm5drone6425}" 2>/dev/null || true

UNIT_SRC="$INSTALL_DIR/etc/systemd/dronebridge-camera-watch.service"
UNIT_DST="/etc/systemd/system/dronebridge-camera-watch.service"
cp "$UNIT_SRC" "$UNIT_DST"
systemctl daemon-reload
systemctl enable --now dronebridge-camera-watch.service

echo "✓ Camera overlay watcher đã bật"
systemctl status dronebridge-camera-watch.service --no-pager | head -8
echo ""
echo "Giờ lưu loại CAM trên http://$(hostname -I | awk '{print $1}'):8080/settings.html"
echo "→ nếu overlay đổi, CM5 sẽ tự reboot trong ~8s."
