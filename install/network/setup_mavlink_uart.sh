#!/bin/bash
# TELEM2 / TELEM3 trên CM5 — UART 4 chân, gọi từ install.sh và dronebridge-uart-setup.service.
# UART2 → /dev/ttyAMA2: TX GPIO4, RX GPIO5, CTS GPIO6, RTS GPIO7
# UART3 → /dev/ttyAMA3: TX GPIO8, RX GPIO9, CTS GPIO10, RTS GPIO11
# Cả hai 1500000 8N1. Flow control phần cứng tắt (termios không bật CRTSCTS).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
BOOT_CONFIG="/boot/firmware/config.txt"
[ -f "$BOOT_CONFIG" ] || BOOT_CONFIG="/boot/config.txt"
MARKER="$INSTALL_DIR/.need_host_reboot"
BLOCK_MARK="# DroneBridge: MAVLink UART2/UART3 4-wire"
LINE2="dtoverlay=uart2-pi5,ctsrts"
LINE3="dtoverlay=uart3-pi5,ctsrts"

if [ ! -f "$BOOT_CONFIG" ]; then
    echo "Không thấy boot config — bỏ qua UART"
    exit 0
fi

config_ok=0
if grep -qxF "$LINE2" "$BOOT_CONFIG" && grep -qxF "$LINE3" "$BOOT_CONFIG"; then
    config_ok=1
fi

if [ "$config_ok" != 1 ]; then
    python3 - "$BOOT_CONFIG" "$BLOCK_MARK" "$LINE2" "$LINE3" <<'PY'
import os, sys
path, marker, line2, line3 = sys.argv[1:]
with open(path) as f:
    lines = [ln.rstrip("\n") for ln in f]
kept = []
skip = False
for s in lines:
    if s.startswith("# DroneBridge: MAVLink UART"):
        skip = True
        continue
    if skip:
        if s.startswith("dtoverlay=uart2") or s.startswith("dtoverlay=uart3"):
            continue
        skip = False
    if s.startswith("dtoverlay=uart2") or s.startswith("dtoverlay=uart3"):
        continue
    kept.append(s)
while kept and kept[-1] == "":
    kept.pop()
text = "\n".join(kept + ["", marker, line2, line3]) + "\n"
bak = path + ".bak.uart"
if not os.path.exists(bak):
    with open(path) as src, open(bak, "w") as dst:
        dst.write(src.read())
tmp = path + ".dronebridge-uart.tmp"
with open(tmp, "w") as f:
    f.write(text)
os.replace(tmp, path)
print("boot config: UART2/UART3 4 chân (TX RX CTS RTS)")
PY
fi

apply_runtime() {
    local name="$1"
    local dev="$2"
    if dtoverlay -l 2>/dev/null | grep -F "$name" | grep -q ctsrts && [ -e "$dev" ]; then
        echo "✅ $dev ($name,ctsrts)"
        return 0
    fi
    dtoverlay -r "$name" >/dev/null 2>&1 || true
    if dtoverlay "$name" ctsrts >/dev/null 2>&1; then
        sleep 0.3
    fi
    if [ -e "$dev" ]; then
        echo "✅ $dev sau dtoverlay $name ctsrts"
        return 0
    fi
    echo "⚠️  Chưa có $dev — reboot để kernel nạp $name,ctsrts"
    return 1
}

ok2=0
ok3=0
apply_runtime uart2-pi5 /dev/ttyAMA2 && ok2=1 || true
apply_runtime uart3-pi5 /dev/ttyAMA3 && ok3=1 || true

mkdir -p "$INSTALL_DIR"
if [ "$ok2" = 1 ] && [ "$ok3" = 1 ]; then
    if [ -f "$MARKER" ]; then
        grep -v '^uart$' "$MARKER" > "${MARKER}.tmp" 2>/dev/null || true
        mv "${MARKER}.tmp" "$MARKER" 2>/dev/null || rm -f "$MARKER"
        [ ! -s "$MARKER" ] && rm -f "$MARKER"
    fi
    exit 0
fi

echo "uart" >> "$MARKER"
sort -u "$MARKER" -o "$MARKER" 2>/dev/null || true
exit 0
