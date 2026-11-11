#!/bin/bash
# Bật I2C cho LCD SH1107 (0x3C).
#   CM5:      I2C3 — GPIO22=SDA, GPIO23=SCL (dtoverlay=i2c3-pi5,pins_22_23) → /dev/i2c-3
#   WalnutPi: TWI4 — J23 chân 6 (PI1) và chân 8 (PI0), overlay "i2c4"     → /dev/i2c-4
# Gọi: setup_lcd_i2c.sh [/opt/dronebridge/config.yaml]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
CONFIG="${1:-$INSTALL_DIR/config.yaml}"
BOOT_CONFIG="/boot/firmware/config.txt"
[ -f "$BOOT_CONFIG" ] || BOOT_CONFIG="/boot/config.txt"
MARKER="$INSTALL_DIR/.need_lcd_reboot"
TMP="${INSTALL_DIR}/.lcd_boot_config.tmp"

enable_i2c_dev() {
    local bus="$1"
    mkdir -p /etc/modules-load.d
    echo "i2c-dev" > /etc/modules-load.d/dronebridge-i2c.conf
    modprobe i2c-dev 2>/dev/null || true
    sleep 0.3
    if [ -e "/dev/i2c-${bus}" ]; then
        echo "✅ /dev/i2c-${bus} sẵn sàng"
        return 0
    fi
    if [ -d "/sys/bus/i2c/devices/i2c-${bus}" ]; then
        echo "⚠️  i2c-${bus} trong sysfs — đã load i2c-dev, chờ udev..."
        udevadm settle 2>/dev/null || sleep 1
        [ -e "/dev/i2c-${bus}" ] && echo "✅ /dev/i2c-${bus} sẵn sàng" && return 0
        echo "⚠️  Chưa có /dev/i2c-${bus} sau modprobe i2c-dev"
        return 1
    fi
    echo "⚠️  Chưa thấy i2c-${bus} trong sysfs — cần reboot sau dtoverlay"
    return 1
}

read -r AUTO_SETUP OVERLAY PINS BUS <<< "$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f) or {}
lcd = cfg.get("lcd") or {}
ov = lcd.get("overlay") or "i2c3-pi5"
pins = lcd.get("pins") or "pins_22_23"
bus = int(lcd.get("bus") or 3)
auto = lcd.get("auto_setup", True) and lcd.get("enabled", True)
print("1" if auto else "0", ov, pins, bus)
PY
)"

# shellcheck source=../lib/board.sh
source "$SCRIPT_DIR/../lib/board.sh"
if [ "$(dronebridge_board)" = walnutpi ]; then
    # GPIO22/23 trên CM2 là PI11/PI10 (không có I2C); LCD chuyển sang TWI4 trên J23.
    # Hai chân J23 6/8 dành cố định cho I2C4 trên WalnutPi, bật kể cả khi lcd.enabled=false.
    dronebridge_strip_boot_block "$BOOT_CONFIG" "# DroneBridge: LCD I2C3" "dtoverlay=i2c3"
    rm -f "$TMP"
    BUS=4
    rc=0
    walnutpi_ensure_overlay i2c4 /soc@3000000/twi4@2503000 status okay || rc=$?
    if [ "$rc" = 1 ]; then
        echo "FAIL  WalnutPi I2C4 overlay"
        exit 1
    fi
    dronebridge_udev_rule 99-zz-dronebridge-i2c.rules 'SUBSYSTEM=="i2c-dev", GROUP="i2c", MODE="0660"'
    dronebridge_add_service_group i2c
    dronebridge_config_set "$CONFIG" lcd bus "$BUS"
    dronebridge_config_set "$CONFIG" lcd overlay i2c4
    dronebridge_config_set "$CONFIG" lcd pins '""'
    if [ "$rc" = 0 ] && enable_i2c_dev "$BUS"; then
        chgrp i2c "/dev/i2c-${BUS}" && chmod 0660 "/dev/i2c-${BUS}"
        rm -f "$MARKER"
        dronebridge_clear_reboot_reason "$INSTALL_DIR" lcd
        echo "✅ LCD /dev/i2c-${BUS} (TWI4, J23 chân 6/8)"
    else
        touch "$MARKER"
        dronebridge_add_reboot_reason "$INSTALL_DIR" lcd
        echo "⚠️  /dev/i2c-${BUS} chưa có — reboot để kernel nạp DTB mới"
    fi
    exit 0
