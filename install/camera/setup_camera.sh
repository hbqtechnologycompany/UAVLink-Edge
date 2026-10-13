#!/bin/bash
# Cấu hình boot camera CM5 từ camera_detected.json (giống setup_eth.sh)
# Gọi: setup_camera.sh [/opt/dronebridge/config.yaml]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
CONFIG="${1:-$INSTALL_DIR/config.yaml}"
CHECK_ONLY=0
BOOT_SETTLED=0
if [ "${1:-}" = "--fix-owner" ]; then
    DETECTED="$INSTALL_DIR/Find_landing/camera_detected.json"
    if [ -f "$DETECTED" ]; then
        chown --reference="$INSTALL_DIR" "$DETECTED"
        chmod 664 "$DETECTED"
        echo "camera_detected.json thuộc $(stat -c '%U:%G' "$DETECTED")"
    fi
    exit 0
elif [ "${1:-}" = "--check" ]; then
    CHECK_ONLY=1
    CONFIG="${2:-$INSTALL_DIR/config.yaml}"
elif [ "${1:-}" = "--boot-settled" ]; then
    BOOT_SETTLED=1
    CONFIG="${2:-$INSTALL_DIR/config.yaml}"
fi
BOOT_CONFIG="/boot/firmware/config.txt"
[ -f "$BOOT_CONFIG" ] || BOOT_CONFIG="/boot/config.txt"
DETECTED="$INSTALL_DIR/Find_landing/camera_detected.json"
MARKER="$INSTALL_DIR/.need_camera_reboot"

read -r AUTO_SETUP <<< "$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f) or {}
cam = cfg.get("camera") or {}
print("1" if cam.get("auto_setup", True) and cam.get("enabled", True) else "0")
PY
)"

if [ "$AUTO_SETUP" != "1" ]; then
    echo "Camera auto_setup tắt — bỏ qua boot config"
    rm -f "$MARKER"
    exit 0
fi

if [ ! -f "$BOOT_CONFIG" ]; then
    echo "Không tìm thấy boot config — bỏ qua"
    exit 1
fi

if [ ! -f "$DETECTED" ]; then
    echo "Tạo $DETECTED từ sensor đang có trên cổng (không đoán imx219)"
    mkdir -p "$(dirname "$DETECTED")"
    python3 - "$DETECTED" <<'PY'
import datetime, json, re, subprocess, sys
p = sys.argv[1]
ports = {
    "cam0": {"overlay": "", "sensor": "", "enabled": False},
    "cam1": {"overlay": "", "sensor": "", "enabled": False},
}
bus_port = {"88000": "cam0", "70000": "cam1"}
text = ""
try:
    text = subprocess.check_output(
        ["rpicam-hello", "--list-cameras"], text=True, stderr=subprocess.STDOUT, timeout=20
    )
except Exception:
    text = ""
for sensor, bus in re.findall(r"(\w+)\s+\[.*?/i2c@(\d+)/", text):
    port = bus_port.get(bus)
    if not port:
        continue
    ports[port] = {"overlay": sensor, "sensor": sensor, "enabled": True}
st = {
    "updated_at": datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"),
    "ports": ports,
    "last_connected": [],
}
open(p, "w").write(json.dumps(st, indent=2) + "\n")
found = [k for k, v in ports.items() if v.get("enabled")]
print("sensor thấy được:", ", ".join(found) if found else "không có")
PY
fi

generate_block() {
    python3 - "$DETECTED" <<'PY'
import json, sys
st = json.load(open(sys.argv[1]))
lines = [
    "# DroneBridge: camera từ camera_detected.json",
    "camera_auto_detect=0",
    "dtparam=cam0_reg=on",
    "dtparam=cam1_reg=on",
    "dtparam=i2c_csi_dsi=on",
    "dtparam=i2c_csi_dsi0=on",
]
for port in ("cam0", "cam1"):
    cfg = st.get("ports", {}).get(port, {})
    if not cfg.get("enabled", True):
        continue
    ov = (cfg.get("overlay") or cfg.get("sensor") or "").strip()
    if not ov:
        continue
    # Overlay chính thức chỉ có tham số cam0. CAM1 là mặc định (Unicam 1), không có tham số cam1.
    if port == "cam0":
        lines.append(f"dtoverlay={ov},cam0")
    else:
        lines.append(f"dtoverlay={ov}")
print("\n".join(lines))
PY
}

