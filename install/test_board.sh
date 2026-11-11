#!/bin/bash
# [TEST] Kiểm tra phần cứng theo board (CM5 / WalnutPi CM2). Chỉ đọc, không cần sudo, không đổi gì.
# Cài / sửa: sudo bash install/install.sh --check
# Gọi:
#   bash install/test_board.sh [/opt/dronebridge/config.yaml]
#   sudo bash install/test_board.sh --fan-spin   quạt 100% rồi dừng (5 s mỗi mức), trả lại như cũ
#                                                WalnutPi chưa có overlay: tạm kéo PI8 (VBUS_EN) lên
#                                                trong lúc thử rồi trả PI8 cho bộ thu hồng ngoại
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=lib/root.sh
source "$SCRIPT_DIR/lib/root.sh"
# shellcheck source=lib/board.sh
source "$SCRIPT_DIR/lib/board.sh"

if [ "${1:-}" = "--fan-spin" ]; then
    [ "$EUID" -eq 0 ] || { echo "Cần sudo: sudo bash $0 --fan-spin"; exit 1; }
    H="$(dronebridge_fan_hwmon any)"
    [ -n "$H" ] || { echo "FAIL  không có hwmon pwmfan — chạy install --check rồi reboot"; exit 1; }
    echo "hwmon: $H (node $(basename "$(readlink -f "$H/of_node")"))"
    orig="$(cat "$H/pwm1")"
    IRRX=2005000.irrx
    IRDRV=/sys/bus/platform/drivers/sunxi-rc-recv
    vbus_pid=""
    ir_unbound=0
    cleanup() {
        echo "$orig" > "$H/pwm1"
        [ -n "$vbus_pid" ] && kill "$vbus_pid" 2>/dev/null && wait "$vbus_pid" 2>/dev/null
        [ "$ir_unbound" = 1 ] && echo "$IRRX" > "$IRDRV/bind" 2>/dev/null
        true
    }
    trap cleanup EXIT
    if [ "$(dronebridge_board)" = walnutpi ] && [ -z "$(dronebridge_vbus_state)" ]; then
        # PI8 = gpiochip 2000000.pinctrl line 8*32+8; đang thuộc bộ thu hồng ngoại.
        if [ -e "$IRDRV/$IRRX" ]; then
            echo "$IRRX" > "$IRDRV/unbind" && ir_unbound=1
        fi
        gpioset -m signal 2000000.pinctrl 264=1 &
        vbus_pid=$!
        sleep 0.5
        if kill -0 "$vbus_pid" 2>/dev/null; then
            echo "VBUS_EN tạm: PI8 = 1 (B2B 111) trong lúc thử"
        else
            vbus_pid=""
            echo "FAIL  không kéo được PI8 lên (gpioset)"
            exit 1
        fi
    fi
    echo "Trước: pwm $orig/255"
    echo "→ pwm 255 (100%) trong 5 s — quạt phải quay nhanh nhất"
    echo 255 > "$H/pwm1"; sleep 5
    echo "→ pwm 1 (dừng) trong 5 s — quạt 4 dây phải chậm hẳn / dừng"
    echo 1 > "$H/pwm1"; sleep 5
    echo "Trả lại pwm $orig${vbus_pid:+, thả PI8 về bộ thu hồng ngoại}."
    echo "Quạt không quay lúc 100% → nguồn quạt chưa có (VBUS_EN, B2B 111) hoặc dây quạt."
    echo "Quay nhưng không đổi tốc độ → Fan_PWM (B2B 19) chưa tới quạt, hoặc quạt 2/3 dây."
    exit 0
fi

INSTALL_DIR="$(dronebridge_root)"
CONFIG="${1:-$INSTALL_DIR/config.yaml}"
[ -f "$CONFIG" ] || CONFIG=/opt/dronebridge/config.yaml
BOARD="$(dronebridge_board)"
SVC_USER="$(dronebridge_service_user)"
SVC_USER="${SVC_USER:-$(id -un)}"
FAILS=0
WARNS=0

pass() { echo "  PASS  $*"; }
fail() { echo "  FAIL  $*"; FAILS=$((FAILS + 1)); }
warn() { echo "  WARN  $*"; WARNS=$((WARNS + 1)); }

cfg_get() {
    python3 - "$CONFIG" "$1" "$2" <<'PY' 2>/dev/null
import sys, yaml
cfg = yaml.safe_load(open(sys.argv[1])) or {}
v = (cfg.get(sys.argv[2]) or {}).get(sys.argv[3], "")
print("" if v is None else v)
PY
}

