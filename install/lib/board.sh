# config.yaml của cây đang chạy (repo hoặc /opt/dronebridge); DRONEBRIDGE_CONFIG ghi đè.
dronebridge_config_file() {
    local app
    if [ -n "${DRONEBRIDGE_CONFIG:-}" ]; then
        printf '%s\n' "$DRONEBRIDGE_CONFIG"
        return 0
    fi
    app="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
    if [ -f "$app/config.yaml" ]; then
        printf '%s\n' "$app/config.yaml"
    else
        printf '%s\n' /opt/dronebridge/config.yaml
    fi
}

# Nhận diện board: "walnutpi" (Allwinner T527, CM2/2B) hoặc "rpi" (CM5/Pi).
# Khóa "board:" trong config.yaml (cm5 | walnutpi; auto/rỗng = tự nhận) thắng, khớp config/board.go.
# WalnutPi không đọc dòng dtoverlay= kiểu Raspberry Pi trong /boot/config.txt;
# overlay của nó nằm trên một dòng "overlays=" và tên khác hẳn.
dronebridge_board() {
    local model="" forced cfg
    cfg="$(dronebridge_config_file)"
    forced="$(sed -n 's/^board:[[:space:]]*["'\'']\{0,1\}\([A-Za-z0-9]*\).*/\1/p' "$cfg" 2>/dev/null | head -1)"
    case "${forced,,}" in
        walnutpi) echo walnutpi; return 0 ;;
        cm5) echo rpi; return 0 ;;
    esac
    [ -r /proc/device-tree/model ] && model="$(tr -d '\0' < /proc/device-tree/model)"
    case "${model,,}" in
        *walnutpi*) echo walnutpi; return 0 ;;
        *raspberry*) echo rpi; return 0 ;;
    esac
    if [ -r /proc/device-tree/compatible ] && tr '\0' '\n' < /proc/device-tree/compatible | grep -q '^allwinner,'; then
        echo walnutpi
        return 0
    fi
    echo rpi
}

# Xóa khối "# DroneBridge: <tag>" và các dòng dtoverlay= theo sau khỏi boot config.
# Gọi: dronebridge_strip_boot_block <boot_config> <marker-prefix> <dtoverlay-prefix>...
dronebridge_strip_boot_block() {
    local cfg="$1" marker="$2"
    shift 2
    [ -f "$cfg" ] || return 0
    grep -qF "$marker" "$cfg" || return 0
    python3 - "$cfg" "$marker" "$@" <<'PY'
import os, sys
path, marker, *prefixes = sys.argv[1:]
with open(path) as f:
    lines = [ln.rstrip("\n") for ln in f]
out, skip = [], False
for s in lines:
    if s.startswith(marker):
        skip = True
        continue
    if skip and any(s.startswith(p) for p in prefixes):
        continue
    skip = False
    out.append(s)
while out and out[-1] == "":
    out.pop()
tmp = path + ".dronebridge.tmp"
with open(tmp, "w") as f:
    f.write("\n".join(out) + "\n")
os.replace(tmp, path)
PY
    echo "Đã gỡ khối '$marker' khỏi $cfg"
}

# Bỏ một dòng lý do khỏi .need_host_reboot; xóa file nếu rỗng.
dronebridge_clear_reboot_reason() {
    local marker="$1/.need_host_reboot" reason="$2"
    [ -f "$marker" ] || return 0
    grep -vx "$reason" "$marker" > "${marker}.tmp" 2>/dev/null || true
    mv "${marker}.tmp" "$marker"
    [ -s "$marker" ] || rm -f "$marker"
}

# Thêm một dòng lý do vào .need_host_reboot (không trùng).
dronebridge_add_reboot_reason() {
    local marker="$1/.need_host_reboot" reason="$2"
    echo "$reason" >> "$marker"
    sort -u "$marker" -o "$marker" 2>/dev/null || true
}

# Thiết bị TELEM2/TELEM3 và bus LCD theo board — khớp config/board.go.
# TELEM3 rỗng: board không có cổng đó.
dronebridge_telem2_dev() { [ "$(dronebridge_board)" = walnutpi ] && echo /dev/ttyS2 || echo /dev/ttyAMA2; }
dronebridge_telem3_dev() { [ "$(dronebridge_board)" = walnutpi ] && echo "" || echo /dev/ttyAMA3; }
dronebridge_lcd_bus() { [ "$(dronebridge_board)" = walnutpi ] && echo 4 || echo 3; }

