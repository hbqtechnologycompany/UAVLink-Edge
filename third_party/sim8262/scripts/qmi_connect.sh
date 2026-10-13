#!/usr/bin/env bash
# Connect SIM8262 PCIe via QMI + rmnet0 (wraps tqmi-connect + route setup).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEVICE="${1:-/dev/wwan0qmi0}"
APN="${2:-m-wap}"
PARENT_IFACE="${3:-mhi_hwip0}"
MUX_ID="${MUX_ID:-112}"
RMNET_IFACE="${RMNET_IFACE:-rmnet0}"
TQMI="${ROOT}/scripts/tqmi-connect"
RECOVER="${ROOT}/scripts/recover_mhi.sh"
RADIO="${ROOT}/scripts/set_radio_preference.sh"
SESSION_FILE="${ROOT}/ip-session-${RMNET_IFACE}-4"
RAT_PROFILE="${RAT_PROFILE:-stability}"

_cleanup_stale() {
  if [[ -f "$SESSION_FILE" ]]; then
    echo "Releasing stale session..."
    sudo "$TQMI" -d "$DEVICE" --release --iface "$RMNET_IFACE" 2>/dev/null || true
    rm -f "$SESSION_FILE"
  fi
  sudo ip link del "$RMNET_IFACE" 2>/dev/null || true
  sleep 1
}

_ping_ok() {
  local iface=$1 target=$2
  ping -I "$iface" -c 2 -W 3 "$target" 2>/dev/null | grep -q ' 0% packet loss'
}

sudo systemctl stop ModemManager 2>/dev/null || true
sudo modprobe rmnet 2>/dev/null || {
  echo "rmnet module missing — run: sudo ${ROOT}/scripts/build_rmnet_module.sh"
  exit 1
}
sudo modprobe mhi_net 2>/dev/null || true

if [[ ! -e "$DEVICE" ]]; then
  echo "QMI device missing — running recovery..."
  sudo bash "$RECOVER"
fi

_cleanup_stale

echo "RAT preference ($RAT_PROFILE)..."
sudo bash "$RADIO" "$DEVICE" "$RAT_PROFILE"

echo "Connect: device=$DEVICE apn=$APN iface=$PARENT_IFACE mux=$MUX_ID"
cd "$ROOT"
sudo "$TQMI" -d "$DEVICE" --connect --apn "$APN" --iface "$PARENT_IFACE" --muxid "$MUX_ID"

[[ -f "$SESSION_FILE" ]] || { echo "Missing $SESSION_FILE"; exit 1; }
# shellcheck disable=SC1090
source "$SESSION_FILE"

echo "Bring up $QMIMUX_IFACE: $IP/$CIDR via $GW"
sudo ip link set "$PARENT_IFACE" up 2>/dev/null || true
sudo ip link set "$QMIMUX_IFACE" mtu "${MTU:-1500}"
sudo ip link set "$QMIMUX_IFACE" up
# KHÔNG đặt default vào main table — SSH/admin giữ wlan0; cloud đi PBR table 100
sudo ip route del default dev "$QMIMUX_IFACE" 2>/dev/null || true

echo "Connectivity test..."
sleep 2
if _ping_ok "$QMIMUX_IFACE" 8.8.8.8; then
  echo "Internet OK via $QMIMUX_IFACE"
  ping -I "$QMIMUX_IFACE" -c 3 -W 3 8.8.8.8
elif _ping_ok "$QMIMUX_IFACE" "$GW"; then
  echo "Gateway OK via $QMIMUX_IFACE"
else
  echo "WARN: ping test failed — check SIM data plan / signal"
  ping -I "$QMIMUX_IFACE" -c 2 -W 2 8.8.8.8 || true
fi
echo "---"
ip -br addr show "$QMIMUX_IFACE"
ip -s link show "$QMIMUX_IFACE" | grep -A2 'RX:'
