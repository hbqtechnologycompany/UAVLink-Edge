#!/bin/bash
# Quạt theo chân B2B của carrier CM5 (19 = Fan_PWM, 16 = Fan_Tacho, 111 = VBUS_EN).
#   CM5:      firmware/kernel Raspberry Pi tự điều khiển quạt — không làm gì.
#   WalnutPi: overlay "dronebridge-fan" — PWM 25 kHz trên PL7 (B2B 19) theo nhiệt CPU;
#             tắt tach PL8 của node hãng (B2B 14 là GND trên carrier CM5);
#             PI8 (IR-RX, B2B 111) giữ mức cao thay VBUS_EN để carrier cấp nguồn quạt / USB3.
#             Fan_Tacho (B2B 16) rơi vào ADC22 của CM2 nên không đọc được tốc độ quạt.
# Gọi:
#   setup_fan.sh            cài / sửa (root); đúng rồi thì không đổi gì
#   setup_fan.sh --check    chỉ kiểm tra; exit 1 nếu còn thiếu hoặc chờ reboot
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
# shellcheck source=../lib/board.sh
source "$SCRIPT_DIR/../lib/board.sh"
INSTALL_DIR="$(dronebridge_root)"

CHECK_ONLY=0
[ "${1:-}" = "--check" ] && CHECK_ONLY=1

OVERLAY=dronebridge-fan
FAN_NODE=/dronebridge-fan
VBUS_NODE=/dronebridge-vbus
VENDOR_NODE=/pwm-fan
IRRX_NODE=/soc@3000000/irrx@2005000

if [ "$(dronebridge_board)" != walnutpi ]; then
    if [ -n "$(dronebridge_fan_hwmon)" ]; then
        echo "✅ Quạt: $(dronebridge_fan_status)"
    else
        echo "ℹ️  Quạt: không thấy hwmon pwmfan (firmware Raspberry Pi quản lý quạt)"
    fi
    exit 0
fi

dtb_has_fan() {
    local dtb
    dtb="$(walnutpi_boot_dtb)"
    [ "$(fdtget "$dtb" "$FAN_NODE" compatible 2>/dev/null)" = pwm-fan ] &&
        [ "$(fdtget "$dtb" "$FAN_NODE" fan-supply 2>/dev/null)" != "" ] &&
        [ "$(fdtget "$dtb" "$VBUS_NODE" compatible 2>/dev/null)" = regulator-fixed ] &&
        [ "$(fdtget "$dtb" "$VENDOR_NODE" status 2>/dev/null)" = disabled ] &&
        [ "$(fdtget "$dtb" "$IRRX_NODE" status 2>/dev/null)" = disabled ]
}

# Kernel đang chạy đã dùng node mới, VBUS_EN đang bật và pwm-fan đã bind vào node mới.
runtime_ok() {
    [ -d "/proc/device-tree$FAN_NODE" ] && [ "$(dronebridge_vbus_state)" = enabled ] &&
        [ -n "$(dronebridge_fan_hwmon)" ]
}

if [ "$CHECK_ONLY" = 1 ]; then
    miss=()
    dtb_has_fan || miss+=("DTB")
    runtime_ok || miss+=("kernel chưa chạy node $FAN_NODE")
    if [ ${#miss[@]} -eq 0 ]; then
        echo "✅ Quạt PL7 25 kHz: $(dronebridge_fan_status)"
        exit 0
    fi
    echo "⚠️  Quạt WalnutPi còn thiếu: $(printf '%s; ' "${miss[@]}" | sed 's/; $//')"
    exit 1
fi

PREFIX="$(sed -n 's/^overlay_prefix=//p' /boot/config.txt | head -1)"
DTBO_DST="/boot/overlays/${PREFIX}-${OVERLAY}.dtbo"

echo "▶ [1/2] Overlay $OVERLAY → /boot/overlays"
if cmp -s "$SCRIPT_DIR/walnutpi/sun55i-t527-${OVERLAY}.dtbo" "$DTBO_DST"; then
    echo "   = $DTBO_DST"
else
    install -m 0644 "$SCRIPT_DIR/walnutpi/sun55i-t527-${OVERLAY}.dtbo" "$DTBO_DST"
    echo "   + $DTBO_DST"
fi

echo "▶ [2/2] DTB boot: tắt $VENDOR_NODE và irrx, bật $FAN_NODE + VBUS_EN (PI8)"
changed=0
if dtb_has_fan && walnutpi_overlay_listed "$OVERLAY"; then
    echo "   = DTB đã có $FAN_NODE"
else
    set-device enable "$OVERLAY" >/dev/null
    if ! dtb_has_fan; then
        echo "   ✗ DTB chưa có $FAN_NODE sau set-device — gỡ overlay"
        set-device disable "$OVERLAY" >/dev/null || true
        exit 1
    fi
    changed=1
    echo "   + $(walnutpi_boot_dtb): $FAN_NODE (PL7, 25 kHz), $VBUS_NODE (PI8 mức cao)"
fi

if [ "$changed" = 0 ] && runtime_ok; then
    dronebridge_clear_reboot_reason "$INSTALL_DIR" fan
    echo "✅ Quạt PL7 25 kHz: $(dronebridge_fan_status)"
    exit 0
fi
dronebridge_add_reboot_reason "$INSTALL_DIR" fan
echo "✅ Quạt WalnutPi đã cập nhật — reboot để kernel dùng DTB mới"