NEW_BLOCK="$(generate_block)"
if ! printf '%s\n' "$NEW_BLOCK" | grep -q '^dtoverlay='; then
    echo "Chưa có overlay sensor — không ghi boot config"
    exit 0
fi

CURRENT_BLOCK=""
if grep -q "# DroneBridge: camera từ camera_detected.json" "$BOOT_CONFIG" 2>/dev/null; then
    CURRENT_BLOCK="$(awk '/# DroneBridge: camera từ camera_detected.json/{flag=1;next}flag&&/^$/{exit}flag' "$BOOT_CONFIG")"
    # include marker line in current for compare
    CURRENT_BLOCK="# DroneBridge: camera từ camera_detected.json
$CURRENT_BLOCK"
    CURRENT_BLOCK="${CURRENT_BLOCK%$'\n'}"
fi

clear_camera_reboot_marker() {
    rm -f "$MARKER"
    if [ -f "$INSTALL_DIR/.need_host_reboot" ]; then
        grep -v '^camera$' "$INSTALL_DIR/.need_host_reboot" > "${INSTALL_DIR}/.need_host_reboot.tmp" 2>/dev/null || true
        mv "${INSTALL_DIR}/.need_host_reboot.tmp" "$INSTALL_DIR/.need_host_reboot" 2>/dev/null || rm -f "$INSTALL_DIR/.need_host_reboot"
        [ ! -s "$INSTALL_DIR/.need_host_reboot" ] && rm -f "$INSTALL_DIR/.need_host_reboot"
    fi
}

camera_reboot_pending() {
    [ -f "$MARKER" ] && return 0
    [ -f "$INSTALL_DIR/.need_host_reboot" ] && grep -q '^camera$' "$INSTALL_DIR/.need_host_reboot" 2>/dev/null
}

if [ "$CURRENT_BLOCK" = "$NEW_BLOCK" ]; then
    if [ "$CHECK_ONLY" = "1" ]; then
        echo "✅ Camera boot config đã đúng — không đổi"
        exit 0
    fi
    if [ "$BOOT_SETTLED" = "1" ]; then
        clear_camera_reboot_marker
        echo "✅ Camera boot config đã đúng — không đổi"
        exit 0
    fi
    if camera_reboot_pending; then
        echo "✅ Camera boot config đã ghi — chờ reboot để áp dụng"
        exit 0
    fi
    echo "✅ Camera boot config đã đúng — không đổi"
    exit 0
fi

if [ "$CHECK_ONLY" = "1" ]; then
    echo "⚠️  Boot overlay cần cập nhật"
    exit 1
fi

# Gỡ block camera DroneBridge cũ + dòng camera lẻ
TMP="$(mktemp)"
python3 - "$BOOT_CONFIG" "$TMP" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
markers = (
    "# DroneBridge: camera",
    "# DroneBridge: Camera",
    "# DroneBridge: Dual IMX219",
)
camera_keys = (
    "camera_auto_detect=",
    "dtparam=cam0_reg=",
    "dtparam=cam1_reg=",
    "dtparam=i2c_csi_dsi=",
    "dtparam=i2c_csi_dsi0=",
)
overlay_prefixes = ("dtoverlay=imx", "dtoverlay=ov", "dtoverlay=arducam")
out = []
skip = False
with open(src) as f:
    for line in f:
        s = line.rstrip("\n")
        if any(s.startswith(m) for m in markers):
            skip = True
            continue
        if skip:
            if s == "":
                skip = False
            continue
        if s.startswith("camera_auto_detect=0"):
            skip = True
            continue
        if skip:
            if s.startswith(camera_keys) or any(s.startswith(p) for p in overlay_prefixes):
                continue
            if s == "":
                skip = False
            else:
                skip = False
                out.append(s)
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
    echo "$NEW_BLOCK"
} >> "$BOOT_CONFIG"

touch "$MARKER"
echo "camera" >> "$INSTALL_DIR/.need_host_reboot"
sort -u "$INSTALL_DIR/.need_host_reboot" -o "$INSTALL_DIR/.need_host_reboot" 2>/dev/null || true
echo "✅ Camera boot config đã cập nhật — sẽ tự reboot nếu DRONEBRIDGE_AUTO_REBOOT=1"
grep -n 'camera\|dtoverlay=imx\|dtoverlay=ov\|cam0\|cam1\|i2c_csi' "$BOOT_CONFIG" | tail -12 || true
