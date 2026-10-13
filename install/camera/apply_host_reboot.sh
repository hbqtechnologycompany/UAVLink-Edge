#!/bin/bash
# Reboot host khi UART2/camera overlay vừa đổi (DRONEBRIDGE_AUTO_REBOOT=1, mặc định bật)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
MARKER="$INSTALL_DIR/.need_host_reboot"
CAM_MARKER="$INSTALL_DIR/.need_camera_reboot"
LOCK="/run/dronebridge-reboot.lock"
AUTO="${DRONEBRIDGE_AUTO_REBOOT:-1}"
REBOOT_DELAY="${DRONEBRIDGE_REBOOT_DELAY:-2}"

if [ "$AUTO" != "1" ]; then
    exit 0
fi

reason=""
if [ -f "$MARKER" ]; then
    reason="$(tr '\n' ' ' < "$MARKER" | sed 's/ $//')"
elif [ -f "$CAM_MARKER" ]; then
    reason="camera overlay"
elif [ -f "$INSTALL_DIR/.need_lcd_reboot" ]; then
    reason="lcd i2c3 overlay"
else
    exit 0
fi

exec 9>"$LOCK"
if ! flock -n 9; then
    logger -t dronebridge "Auto-reboot skipped — already scheduled"
    exit 0
fi

logger -t dronebridge "Auto-reboot: $reason"
echo "🔄 Tự reboot trong ${REBOOT_DELAY}s ($reason)..."
sleep "$REBOOT_DELAY"
touch /run/dronebridge-rebooting
sync
systemctl reboot
