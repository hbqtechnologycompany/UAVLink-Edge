#!/bin/bash
# Áp dụng overlay camera vào boot config + reboot CM5 (cần sudo, xem install_camera_sudoers.sh)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
CONFIG="${1:-$INSTALL_DIR/config.yaml}"

run_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1 && sudo -n "$@"; then
        return 0
    else
        echo "✗ Cần quyền root cho: $*" >&2
        exit 1
    fi
}

echo "▶ setup_camera.sh ..."
run_root "$SCRIPT_DIR/setup_camera.sh" "$CONFIG"

if [ -f "$INSTALL_DIR/.need_camera_reboot" ] || \
   { [ -f "$INSTALL_DIR/.need_host_reboot" ] && grep -q '^camera$' "$INSTALL_DIR/.need_host_reboot" 2>/dev/null; }; then
    echo "▶ apply_host_reboot.sh ..."
    run_root "$SCRIPT_DIR/apply_host_reboot.sh"
else
    echo "ℹ️  Boot overlay không đổi — bỏ qua reboot"
fi
