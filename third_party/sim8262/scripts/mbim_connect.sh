#!/usr/bin/env bash
# Connect SIM8262 over PCIe/MBIM using proper MBIM session (TRID chain).
set -euo pipefail

DEVICE="${1:-/dev/wwan0mbim0}"
APN="${2:-m-wap}"
IP_TYPE="${3:-ipv4}"

_extract_trid() {
  sed -n "s/.*TRID.*'\([^']*\)'.*/\1/p" | tail -1
}

_run() {
  local out
  out=$(mbimcli -d "$DEVICE" "$@" 2>&1) || { echo "$out"; return 1; }
  echo "$out"
  echo "$out" | _extract_trid
}

echo "Device: $DEVICE  APN: $APN"
sudo systemctl stop ModemManager 2>/dev/null || true

echo "[1] Subscriber ready..."
mapfile -t lines < <(sudo mbimcli -d "$DEVICE" --query-subscriber-ready-status --no-close 2>&1)
printf '%s\n' "${lines[@]}"
TRID=$(printf '%s\n' "${lines[@]}" | _extract_trid)
[[ -n "$TRID" ]] || { echo "No TRID"; exit 1; }

echo "[2] Registration (TRID=$TRID)..."
mapfile -t lines < <(sudo mbimcli -d "$DEVICE" --query-registration-state --no-open="$TRID" --no-close 2>&1)
printf '%s\n' "${lines[@]}"
TRID=$(printf '%s\n' "${lines[@]}" | _extract_trid)

echo "[3] Attach packet service (TRID=$TRID)..."
mapfile -t lines < <(sudo mbimcli -d "$DEVICE" --attach-packet-service --no-open="$TRID" --no-close 2>&1)
printf '%s\n' "${lines[@]}"
TRID=$(printf '%s\n' "${lines[@]}" | _extract_trid)

echo "[4] Connect (TRID=$TRID)..."
CONNECT_OUT=$(sudo mbimcli -d "$DEVICE" --connect="access-string=${APN},ip-type=${IP_TYPE}" --no-open="$TRID" 2>&1)
echo "$CONNECT_OUT"

# Configure data interface (mhi_hwip0 for Qualcomm PCIe MBIM data path)
IP_LINE=$(echo "$CONNECT_OUT" | grep "IP \[0\]:" | head -1)
GW_LINE=$(echo "$CONNECT_OUT" | grep "Gateway:" | head -1)
if [[ -n "$IP_LINE" && -n "$GW_LINE" ]]; then
  IP_CIDR=$(echo "$IP_LINE" | sed "s/.*'\\([^']*\\)'.*/\\1/")
  GATEWAY=$(echo "$GW_LINE" | sed "s/.*'\\([^']*\\)'.*/\\1/")
  IFACE=""
  for candidate in mhi_hwip0 mhi_swip0; do
    if ip link show "$candidate" &>/dev/null; then
      IFACE="$candidate"
      break
    fi
  done
  if [[ -n "$IFACE" ]]; then
    echo "[5] Configure $IFACE: $IP_CIDR via $GATEWAY"
    echo "NOTE: PCIe SDX65 data uses rmnet0 — run scripts/qmi_connect.sh instead."
    sudo modprobe mhi_net 2>/dev/null || true
    sudo ip addr flush dev "$IFACE" 2>/dev/null || true
    sudo ip addr add "$IP_CIDR" dev "$IFACE"
    sudo ip link set "$IFACE" up
    sudo ip route replace default via "$GATEWAY" dev "$IFACE" metric 100
    echo "Test: ping -I $IFACE 8.8.8.8"
  else
    echo "[5] No mhi_hwip0/mhi_swip0 — load: sudo modprobe mhi_net"
  fi
else
  echo "[5] Connect did not return IP configuration"
fi