fi

# config.yaml ghi trên WalnutPi (overlay i2c4, pins rỗng, bus 4) → giá trị CM5, khớp applyBoardProfile.
if [ "$OVERLAY" = i2c4 ] || [ "$BUS" = 4 ]; then
    OVERLAY=i2c3-pi5
    PINS=pins_22_23
    BUS="$(dronebridge_config_lcd_bus "$CONFIG")"
    dronebridge_config_set "$CONFIG" lcd overlay "$OVERLAY"
    dronebridge_config_set "$CONFIG" lcd pins "$PINS"
    dronebridge_config_set "$CONFIG" lcd bus "$BUS"
fi

if [ "$AUTO_SETUP" != "1" ]; then
    echo "LCD I2C auto_setup tắt — bỏ qua"
    rm -f "$MARKER"
    exit 0
fi

# Luôn load i2c-dev trước (không phụ thuộc ghi config.txt)
enable_i2c_dev "$BUS" || true

if [ ! -f "$BOOT_CONFIG" ]; then
    echo "Không tìm thấy boot config — bỏ qua overlay"
    exit 0
fi

OVERLAY_LINE="dtoverlay=${OVERLAY},${PINS}"
if grep -qF "$OVERLAY_LINE" "$BOOT_CONFIG" 2>/dev/null \
    && grep -q "# DroneBridge: LCD I2C3" "$BOOT_CONFIG" 2>/dev/null; then
    echo "✅ LCD I2C boot config đã đúng — không đổi"
    rm -f "$MARKER" "$TMP"
    if [ -f "$INSTALL_DIR/.need_host_reboot" ]; then
        grep -v '^lcd$' "$INSTALL_DIR/.need_host_reboot" > "${INSTALL_DIR}/.need_host_reboot.tmp" 2>/dev/null || true
        mv "${INSTALL_DIR}/.need_host_reboot.tmp" "$INSTALL_DIR/.need_host_reboot" 2>/dev/null || rm -f "$INSTALL_DIR/.need_host_reboot"
        [ ! -s "$INSTALL_DIR/.need_host_reboot" ] && rm -f "$INSTALL_DIR/.need_host_reboot"
    fi
    exit 0
fi

python3 - "$BOOT_CONFIG" "$TMP" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
marker = "# DroneBridge: LCD I2C3"
out = []
skip = False
with open(src) as f:
    for line in f:
        s = line.rstrip("\n")
        if s.startswith(marker):
            skip = True
            continue
        if skip:
            if s.startswith("dtoverlay=i2c3") or s.startswith("dtoverlay=i2c3-pi5"):
                continue
            skip = False
        if s.startswith("dtoverlay=i2c3-pi5,pins_22_23") or s.startswith("dtoverlay=i2c3,pins_22_23"):
            continue
        out.append(s)
while out and out[-1] == "":
    out.pop()
with open(dst, "w") as f:
    f.write("\n".join(out) + "\n")
PY

cp "$BOOT_CONFIG" "${BOOT_CONFIG}.bak.$(date +%Y%m%d_%H%M%S)"
mv "$TMP" "$BOOT_CONFIG"
{
    echo ""
    echo "# DroneBridge: LCD I2C3 GPIO22(SDA) GPIO23(SCL) -> /dev/i2c-${BUS}"
    echo "$OVERLAY_LINE"
} >> "$BOOT_CONFIG"

touch "$MARKER"
echo "lcd" >> "$INSTALL_DIR/.need_host_reboot"
sort -u "$INSTALL_DIR/.need_host_reboot" -o "$INSTALL_DIR/.need_host_reboot" 2>/dev/null || true
echo "✅ LCD I2C3 overlay đã cập nhật — reboot để áp dụng"
grep -n 'LCD I2C3\|i2c3-pi5' "$BOOT_CONFIG" | tail -4 || true
