#!/bin/bash
# WalnutPi CM2 — cam0 IMX219 trên VIN (CSI0, 2-lane RAW10) + ISP hãng (walnutpi.isp).
# Gọi qua setup_camera.sh (cùng tham số):
#   setup_camera.sh [config.yaml]        cài / sửa tại chỗ (root)
#   setup_camera.sh --check [config]     chỉ kiểm tra; exit 1 nếu còn thiếu hoặc chờ reboot
#   setup_camera.sh --boot-settled [cfg] như cài, gọi sau boot
# Mỗi bước idempotent: đúng rồi thì không đổi gì, không đòi reboot.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../../lib/root.sh
source "$SCRIPT_DIR/../../lib/root.sh"
# shellcheck source=../../lib/board.sh
source "$SCRIPT_DIR/../../lib/board.sh"
INSTALL_DIR="$(dronebridge_root)"
SRC="$SCRIPT_DIR/imx219"
MARKER="$INSTALL_DIR/.need_camera_reboot"

CHECK_ONLY=0
CONFIG="$INSTALL_DIR/config.yaml"
case "${1:-}" in
    --check) CHECK_ONLY=1; CONFIG="${2:-$CONFIG}" ;;
    --boot-settled) CONFIG="${2:-$CONFIG}" ;;
    --fix-owner) exit 0 ;;
    "") ;;
    *) CONFIG="$1" ;;
esac

KVER="$(uname -r)"
MOD_DST="/lib/modules/$KVER/kernel/bsp/drivers/vin/modules/sensor/imx219.ko"
PREFIX="$(sed -n 's/^overlay_prefix=//p' /boot/config.txt | head -1)"
DTBO_DST="/boot/overlays/${PREFIX}-imx219.dtbo"
SENSOR_NODE=/soc@3000000/vind@5800800/sensor@5812000
SENSOR_LIST_NODE=/soc@3000000/vind@5800800/sensor_list@5812040
MODLOAD=/etc/modules-load.d/dronebridge-imx219.conf
MODPROBE=/etc/modprobe.d/dronebridge-imx219.conf

camera_enabled() {
    python3 - "$CONFIG" <<'PY'
import sys, yaml
cam = (yaml.safe_load(open(sys.argv[1])) or {}).get("camera") or {}
sys.exit(0 if cam.get("enabled", True) and cam.get("auto_setup", True) else 1)
PY
}

# Mã máy (.text/.rodata) giống nhau là cùng driver; đường dẫn build khác không tính.
ko_same() {
    [ -f "$1" ] && [ -f "$2" ] || return 1
    cmp -s "$1" "$2" && return 0
    command -v objcopy >/dev/null 2>&1 || return 1
    local t
    t="$(mktemp -d)"
    objcopy -O binary -j .text "$1" "$t/a.text" && objcopy -O binary -j .text "$2" "$t/b.text" &&
        objcopy -O binary -j .rodata "$1" "$t/a.ro" && objcopy -O binary -j .rodata "$2" "$t/b.ro" &&
        cmp -s "$t/a.text" "$t/b.text" && cmp -s "$t/a.ro" "$t/b.ro"
    local rc=$?
    rm -rf "$t"
    return $rc
}

dtb_has_imx219() {
    local dtb
    dtb="$(walnutpi_boot_dtb)"
    [ "$(fdtget "$dtb" "$SENSOR_NODE" sensor0_mname 2>/dev/null)" = imx219 ] &&
        [ "$(fdtget "$dtb" "$SENSOR_LIST_NODE" sensor00_mname 2>/dev/null)" = imx219 ]
}

# Kernel đang chạy đã nhận IMX219 (driver nạp + VIN có /dev/video0).
runtime_ok() {
    grep -q '^imx219 ' /proc/modules && [ -e /dev/video0 ]
}