user_in_group() { id -nG "$SVC_USER" 2>/dev/null | tr ' ' '\n' | grep -qx "$1"; }

# Thiết bị char: có, đúng nhóm, user dịch vụ đọc/ghi được.
check_dev() {
    local label="$1" dev="$2" group="$3"
    if [ ! -e "$dev" ]; then
        fail "$label: không có $dev (overlay chưa nạp — cần install --check rồi reboot)"
        return 1
    fi
    local g mode
    g="$(stat -c %G "$dev")"
    mode="$(stat -c %a "$dev")"
    if [ "$g" = "$group" ] && [ "${mode:1:1}" -ge 6 ] && user_in_group "$group"; then
        pass "$label: $dev ($g $mode, $SVC_USER ∈ $group)"
    elif [ -r "$dev" ] && [ -w "$dev" ] && [ "$(id -un)" = "$SVC_USER" ]; then
        pass "$label: $dev (quyền đọc/ghi OK)"
    else
        fail "$label: $dev là $(stat -c %U:%G "$dev") $mode — $SVC_USER chưa đọc/ghi được"
        return 1
    fi
}

echo "=== DroneBridge board test: $BOARD (config: $CONFIG) ==="

echo "[1] MAVLink TELEM UART"
T2_WANT="$(dronebridge_telem2_dev)"
T3_WANT="$(dronebridge_telem3_dev)"
T2_CFG="$(cfg_get network telem2_port)"
T3_CFG="$(cfg_get network telem3_port)"
check_dev "TELEM2" "$T2_WANT" dialout
if [ "$T2_CFG" = "$T2_WANT" ]; then
    pass "config telem2_port = $T2_CFG"
else
    warn "config telem2_port = '${T2_CFG}' (board dùng $T2_WANT; DroneBridge tự đổi khi chạy)"
fi
if [ -n "$T3_WANT" ]; then
    check_dev "TELEM3" "$T3_WANT" dialout
elif [ -n "$T3_CFG" ]; then
    warn "config telem3_port = '$T3_CFG' nhưng board $BOARD không có TELEM3"
else
    pass "TELEM3: board $BOARD không có cổng này (config trống)"
fi

echo "[2] LCD I2C"
BUS="$(dronebridge_lcd_bus)"
BUS_CFG="$(cfg_get lcd bus)"
[ "$BUS_CFG" = "$BUS" ] && pass "config lcd.bus = $BUS_CFG" || warn "config lcd.bus = '${BUS_CFG}' (board dùng $BUS)"
if check_dev "I2C$BUS" "/dev/i2c-$BUS" i2c; then
    if ! command -v i2cdetect >/dev/null 2>&1; then
        warn "chưa có i2cdetect (apt install i2c-tools) — bỏ qua dò LCD"
    elif [ -r "/dev/i2c-$BUS" ] && [ -w "/dev/i2c-$BUS" ]; then
        if i2cdetect -y -r "$BUS" 0x3c 0x3c 2>/dev/null | grep -qE '^30:.* 3c'; then
            pass "LCD SH1107 trả lời tại 0x3C trên /dev/i2c-$BUS"
        else
            warn "không thấy 0x3C trên /dev/i2c-$BUS (LCD chưa cắm, hoặc SDA/SCL đảo)"
        fi
    else
        warn "user $(id -un) chưa vào nhóm i2c trong phiên này — đăng nhập lại để dò LCD"
    fi
fi
[ "$(cfg_get lcd enabled)" = "True" ] && \
    { systemctl is-active -q dronebridge-lcd.service && pass "dronebridge-lcd đang chạy" || warn "lcd.enabled=true nhưng dronebridge-lcd không chạy"; }

echo "[3] Camera"
if [ "$BOARD" = walnutpi ]; then
    out="$(bash "$SCRIPT_DIR/camera/walnutpi/setup_imx219.sh" --check "$CONFIG" 2>&1)" \
        && pass "${out#✅ }" || fail "${out#⚠️  }"
    check_dev "cam0" /dev/video0 video
else
    if command -v rpicam-hello >/dev/null 2>&1; then
        n="$(rpicam-hello --list-cameras 2>/dev/null | grep -cE '^[0-9]+ : ')"
        [ "$n" -gt 0 ] && pass "rpicam thấy $n camera" || warn "rpicam không thấy camera nào"
    else
        warn "không có rpicam-hello"
    fi
