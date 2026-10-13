#!/bin/bash
# Build local rồi deploy binary + restart DroneBridge (không reboot hệ thống).
#
#   go build -o dronebridge .
#   sudo bash install/service/deploy-restart.sh
#
# UI (dashboard/settings) nằm TRONG binary (go:embed) — phải restart process sau khi build.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BINARY_SRC="${REPO_ROOT}/dronebridge"

if [ "$(id -u)" -ne 0 ]; then
    echo "Chạy với sudo: sudo bash deploy-restart.sh" >&2
    exit 1
fi

if [ ! -f "$BINARY_SRC" ]; then
    echo "Chưa có binary — chạy trước: go build -o dronebridge ." >&2
    exit 1
fi

# shellcheck source=dronebridge-ctl.sh
source "${SCRIPT_DIR}/dronebridge-ctl.sh"

echo "▶ Deploy-restart DroneBridge"
restart_dronebridge "$BINARY_SRC"

IP="$(hostname -I | awk '{print $1}')"
echo "✓ Xong — http://${IP}:8080/dashboard.html"