# Bus LCD trong config.yaml sau khi map board như applyBoardProfile (config/board.go):
# rỗng/0 hoặc bằng bus mặc định của board kia → bus của board này; giá trị riêng (vd 1) giữ nguyên.
# Gọi: dronebridge_config_lcd_bus [config.yaml]
dronebridge_config_lcd_bus() {
    local cfg="${1:-$(dronebridge_config_file)}" raw own other
    own="$(dronebridge_lcd_bus)"
    [ "$own" = 4 ] && other=3 || other=4
    raw="$(python3 -c 'import sys,yaml;print(int(((yaml.safe_load(open(sys.argv[1])) or {}).get("lcd") or {}).get("bus") or 0))' "$cfg" 2>/dev/null || echo 0)"
    if [ "$raw" = 0 ] || [ "$raw" = "$other" ]; then
        echo "$own"
    else
        echo "$raw"
    fi
}

# hwmon của quạt pwm-fan. WalnutPi: chỉ tính node /dronebridge-fan (node hãng /pwm-fan
# lấy tach ở PL8, chân GND trên carrier CM5); tham số "any" nhận cả node hãng.
dronebridge_fan_hwmon() {
    local h node
    for h in /sys/class/hwmon/hwmon*; do
        [ "$(cat "$h/name" 2>/dev/null)" = pwmfan ] || continue
        node="$(basename "$(readlink -f "$h/of_node" 2>/dev/null)")"
        if [ "${1:-}" != any ] && [ "$(dronebridge_board)" = walnutpi ] && [ "$node" != dronebridge-fan ]; then
            continue
        fi
        echo "$h"
        return 0
    done
}

# Trạng thái regulator thay VBUS_EN trên WalnutPi (PI8): enabled / disabled / rỗng nếu chưa có.
dronebridge_vbus_state() {
    local r
    for r in /sys/class/regulator/regulator.*; do
        [ "$(cat "$r/name" 2>/dev/null)" = dronebridge-vbus-en ] || continue
        cat "$r/state" 2>/dev/null || echo enabled
        return 0
    done
}

# Một dòng trạng thái quạt: VBUS_EN, duty, mức làm mát, nhiệt CPU, RPM nếu có tach.
dronebridge_fan_status() {
    local h c z out=""
    h="$(dronebridge_fan_hwmon)"
    [ -n "$h" ] || { echo "không có hwmon pwmfan"; return 1; }
    [ "$(dronebridge_board)" = walnutpi ] && out="VBUS_EN(PI8) $(dronebridge_vbus_state), "
    out="${out}pwm $(cat "$h/pwm1")/255"
    for c in /sys/class/thermal/cooling_device*; do
        [ "$(cat "$c/type" 2>/dev/null)" = pwm-fan ] || continue
        out="$out, mức $(cat "$c/cur_state")/$(cat "$c/max_state")"
        break
    done
    for z in /sys/class/thermal/thermal_zone*; do
        case "$(cat "$z/type" 2>/dev/null)" in
            cpul*|cpu-thermal*|cpu_thermal*)
                out="$out, CPU $(( $(cat "$z/temp") / 1000 ))°C"
                break ;;
        esac
    done
    if [ -r "$h/fan1_input" ]; then
        out="$out, $(cat "$h/fan1_input") rpm"
    else
        out="$out, không đo tốc độ"
    fi
    echo "$out"
}

# User chạy dronebridge.service (install.sh đã thay User= trong unit).
dronebridge_service_user() {
    local u=""
    [ -f /etc/systemd/system/dronebridge.service ] && \
        u="$(sed -n 's/^User=//p' /etc/systemd/system/dronebridge.service | head -1)"
    if [ -z "$u" ] || ! id -u "$u" >/dev/null 2>&1; then
        u="${SUDO_USER:-}"
    fi
    printf '%s\n' "$u"
}

# Cho user dịch vụ vào nhóm (vd. dialout, i2c, video). Có hiệu lực khi service khởi động lại.
dronebridge_add_service_group() {
    local group="$1" user
    user="$(dronebridge_service_user)"
    [ -n "$user" ] && getent group "$group" >/dev/null || return 0
    id -nG "$user" | tr ' ' '\n' | grep -qx "$group" && return 0
    if usermod -aG "$group" "$user"; then
        echo "   + $user vào nhóm $group (restart dronebridge để nhận)"
    else
        echo "   ✗ không thêm được $user vào nhóm $group"
    fi
}