fi
STATS=/tmp/camera_stream_stats_0.json
if [ -f "$STATS" ] && [ $(( $(date +%s) - $(stat -c %Y "$STATS") )) -lt 30 ]; then
    fps="$(python3 -c "import json;print(json.load(open('$STATS')).get('fps_sent',0))")"
    pass "cam0 đang đẩy stream: ${fps} fps"
else
    warn "cam0 chưa đẩy stream (không có $STATS mới)"
fi

echo "[4] Quạt (B2B 19 Fan_PWM, 16 Fan_Tacho, 111 VBUS_EN)"
if [ "$BOARD" = walnutpi ]; then
    out="$(bash "$SCRIPT_DIR/fan/setup_fan.sh" --check 2>&1)" \
        && pass "${out#✅ }" || fail "${out#⚠️  }"
    if [ -d /proc/device-tree/pwm-fan ] && \
       [ "$(tr -d '\0' 2>/dev/null < /proc/device-tree/pwm-fan/status)" != disabled ]; then
        warn "node hãng /pwm-fan vẫn bật (100 kHz, tach PL8 = GND trên carrier CM5)"
    fi
    pass "Fan_Tacho: CM2 nối vào ADC22 — không đo RPM (giới hạn phần cứng, không phải lỗi)"
    if [ "$EUID" -eq 0 ]; then
        l="$(grep -E 'pwm-5 .*(fan|requested)' /sys/kernel/debug/pwm 2>/dev/null | head -1)"
        [ -n "$l" ] && pass "debugfs pwm: $(echo "$l" | tr -s ' ')"
        l="$(grep -h '(PL7)' /sys/kernel/debug/pinctrl/*/pinmux-pins 2>/dev/null | head -1)"
        case "$l" in
            *mcu_pwm0_5*) pass "PL7 mux: $l" ;;
            "") ;;
            *) warn "PL7 chưa mux PWM: $l" ;;
        esac
    fi
else
    if [ -n "$(dronebridge_fan_hwmon)" ]; then
        pass "Quạt: $(dronebridge_fan_status)"
    else
        warn "không thấy hwmon pwmfan (không có quạt hoặc firmware chưa bật cooling_fan)"
    fi
fi

echo "[5] Dịch vụ"
if systemctl is-active -q dronebridge; then
    pass "dronebridge đang chạy"
    line="$(journalctl -u dronebridge -b --no-pager 2>/dev/null | grep -F '[BOARD]' | tail -1 | sed 's/.*\[BOARD\] //')"
    [ -n "$line" ] && pass "DroneBridge nhận board: $line" || warn "chưa thấy dòng [BOARD] trong log (binary cũ hoặc chưa có quyền đọc journal)"
else
    fail "dronebridge không chạy"
fi
if [ -f "$INSTALL_DIR/.need_host_reboot" ]; then
    warn "đang chờ reboot: $(tr '\n' ' ' < "$INSTALL_DIR/.need_host_reboot")"
fi

echo "[6] Log và đăng ký server"
out="$(bash "$SCRIPT_DIR/service/setup_journald.sh" --check 2>&1)" \
    && pass "${out#✅ }" || warn "${out#⚠️  } (sudo bash install/install.sh --check)"
ERRF="$INSTALL_DIR/data/logs/dronebridge-errors.log"
if [ -f "$ERRF" ]; then
    pass "file lỗi $ERRF: $(du -ch "$ERRF"* 2>/dev/null | tail -1 | cut -f1), $(wc -l < "$ERRF") dòng"
else
    warn "chưa có $ERRF (binary cũ, hoặc chưa phát sinh WARN/ERROR)"
fi
if [ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" = yes ]; then
    pass "đồng hồ đã đồng bộ NTP"
else
    warn "đồng hồ chưa đồng bộ NTP — server sẽ từ chối xác thực (error=1)"
fi
jlog="$(journalctl -u dronebridge -b --no-pager -q -o cat 2>/dev/null)"
if [ -n "$jlog" ]; then
    r="$(grep -c 'AUTH_ACK reject' <<< "$jlog")"
    if grep -qE '\[AUTH\] ✅ Authentication successful' <<< "$jlog"; then
        pass "đăng ký server OK trong lần boot này (bị từ chối $r lần trước đó)"
    else
        fail "chưa xác thực được với server (bị từ chối $r lần) — xem: journalctl -u dronebridge -p warning"
    fi
else
    warn "không đọc được journal của dronebridge (user chưa vào nhóm systemd-journal/adm)"
fi

echo "=== Kết quả: $FAILS FAIL, $WARNS WARN ==="
[ "$FAILS" -eq 0 ]
