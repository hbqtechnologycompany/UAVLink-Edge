#!/bin/bash
# Khối A — PX4 Ethernet: gán IP tĩnh cho cổng nối PX4 (đọc config.yaml).
# Không đụng 4G / PBR / MAVLink server. CM5: end0; Pi4: eth0.
set -euo pipefail
CONFIG="${1:-/opt/dronebridge/config.yaml}"
CONN_NAME="dronebridge-px4-eth"

read -r IFACE_CFG IP SUBNET AUTO_SETUP <<< "$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f) or {}
eth = cfg.get("ethernet") or {}
print(eth.get("interface", "eth0"),
      eth.get("local_ip", ""),
      eth.get("subnet", "24"),
      "1" if eth.get("auto_setup", False) else "0")
PY
)"

IFACE="$(python3 - "$IFACE_CFG" <<'PY'
import os, sys
c = sys.argv[1]
if c and os.path.exists(f"/sys/class/net/{c}"):
    print(c)
    raise SystemExit(0)
for n in ("end0", "eth0"):
    if os.path.exists(f"/sys/class/net/{n}"):
        print(n)
        raise SystemExit(0)
print(c or "eth0")
PY
)"

if [ "$AUTO_SETUP" != "1" ] || [ -z "$IP" ]; then
    echo "Ethernet auto_setup tắt hoặc local_ip trống — bỏ qua"
    exit 0
fi
if ! ip link show "$IFACE" >/dev/null 2>&1; then
    echo "Không tìm thấy interface PX4 (config=${IFACE_CFG}, resolved=${IFACE})"
    exit 1
fi
if [ "$IFACE" != "$IFACE_CFG" ]; then
    echo "ℹ️  PX4 netdev: ${IFACE_CFG} → ${IFACE}"
fi

CIDR="${IP}/${SUBNET}"

if command -v nmcli >/dev/null 2>&1 && systemctl is-active -q NetworkManager; then
    if nmcli -t -f NAME connection show | grep -qx "$CONN_NAME"; then
        nmcli connection modify "$CONN_NAME" \
            connection.interface-name "$IFACE" \
            ipv4.method manual ipv4.addresses "$CIDR" \
            ipv4.never-default yes connection.autoconnect yes
    else
        nmcli connection add type ethernet con-name "$CONN_NAME" ifname "$IFACE" \
            ipv4.method manual ipv4.addresses "$CIDR" \
            ipv4.never-default yes connection.autoconnect yes
    fi
    for old in $(nmcli -t -f NAME,TYPE connection show | awk -F: '$2=="802-3-ethernet" && $1!="'"$CONN_NAME"'" {print $1}'); do
        nmcli connection modify "$old" connection.autoconnect no 2>/dev/null || true
    done
    nmcli connection up "$CONN_NAME" ifname "$IFACE" || true
    echo "✅ $IFACE = $CIDR (NetworkManager)"
else
    ip link set dev "$IFACE" up
    ip -4 addr show dev "$IFACE" | grep -q "inet ${IP}/" || ip addr add "$CIDR" dev "$IFACE"
    echo "✅ $IFACE = $CIDR (ip command)"
fi
