#!/bin/bash
# =============================================================================
# setup_pbr.sh — Policy-Based Routing (3-plane: eth / wlan admin / wwan data)
# =============================================================================
# Planes:
#   eth0  (10.41.10.x) — MAVLink PX4 LAN: KHÔNG mark PBR, luôn local link
#   wlan0             — Admin (SSH, :8080 reply): main table, không mark
#   wwan0|wlan0       — Cloud uplink (auth/VPN/MAVLink): table 100 + fwmark
#
# Chạy bởi: dronebridge-netmon.service → ExecStartPre=
# =============================================================================

set -e

TABLE=100
TABLE_NAME="dronebridge"
WLAN_REPLY_TABLE=101
WLAN_REPLY_NAME="wlan_reply"
ETH_PIXHAWK_TABLE=103
ETH_PIXHAWK_NAME="eth_pixhawk"
MARK=0x01
DRONEBRIDGE_USER="cm5drone6425"
# Subnet PX4 LAN — khớp config.yaml ethernet.local_ip /24
ETH_LAN_CIDR="${DRONEBRIDGE_ETH_LAN_CIDR:-10.41.10.0/24}"
# PX4 netdev — CM5 end0, Pi4 eth0 (setup_eth.sh / eth_plane.py)
ETH_IFACE="${DRONEBRIDGE_ETH_IFACE:-eth0}"
if ! ip link show "$ETH_IFACE" &>/dev/null; then
    ip link show end0 &>/dev/null && ETH_IFACE=end0
fi

ensure_rt_table() {
    local id="$1" name="$2"
    if ! grep -qE "^${id}[[:space:]]+${name}" /etc/iproute2/rt_tables 2>/dev/null; then
        echo "${id}   ${name}" >> /etc/iproute2/rt_tables
        echo "[PBR] ✓ rt_tables: ${id} ${name}"
    fi
}

ensure_rt_table "${TABLE}" "${TABLE_NAME}"
ensure_rt_table "${WLAN_REPLY_TABLE}" "${WLAN_REPLY_NAME}"
ensure_rt_table "${ETH_PIXHAWK_TABLE}" "${ETH_PIXHAWK_NAME}"

# ── 1. ip rule: fwmark 0x1 → table 100 (cloud data plane) ───────────────────
if ip rule show | grep -q "fwmark 0x1 lookup ${TABLE_NAME}"; then
    echo "[PBR] ip rule đã tồn tại: fwmark ${MARK} → table ${TABLE_NAME}"
else
    ip rule add fwmark "${MARK}" table "${TABLE}" priority 100
    echo "[PBR] ✓ ip rule: fwmark ${MARK} → table ${TABLE} (${TABLE_NAME})"
fi

# ── 2. iptables mangle — thứ tự: exempt TRƯỚC, mark SAU ─────────────────────
# Gỡ rule RETURN legacy theo oif (gây lệch route cloud)
for legacy_iface in lo wlan0 tailscale0 "$ETH_IFACE"; do
    while iptables -t mangle -C OUTPUT -o "$legacy_iface" \
          -m owner --uid-owner "${DRONEBRIDGE_USER}" -j RETURN 2>/dev/null; do
        iptables -t mangle -D OUTPUT -o "$legacy_iface" \
            -m owner --uid-owner "${DRONEBRIDGE_USER}" -j RETURN
        echo "[PBR] Removed legacy RETURN oif ${legacy_iface}"
    done
done

# Helper: insert rule at top if missing (newest -I = highest priority)
ensure_mangle_return() {
    local desc="$1"
    shift
    if ! iptables -t mangle -C OUTPUT "$@" -m owner --uid-owner "${DRONEBRIDGE_USER}" -j RETURN 2>/dev/null; then
        iptables -t mangle -I OUTPUT "$@" -m owner --uid-owner "${DRONEBRIDGE_USER}" -j RETURN
        echo "[PBR] ✓ exempt: ${desc}"
    fi
}

# Dashboard HTTP replies — admin plane (main table)
ensure_mangle_return "tcp sport 8080 (dashboard)" -p tcp --sport 8080
# PX4 MAVLink LAN — eth plane (không đi table 100 / wwan0)
ensure_mangle_return "dst ${ETH_LAN_CIDR} (PX4 eth)" -d "${ETH_LAN_CIDR}"
# Loopback / local services
ensure_mangle_return "dst 127.0.0.0/8" -d 127.0.0.0/8

# ── 3. Mark remaining dronebridge uid traffic → cloud data plane ─────────────
if iptables -t mangle -C OUTPUT \
       -m owner --uid-owner "${DRONEBRIDGE_USER}" \
       -j MARK --set-mark "${MARK}" 2>/dev/null; then
    echo "[PBR] iptables MARK rule đã tồn tại (uid=${DRONEBRIDGE_USER})"
else
    iptables -t mangle -A OUTPUT \
        -m owner --uid-owner "${DRONEBRIDGE_USER}" \
        -j MARK --set-mark "${MARK}"
    echo "[PBR] ✓ iptables MARK: uid ${DRONEBRIDGE_USER} → mark ${MARK}"
fi

# ── 4. Summary ───────────────────────────────────────────────────────────────
echo ""
echo "=== PBR Setup Complete (3-plane) ==="
echo "  eth0  ${ETH_LAN_CIDR}  → exempt MARK (MAVLink PX4)"
echo "  wlan0             → admin / SSH (main table)"
echo "  wwan0|wlan0       → table ${TABLE} (cloud, marked uid)"
echo ""
ip rule show | grep -E "fwmark|local|main|default" | head -10
echo ""
iptables -t mangle -L OUTPUT -n --line-numbers | head -12

# ── 5. PX4 LAN trong table 100 (lớp an toàn khi fwmark vẫn áp dụng) ─────────
if ip -4 addr show dev "$ETH_IFACE" 2>/dev/null | grep -q "inet "; then
    ip route replace "${ETH_LAN_CIDR}" dev "$ETH_IFACE" table "${TABLE}" 2>/dev/null \
        && echo "[PBR] ✓ table ${TABLE}: ${ETH_LAN_CIDR} dev ${ETH_IFACE}"
fi