# Ghi udev rule nếu khác nội dung hiện có, rồi nạp lại.
# Tên 99-zz-* để chạy sau 99-dev-pi.rules của WalnutPi (rule đó đổi OWNER/GROUP mọi thiết bị).
dronebridge_udev_rule() {
    local file="/etc/udev/rules.d/$1" content="$2"
    if [ -f "$file" ] && [ "$(cat "$file")" = "$content" ]; then
        return 0
    fi
    printf '%s\n' "$content" > "$file"
    udevadm control --reload 2>/dev/null || true
    echo "   + udev rule $file"
}

# Sửa một khóa trong một section cấp 1 của config.yaml, giữ nguyên comment.
# Gọi: dronebridge_config_set <config.yaml> <section> <key> <value-yaml>
dronebridge_config_set() {
    local cfg="$1" section="$2" key="$3" value="$4"
    [ -f "$cfg" ] || return 0
    python3 - "$cfg" "$section" "$key" "$value" <<'PY'
import os, re, sys
path, section, key, value = sys.argv[1:]
with open(path) as f:
    lines = f.read().split("\n")
in_sec, changed, indent = False, False, None
pat = re.compile(r"^(\s+)" + re.escape(key) + r":\s*([^#]*?)(\s*#.*)?$")
for i, s in enumerate(lines):
    if re.match(r"^[A-Za-z_]", s):
        in_sec = s.split(":")[0] == section
        continue
    if not in_sec or not s.strip() or s.lstrip().startswith("#"):
        continue
    if indent is None:
        indent = len(s) - len(s.lstrip())
    m = pat.match(s)
    if m and len(m.group(1)) == indent:
        if m.group(2).strip() != value:
            lines[i] = f"{m.group(1)}{key}: {value}{m.group(3) or ''}"
            changed = True
        break
if changed:
    tmp = path + ".dronebridge.tmp"
    with open(tmp, "w") as f:
        f.write("\n".join(lines))
    st = os.stat(path)
    os.chown(tmp, st.st_uid, st.st_gid)
    os.chmod(tmp, st.st_mode & 0o777)
    os.replace(tmp, path)
    print(f"   config.yaml: {section}.{key} = {value}")
PY
}

# ── WalnutPi: overlay qua set-device ─────────────────────────────────────────
# /boot/config.txt "overlays=" chỉ có hiệu lực khi set-device dựng lại DTB boot
# (fdtoverlay vào /boot/<fdtfile>.dtb). u-boot không tự áp overlay.
walnutpi_boot_dtb() {
    echo "/boot/$(sed -n 's/^fdtfile=//p' /boot/config.txt | head -1).dtb"
}

walnutpi_overlay_listed() {
    sed -n 's/^overlays=//p' /boot/config.txt | tr ' ' '\n' | grep -qx "$1"
}

# Bật overlay và kiểm tra DTB boot có thuộc tính mong đợi.
# Gọi: walnutpi_ensure_overlay <overlay> <node> <prop> <value>
# Trả về 0 = DTB đã đúng từ trước, 2 = vừa dựng lại DTB (cần reboot), 1 = lỗi.
walnutpi_ensure_overlay() {
    local name="$1" node="$2" prop="$3" want="$4" dtb
    dtb="$(walnutpi_boot_dtb)"
    if walnutpi_overlay_listed "$name" && \
       [ "$(fdtget "$dtb" "$node" "$prop" 2>/dev/null)" = "$want" ]; then
        return 0
    fi
    local prefix
    prefix="$(sed -n 's/^overlay_prefix=//p' /boot/config.txt | head -1)"
    if [ ! -f "/boot/overlays/${prefix}-${name}.dtbo" ]; then
        echo "   ✗ thiếu /boot/overlays/${prefix}-${name}.dtbo"
        return 1
    fi
    set-device enable "$name" >/dev/null
    if [ "$(fdtget "$dtb" "$node" "$prop" 2>/dev/null)" != "$want" ]; then
        echo "   ✗ $dtb: $node $prop khác '$want' sau set-device enable $name"
        return 1
    fi
    echo "   + overlay $name đã ghi vào $dtb"
    return 2
}
