#!/bin/bash
# Khôi phục sau khi setcap làm dronebridge không start (203/EXEC).
# Chạy: sudo bash install/network/recover-network.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
INSTALL_DIR="${DRONEBRIDGE_DIR:-/opt/dronebridge}"

echo "▶ [1/5] Gỡ file capabilities (fix 203/EXEC)..."
setcap -r "$INSTALL_DIR/dronebridge" 2>/dev/null || true

echo "▶ [2/5] Sync code từ repo..."
bash "$REPO_ROOT/install/service/sync-runtime.sh" --no-build

echo "▶ [3/5] Build Go binary..."
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o "$INSTALL_DIR/dronebridge" .)
chmod +x "$INSTALL_DIR/dronebridge"
setcap -r "$INSTALL_DIR/dronebridge" 2>/dev/null || true

echo "▶ [4/5] PBR + eth route table 100..."
bash "$INSTALL_DIR/install/network/setup_pbr.sh"

echo "▶ [5/5] Restart services..."
systemctl restart dronebridge-netmon.service
sleep 2
systemctl restart dronebridge.service
sleep 3

echo ""
echo "=== Status ==="
systemctl is-active dronebridge dronebridge-netmon || true
ss -tlnp | grep ':8080' || echo "⚠ :8080 not listening"
echo ""
ip route show table dronebridge
echo ""
ip route get 10.41.10.2 mark 0x1 || true
echo ""
journalctl -u dronebridge -n 8 --no-pager | grep -iE 'PARTNER_HB|AUTH|error|Failed' || true
