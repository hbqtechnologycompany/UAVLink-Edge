#!/usr/bin/env bash
# Theo dõi TCP auth :5770 — CHỈ in dòng khi có bytes/packet (không spam 0 B/s).
#
#   bash tcp_auth_rate.sh
#   INTERVAL=1 bash tcp_auth_rate.sh

set -euo pipefail

HOST="${1:-45.117.171.237}"
PORT="${2:-5770}"
INTERVAL="${INTERVAL:-1}"
SHOW_IDLE="${SHOW_IDLE:-0}"   # SHOW_IDLE=1 để in cả lúc 0 B/s

fmt_rate() {
    local bps="$1"
    awk -v b="$bps" 'BEGIN {
        if (b < 1024) printf "%.0f B/s", b;
        else if (b < 1048576) printf "%.2f KB/s", b/1024;
        else printf "%.2f MB/s", b/1048576;
    }'
}

read_tcp_stats() {
    local block
    block=$(ss -tipn 2>/dev/null | grep -A1 "${HOST}:${PORT}" | grep 'bytes_sent:' || true)
    if [ -z "$block" ]; then
        echo "NOCONN"
        return
    fi
    local sent recv seg_out seg_in kern_bps
    sent=$(sed -n 's/.*bytes_sent:\([0-9]*\).*/\1/p' <<<"$block")
    recv=$(sed -n 's/.*bytes_received:\([0-9]*\).*/\1/p' <<<"$block")
    seg_out=$(sed -n 's/.*data_segs_out:\([0-9]*\).*/\1/p' <<<"$block")
    seg_in=$(sed -n 's/.*data_segs_in:\([0-9]*\).*/\1/p' <<<"$block")
    kern_bps=$(sed -n 's/.* send \([0-9]*\)bps.*/\1/p' <<<"$block")
    echo "${sent:-0} ${recv:-0} ${seg_out:-0} ${seg_in:-0} ${kern_bps:-0}"
}

echo "============================================================"
echo " TCP monitor — ${HOST}:${PORT}  (chỉ in khi có data)"
echo " SESSION_REFRESH ~mỗi 30s  |  Ctrl+C dừng"
echo "============================================================"
printf "%-8s %12s %12s %7s %7s %14s %12s\n" \
    "TIME" "OUT" "IN" "pkt↑/s" "pkt↓/s" "kernel OUT" "TOTAL out"
echo "$(date '+%H:%M:%S')  ... đang chờ traffic TCP (idle giữa các refresh là bình thường)"

prev_sent=0
prev_recv=0
prev_seg_out=0
prev_seg_in=0
first=1
was_noconn=0

while true; do
    stats=$(read_tcp_stats)
    if [ "$stats" = "NOCONN" ]; then
        if [ "$was_noconn" -eq 0 ]; then
            printf "%-8s  ✗ mất kết nối TCP %s:%s\n" "$(date '+%H:%M:%S')" "$HOST" "$PORT"
            was_noconn=1
            first=1
        fi
        prev_sent=0
        prev_recv=0
        prev_seg_out=0
        prev_seg_in=0
        sleep "$INTERVAL"
        continue
    fi
    was_noconn=0

    read -r sent recv seg_out seg_in kern_bps <<<"$stats"

    if [ "$first" -eq 1 ]; then
        prev_sent=$sent
        prev_recv=$recv
        prev_seg_out=$seg_out
        prev_seg_in=$seg_in
        first=0
        sleep "$INTERVAL"
        continue
    fi

    dt_out=$((sent - prev_sent))
    dt_in=$((recv - prev_recv))
    dpkt_out=$((seg_out - prev_seg_out))
    dpkt_in=$((seg_in - prev_seg_in))

    if [ "$dt_out" -gt 0 ] || [ "$dt_in" -gt 0 ] || [ "$dpkt_out" -gt 0 ] || [ "$dpkt_in" -gt 0 ] || [ "$SHOW_IDLE" = "1" ]; then
        if [ "$dt_out" -eq 0 ] && [ "$dt_in" -eq 0 ] && [ "$SHOW_IDLE" != "1" ]; then
            : # skip pure ack-only if no byte change — rare
        else
            rate_out=$(awk -v d="$dt_out" -v t="$INTERVAL" 'BEGIN { printf "%.0f", d/t }')
            rate_in=$(awk -v d="$dt_in" -v t="$INTERVAL" 'BEGIN { printf "%.0f", d/t }')
            rate_pkt_out=$(awk -v d="$dpkt_out" -v t="$INTERVAL" 'BEGIN { printf "%.1f", d/t }')
            rate_pkt_in=$(awk -v d="$dpkt_in" -v t="$INTERVAL" 'BEGIN { printf "%.1f", d/t }')
            kern_fmt="—"
            if [ "${kern_bps:-0}" -gt 0 ] 2>/dev/null; then
                kern_fmt=$(fmt_rate "$(awk -v b="$kern_bps" 'BEGIN { print b/8 }')")
            fi
            total_fmt=$(numfmt --to=iec "$sent" 2>/dev/null || echo "${sent}B")
            printf "%-8s %12s %12s %7s %7s %14s %12s\n" \
                "$(date '+%H:%M:%S')" \
                "$(fmt_rate "$rate_out")" \
                "$(fmt_rate "$rate_in")" \
                "$rate_pkt_out" \
                "$rate_pkt_in" \
                "$kern_fmt" \
                "$total_fmt"
        fi
    fi

    prev_sent=$sent
    prev_recv=$recv
    prev_seg_out=$seg_out
    prev_seg_in=$seg_in
    sleep "$INTERVAL"
done