# ── Kiểm tra (không đổi hệ thống) ────────────────────────────────────────────
if [ "$CHECK_ONLY" = 1 ]; then
    miss=()
    ko_same "$SRC/imx219.ko" "$MOD_DST" || miss+=("driver")
    dtb_has_imx219 || miss+=("DTB")
    [ -f "$MODLOAD" ] || miss+=("modules-load")
    runtime_ok || miss+=("kernel chưa nạp imx219 / chưa có /dev/video0")
    if [ ${#miss[@]} -eq 0 ]; then
        echo "✅ WalnutPi IMX219: driver, DTB, /dev/video0 đã sẵn sàng"
        exit 0
    fi
    echo "⚠️  WalnutPi IMX219 còn thiếu: ${miss[*]}"
    exit 1
fi

if ! camera_enabled; then
    echo "Camera tắt hoặc auto_setup tắt — bỏ qua IMX219"
    exit 0
fi

changed=0

# Khối overlay camera kiểu Raspberry Pi (nếu bản cài cũ đã ghi) không có tác dụng trên T527.
dronebridge_strip_boot_block /boot/config.txt "# DroneBridge: camera" \
    "camera_auto_detect=" "dtparam=cam" "dtparam=i2c_csi" \
    "dtoverlay=imx" "dtoverlay=ov" "dtoverlay=arducam"

echo "▶ [1/5] Driver imx219.ko"
want="$(/sbin/modinfo -F vermagic "$SRC/imx219.ko" | awk '{print $1}')"
if [ "$want" != "$KVER" ]; then
    echo "   ✗ imx219.ko build cho $want, kernel đang chạy $KVER — dựng lại: bash $SRC/build.sh"
    exit 1
fi
if ko_same "$SRC/imx219.ko" "$MOD_DST"; then
    echo "   = đã cài"
else
    install -D -m 0644 "$SRC/imx219.ko" "$MOD_DST"
    depmod -a "$KVER"
    changed=1
    echo "   + $MOD_DST"
fi

echo "▶ [2/5] Overlay imx219 → DTB boot"
if ! cmp -s "$SRC/sun55i-t527-imx219.dtbo" "$DTBO_DST"; then
    install -m 0644 "$SRC/sun55i-t527-imx219.dtbo" "$DTBO_DST"
fi
if dtb_has_imx219 && walnutpi_overlay_listed imx219; then
    echo "   = DTB đã có imx219 (sensor0 + sensor_list0)"
else
    dtb="$(walnutpi_boot_dtb)"
    cp -a "$dtb" "$dtb.bak.imx219"
    set-device enable imx219 >/dev/null
    if ! dtb_has_imx219; then
        echo "   ✗ DTB vẫn chưa có imx219 — khôi phục $dtb.bak.imx219"
        cp -a "$dtb.bak.imx219" "$dtb"
        exit 1
    fi
    changed=1
    echo "   + $dtb: sensor0 / sensor00 = imx219 @ 0x20"
fi

echo "▶ [3/5] Nạp module khi boot"
# vin.conf thuộc gói libisp-t527 (nâng cấp gói ghi đè lại ov5647_mipi), nên dùng file riêng.
# File này xếp trước vin.conf nên imx219 nạp trước vin_v4l2; ov5647_mipi bị chặn hẳn.
# Chỉ có hiệu lực từ lần boot sau; không tự nó đòi reboot khi kernel đang chạy đã đúng.
for pair in "$MODLOAD|imx219" "$MODPROBE|install ov5647_mipi /bin/true"; do
    f="${pair%%|*}"
    line="${pair#*|}"
    if [ "$(cat "$f" 2>/dev/null)" = "$line" ]; then
        echo "   = $f"
    else
        mkdir -p "$(dirname "$f")"
        echo "$line" > "$f"
        echo "   + $f"
    fi
done

echo "▶ [4/5] Quyền /dev/video* /dev/media* cho nhóm video"
rm -f /etc/udev/rules.d/90-vin-video.rules
dronebridge_udev_rule 99-zz-vin-video.rules 'SUBSYSTEM=="video4linux", GROUP="video", MODE="0660"
SUBSYSTEM=="media", GROUP="video", MODE="0660"'
dronebridge_add_service_group video

echo "▶ [5/5] ISP hãng (AE/AWB)"
missing_pkg=()
for p in libisp-t527 walnutpi.isp; do
    dpkg -s "$p" >/dev/null 2>&1 || missing_pkg+=("$p")
done
if [ ${#missing_pkg[@]} -gt 0 ]; then
    apt-get install -y -q "${missing_pkg[@]}" || {
        echo "   ✗ thiếu gói ${missing_pkg[*]} (repo apt WalnutPi)"
        exit 1
    }
fi
echo "   = libisp-t527, walnutpi.isp"

if [ "$changed" = 0 ] && runtime_ok; then
    rm -f "$MARKER"
    dronebridge_clear_reboot_reason "$INSTALL_DIR" camera
    echo "✅ WalnutPi IMX219 sẵn sàng (/dev/video0)"
    exit 0
fi
touch "$MARKER"
dronebridge_add_reboot_reason "$INSTALL_DIR" camera
echo "✅ WalnutPi IMX219 đã cập nhật — reboot để kernel nạp driver/DTB mới"
