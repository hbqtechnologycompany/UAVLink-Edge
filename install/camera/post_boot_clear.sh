#!/bin/bash
# Sau reboot: xóa marker khi overlay đã áp dụng (gọi từ entrypoint / setup services)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
CONFIG="$INSTALL_DIR/config.yaml"
MARKER="$INSTALL_DIR/.need_host_reboot"
CAM_MARKER="$INSTALL_DIR/.need_camera_reboot"
LCD_MARKER="$INSTALL_DIR/.need_lcd_reboot"

touch /run/dronebridge-post-reboot 2>/dev/null || true
rm -f /run/dronebridge-rebooting /run/dronebridge-reboot.lock 2>/dev/null || true

if [ -f "$CAM_MARKER" ]; then
    bash "$SCRIPT_DIR/setup_camera.sh" --boot-settled "$CONFIG" 2>/dev/null || true
fi
if [ -f "$LCD_MARKER" ]; then
    bash "$SCRIPT_DIR/../network/setup_lcd_i2c.sh" "$CONFIG" 2>/dev/null || true
fi
if [ -f "$MARKER" ] && grep -q '^uart$' "$MARKER" 2>/dev/null; then
    bash "$SCRIPT_DIR/../network/setup_mavlink_uart.sh" 2>/dev/null || true
fi

LCD_BUS="$(python3 - "$CONFIG" <<'PY' 2>/dev/null || echo 3
import sys, yaml
with open(sys.argv[1]) as f:
    print(int((yaml.safe_load(f) or {}).get("lcd", {}).get("bus", 3)))
PY
)"

camera_ok=1
[ -f "$CAM_MARKER" ] && camera_ok=0

lcd_ok=1
if [ -f "$LCD_MARKER" ] || { [ -f "$MARKER" ] && grep -q '^lcd$' "$MARKER" 2>/dev/null; }; then
    [ -e "/dev/i2c-${LCD_BUS}" ] || lcd_ok=0
fi

uart_ok=1
if [ -f "$MARKER" ] && grep -q '^uart$' "$MARKER" 2>/dev/null; then
    [ -e /dev/ttyAMA2 ] && [ -e /dev/ttyAMA3 ] || uart_ok=0
fi

if [ "$camera_ok" = "1" ] && [ "$lcd_ok" = "1" ] && [ "$uart_ok" = "1" ]; then
    rm -f "$MARKER" "$CAM_MARKER" "$LCD_MARKER"
    logger -t dronebridge "Post-boot: overlay markers cleared"
fi
