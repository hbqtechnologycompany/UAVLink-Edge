#!/bin/bash
# TELEM UART cho PX4 — gọi từ install.sh và dronebridge-uart-setup.service (mỗi lần boot).
# CM5:
#   UART2 → /dev/ttyAMA2: TX GPIO4, RX GPIO5, CTS GPIO6, RTS GPIO7
#   UART3 → /dev/ttyAMA3: TX GPIO8, RX GPIO9, CTS GPIO10, RTS GPIO11
# WalnutPi CM2 (cùng đế CM5): chỉ một cổng
#   UART2 → /dev/ttyS2: J23 chân 4 = PB0 TX, chân 5 = PB1 RX. Không CTS/RTS, không TELEM3.
# Tất cả 1500000 8N1. Flow control phần cứng tắt (termios không bật CRTSCTS).
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

# shellcheck source=../lib/board.sh
source "$SCRIPT_DIR/../lib/board.sh"
if [ "$(dronebridge_board)" = walnutpi ]; then
    # GPIO4-11 của đế CM5 không trùng UART nào của T527; TELEM2 lấy UART2 trên J23.
    dronebridge_strip_boot_block "$BOOT_CONFIG" "$BLOCK_MARK" "dtoverlay=uart2-pi5" "dtoverlay=uart3-pi5"
    DEV=/dev/ttyS2
    rc=0
    walnutpi_ensure_overlay uart2 /soc@3000000/uart@2500800 status okay || rc=$?
    if [ "$rc" = 1 ]; then
        echo "FAIL  WalnutPi UART2 overlay"
        exit 1
    fi
    dronebridge_udev_rule 99-zz-dronebridge-uart.rules 'KERNEL=="ttyS2", GROUP="dialout", MODE="0660"'
    dronebridge_add_service_group dialout
    CONFIG="$INSTALL_DIR/config.yaml"
    dronebridge_config_set "$CONFIG" network telem2_port "$DEV"
    dronebridge_config_set "$CONFIG" network serial_port "$DEV"
    dronebridge_config_set "$CONFIG" network telem3_port '""'
    if [ "$rc" = 0 ] && [ -e "$DEV" ]; then
        chgrp dialout "$DEV" && chmod 0660 "$DEV"
        dronebridge_clear_reboot_reason "$INSTALL_DIR" uart
        echo "✅ TELEM2 $DEV (UART2, J23 chân 4 TX / chân 5 RX)"
    else
        dronebridge_add_reboot_reason "$INSTALL_DIR" uart
        echo "⚠️  TELEM2 $DEV chưa có — reboot để kernel nạp DTB mới"
    fi
    exit 0
fi

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
