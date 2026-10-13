#!/usr/bin/env bash
# Bắt & decode TCP auth :5770 — mặc định chạy liên tục, CHỈ in khi có gói mới.
#
#   sudo bash tcp_auth_sniff.sh           # follow (mặc định)
#   sudo bash tcp_auth_sniff.sh -c 8    # bắt N gói rồi thoát
#
# Cần: sudo apt install -y tcpdump

set -euo pipefail

HOST="${HOST:-45.117.171.237}"
PORT="${PORT:-5770}"
IFACE="${IFACE:-}"
COUNT=""
FOLLOW=true

detect_iface() {
    if [ -n "$IFACE" ]; then
        echo "$IFACE"
        return
    fi
    ip -o route get "$HOST" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}'
}

while [ $# -gt 0 ]; do
    case "$1" in
        -f|--follow) FOLLOW=true ;;
        -c|--count) COUNT="${2:?}"; FOLLOW=false; shift ;;
        -h|--help)
            sed -n '2,7p' "$0" | tr -d '#'
            exit 0
            ;;
        *) echo "Unknown: $1" >&2; exit 1 ;;
    esac
    shift
done

if [ "$EUID" -ne 0 ]; then
    echo "Chạy: sudo bash tcp_auth_sniff.sh"
    exit 1
fi

if ! command -v tcpdump >/dev/null 2>&1; then
    echo "Cài tcpdump: sudo apt install -y tcpdump"
    exit 1
fi

IFACE="$(detect_iface)"
if [ -z "$IFACE" ]; then
    echo "Chỉ định IFACE=wlan0 sudo bash $0"
    exit 1
fi

FILTER="host ${HOST} and port ${PORT}"

