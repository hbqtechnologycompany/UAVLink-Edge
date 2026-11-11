#!/bin/bash
# Cho phép user dịch vụ gọi đúng các script camera (setup, overlay, reboot) không cần mật khẩu.
# Web UI gọi chúng bằng "sudo -n <script>". Chạy 1 lần bằng sudo (install.sh tự gọi).
# Gọi: sudo bash install_camera_sudoers.sh [username]
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "Chạy: sudo bash install_camera_sudoers.sh [username]" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/board.sh
source "$SCRIPT_DIR/../lib/board.sh"
USER_NAME="${1:-$(dronebridge_service_user)}"
if [ -z "$USER_NAME" ] || ! id -u "$USER_NAME" >/dev/null 2>&1; then
    echo "Không xác định được user dịch vụ (truyền tên user làm tham số)" >&2
    exit 1
fi
DEST="/etc/sudoers.d/dronebridge-camera"
TMP="$(mktemp)"

# Script trực tiếp, không qua "bash script": sudoers không khớp thì luôn đòi mật khẩu.
cat > "$TMP" <<EOF
# DroneBridge — đúng lệnh mà service gọi (script trực tiếp, có hoặc không có tham số).
${USER_NAME} ALL=(root) NOPASSWD: ${SCRIPT_DIR}/setup_camera.sh, ${SCRIPT_DIR}/setup_camera.sh *, ${SCRIPT_DIR}/apply_host_reboot.sh, ${SCRIPT_DIR}/apply_camera_overlay.sh, ${SCRIPT_DIR}/apply_camera_overlay.sh *, ${SCRIPT_DIR}/post_boot_clear.sh
EOF
if ! visudo -cf "$TMP" >/dev/null; then
    rm -f "$TMP"
    echo "✗ sudoers không hợp lệ — không ghi $DEST" >&2
    exit 1
fi
install -m 0440 -o root -g root "$TMP" "$DEST"
rm -f "$TMP"
echo "✓ Đã cài sudoers: $DEST"
echo "  User ${USER_NAME} có thể: sudo -n ${SCRIPT_DIR}/apply_camera_overlay.sh"
