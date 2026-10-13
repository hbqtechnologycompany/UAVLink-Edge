#!/bin/bash
# Cho phép user dronebridge gọi setup camera + reboot không cần mật khẩu (chạy 1 lần bằng sudo)
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "Chạy: sudo bash install_camera_sudoers.sh [username]" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
USER_NAME="${1:-cm5drone6425}"
DEST="/etc/sudoers.d/dronebridge-camera"

cat > "$DEST" <<EOF
# DroneBridge — đúng lệnh mà service gọi (script trực tiếp, có hoặc không có tham số).
# Không dùng "bash script": sudoers không khớp thì luôn đòi mật khẩu.
${USER_NAME} ALL=(root) NOPASSWD: ${SCRIPT_DIR}/setup_camera.sh, ${SCRIPT_DIR}/setup_camera.sh *, ${SCRIPT_DIR}/apply_host_reboot.sh, ${SCRIPT_DIR}/apply_camera_overlay.sh, ${SCRIPT_DIR}/apply_camera_overlay.sh *, ${SCRIPT_DIR}/post_boot_clear.sh
EOF
chmod 440 "$DEST"
visudo -cf "$DEST"
echo "✓ Đã cài sudoers: $DEST"
echo "  User ${USER_NAME} có thể: sudo -n ${SCRIPT_DIR}/apply_camera_overlay.sh"