run_decoder() {
    python3 - "$@" <<'PY'
import struct, sys, datetime, os

MSG = {
    0x01: "AUTH_INIT",
    0x02: "AUTH_CHALLENGE",
    0x03: "AUTH_RESPONSE",
    0x04: "AUTH_ACK",
    0x12: "SESSION_REFRESH",
    0x13: "SESSION_REFRESH_ACK",
    0xB0: "VPN_PROVISION_REQUEST",
    0xB1: "VPN_PROVISION_ACK",
    0xA0: "REGISTER_INIT",
    0xA1: "REGISTER_CHALLENGE",
    0xA2: "REGISTER_RESPONSE",
    0xA3: "REGISTER_ACK",
}

def read_str(data, offset):
    if offset + 2 > len(data):
        return None, offset
    ln = struct.unpack_from("<H", data, offset)[0]
    offset += 2
    if offset + ln > len(data):
        return None, offset
    return data[offset:offset + ln], offset + ln

def parse_ipv4_tcp(pkt):
    if len(pkt) < 14:
        return None
    eth_type = struct.unpack("!H", pkt[12:14])[0]
    off = 14
    if eth_type == 0x8100:
        if len(pkt) < 18:
            return None
        eth_type = struct.unpack("!H", pkt[16:18])[0]
        off = 18
    if eth_type != 0x0800:
        return None
    ip = pkt[off:]
    if len(ip) < 20:
        return None
    ihl = (ip[0] & 0x0F) * 4
    if ip[9] != 6:
        return None
    src = ".".join(str(b) for b in ip[12:16])
    dst = ".".join(str(b) for b in ip[16:20])
    tcp = ip[ihl:]
    if len(tcp) < 20:
        return None
    doff = ((tcp[12] >> 4) & 0x0F) * 4
    payload = tcp[doff:]
    if not payload:
        return None
    sport = struct.unpack("!H", tcp[0:2])[0]
    dport = struct.unpack("!H", tcp[2:4])[0]
    return src, sport, dst, dport, payload

def decode_payload(payload, direction):
    mt = payload[0]
    name = MSG.get(mt, f"UNKNOWN_0x{mt:02X}")
    lines = [f"  [{direction}] 0x{mt:02X} {name}  {len(payload)}B"]
    off = 1
    if mt in (0x01, 0xA0):
        s, _ = read_str(payload, off)
        if s:
            lines.append(f"    uuid={s.decode('utf-8', 'replace')}")
    elif mt == 0x03:
        s, off = read_str(payload, off)
        if s:
            lines.append(f"    uuid={s.decode('utf-8', 'replace')}")
        if off + 2 <= len(payload):
            hlen = struct.unpack_from("<H", payload, off)[0]
            lines.append(f"    hmac_len={hlen}")
    elif mt == 0x04:
        if len(payload) >= 2:
            if payload[1] == 0:
                tok, _ = read_str(payload, 2)
                if tok:
                    lines.append(f"    result=OK token_len={len(tok)}")
            else:
                lines.append(f"    result=FAIL error={payload[2] if len(payload)>2 else 0}")
    elif mt == 0x12:
        tok, off2 = read_str(payload, off)
        if tok:
            uuid, _ = read_str(payload, off2)
            lines.append(f"    token_len={len(tok)}")
            if uuid:
                lines.append(f"    uuid={uuid.decode('utf-8', 'replace')}")
    elif mt == 0x13:
        lines.append(f"    result={'OK' if len(payload)>1 and payload[1]==0 else 'FAIL'}")
    return "\n".join(lines)

def pcap_endian(path):
    with open(path, "rb") as f:
        magic = f.read(4)
    return "<" if magic in (b"\xd4\xc3\xb2\xa1", b"\x4d\x3c\xb2\xa1") else ">"

def iter_pcap(path, offset):
    if not os.path.exists(path) or os.path.getsize(path) < 24:
        return offset, []
    out = []
    endian = pcap_endian(path)
    host = sys.argv[2]
    port = int(sys.argv[3])
    with open(path, "rb") as f:
        if offset == 0:
            if f.read(24) is None or os.path.getsize(path) < 24:
                return 0, []
            offset = 24
        else:
            f.seek(offset)
        while True:
            pos = f.tell()
            ph = f.read(16)
            if len(ph) < 16:
                break
            ts_sec, ts_usec, incl_len, _ = struct.unpack(endian + "IIII", ph)
            data = f.read(incl_len)
            if len(data) < incl_len:
                f.seek(pos)
                break
            offset = f.tell()
            tstr = datetime.datetime.fromtimestamp(ts_sec + ts_usec / 1e6).strftime("%H:%M:%S")
            parsed = parse_ipv4_tcp(data)
            if not parsed:
                continue
            src, sport, dst, dport, payload = parsed
            if not ((dst == host and dport == port) or (src == host and sport == port)):
                continue
            if dst == host and dport == port:
                flow, direction = f"{src}:{sport} → {dst}:{dport}", "OUT ↑"
            else:
                flow, direction = f"{src}:{sport} → {dst}:{dport}", "IN ↓"
            block = f"\n── {tstr} {flow} ──\n" + decode_payload(payload, direction)
            out.append(block)
    return offset, out

mode = sys.argv[1]
path = sys.argv[4]
host = sys.argv[2]
port = int(sys.argv[3])

if mode == "once":
    off, blocks = iter_pcap(path, 0)
    if blocks:
        print("".join(blocks))
    else:
        print("(chưa có gói — đợi thêm hoặc restart dronebridge)")
elif mode == "follow":
    off_file = path + ".off"
    off = 0
    if os.path.exists(off_file):
        try:
            off = int(open(off_file).read().strip())
        except Exception:
            off = 0
    off, blocks = iter_pcap(path, off)
    open(off_file, "w").write(str(off))
    if blocks:
        print("".join(blocks), end="", flush=True)
PY
}

echo "============================================================"
echo " TCP decode — ${HOST}:${PORT}  iface=${IFACE}"
if $FOLLOW; then
    echo " Chế độ: theo dõi liên tục — chỉ in khi có gói mới (Ctrl+C dừng)"
else
    echo " Chế độ: bắt ${COUNT} gói rồi thoát"
fi
echo "============================================================"
echo "$(date '+%H:%M:%S')  đang lắng nghe..."

PCAP="$(mktemp /tmp/drone_auth_XXXXXX.pcap)"
OFFSET_FILE="${PCAP}.off"
cleanup() { rm -f "$PCAP" "$OFFSET_FILE" 2>/dev/null; kill "${TCPDUMP_PID:-}" 2>/dev/null; }
trap cleanup EXIT

if $FOLLOW; then
    tcpdump -i "$IFACE" -nn -s 0 -w "$PCAP" -U "$FILTER" 2>/dev/null &
    TCPDUMP_PID=$!
    while true; do
        run_decoder follow "$HOST" "$PORT" "$PCAP"
        sleep 0.5
    done
else
    timeout 60 tcpdump -i "$IFACE" -nn -s 0 -w "$PCAP" -c "${COUNT:-8}" "$FILTER" 2>/dev/null || true
    run_decoder once "$HOST" "$PORT" "$PCAP"
fi
