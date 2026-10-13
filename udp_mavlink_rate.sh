#!/usr/bin/env bash
# Theo dõi tốc độ MAVLink UDP uplink (CM5 → server qua VPN 10.8.0.1:14550).
# Kernel ss không thấy byte VPN userspace → dùng /api/status (sent_packets).
#
#   bash udp_mavlink_rate.sh
#   INTERVAL=1 bash udp_mavlink_rate.sh
#   API_URL=http://127.0.0.1:8080/api/status bash udp_mavlink_rate.sh
#
# Ước lượng KB/s: AVG_BYTES_PER_MSG (mặc định 48 byte/msg MAVLink v2)

set -euo pipefail

INTERVAL="${INTERVAL:-1}"
API_URL="${API_URL:-http://127.0.0.1:8080/api/status}"
AVG_BYTES_PER_MSG="${AVG_BYTES_PER_MSG:-48}"
TARGET_HOST="${TARGET_HOST:-10.8.0.1}"
TARGET_PORT="${TARGET_PORT:-14550}"

fmt_rate() {
    local bps="$1"
    awk -v b="$bps" 'BEGIN {
        if (b < 1024) printf "%.0f B/s", b;
        else if (b < 1048576) printf "%.2f KB/s", b/1024;
        else printf "%.2f MB/s", b/1048576;
    }'
}

read_api() {
    python3 - "$API_URL" <<'PY'
import json, sys, urllib.request
url = sys.argv[1]
try:
    with urllib.request.urlopen(url, timeout=3) as r:
        d = json.load(r)
except Exception as e:
    print("ERROR", e, sep="\t")
    sys.exit(0)
sent = sum((d.get("sent_packets") or {}).values())
print(
    sent,
    d.get("auth_status", "?"),
    d.get("current_ip", "?"),
    sep="\t",
)
PY
}

read_pkt_sec() {
    # Nếu log.show_packet_stats: true — lấy outServerForward/s từ journal
    journalctl -u dronebridge --since "${INTERVAL} sec ago" --no-pager 2>/dev/null \
        | grep 'PKT_SEC\[UPLINK\]' | tail -1 \
        | sed -n 's/.*outServerForward\/s=\([0-9]*\).*/\1/p' || true
}

echo "============================================================"
echo " UDP MAVLink monitor — uplink VPN → ${TARGET_HOST}:${TARGET_PORT}"
echo " Nguồn: DroneBridge API (sent_packets)  INTERVAL=${INTERVAL}s"
echo " Est. bytes/s ≈ msg/s × ${AVG_BYTES_PER_MSG} B  (Ctrl+C dừng)"
echo " Lưu ý: không phải TCP :5770 — telemetry đi UDP qua WireGuard"
echo "============================================================"
printf "%-8s %10s %12s %10s %12s %s\n" \
    "TIME" "OUT msg/s" "OUT (est.)" "pkt/s*" "TOTAL msg" "auth / VPN IP"

prev_sent=""
first=1

while true; do
    line=$(read_api)
    if [[ "$line" == ERROR* ]]; then
        err=$(cut -f2- <<<"$line")
        printf "%-8s  API lỗi: %s\n" "$(date '+%H:%M:%S')" "$err"
        sleep "$INTERVAL"
        continue
    fi

    IFS=$'\t' read -r sent auth vpn_ip <<<"$line"
    pkt_sec=$(read_pkt_sec)

    if [ "$first" -eq 1 ]; then
        printf "%-8s %10s %12s %10s %12s %s\n" \
            "$(date '+%H:%M:%S')" "..." "..." "..." "$sent" "${auth} / ${vpn_ip}"
        first=0
        prev_sent=$sent
        sleep "$INTERVAL"
        continue
    fi

    dmsg=$((sent - prev_sent))
    rate_msg=$(awk -v d="$dmsg" -v t="$INTERVAL" 'BEGIN { printf "%.1f", d/t }')
    rate_bytes=$(awk -v m="$rate_msg" -v b="$AVG_BYTES_PER_MSG" 'BEGIN { printf "%.0f", m*b }')
    pkt_display="${pkt_sec:-—}"
    if [ -n "$pkt_sec" ] && [ "$pkt_sec" != "—" ]; then
        pkt_display="$pkt_sec"
    fi

    printf "%-8s %10s %12s %10s %12s %s\n" \
        "$(date '+%H:%M:%S')" \
        "$rate_msg" \
        "$(fmt_rate "$rate_bytes")" \
        "$pkt_display" \
        "$sent" \
        "${auth} / ${vpn_ip}"

    prev_sent=$sent
    sleep "$INTERVAL"
done
