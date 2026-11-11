#!/bin/bash
# Đăng ký lại drone với server, UUID cũ hoặc mới (root).
#   Dashboard: systemd-run --unit=dronebridge-reregister /bin/bash reregister.sh <uuid> [vehicle_type]
#              (ngoài cgroup của dronebridge — script dừng service đó)
#   Tay:       sudo bash /opt/dronebridge/install/service/reregister.sh <uuid> [vehicle_type]
# vehicle_type = HEARTBEAT.type gửi trong REGISTER_INIT v2 (server chọn hardware_profile / giao diện:
# 2 quadrotor, 10 rover, …). Bỏ trống = giữ auth.vehicle_type trong config.
# Các bước: chờ NTP → sao lưu config + secret → dừng dronebridge → ghi auth.uuid →
#           dronebridge --register (user dịch vụ) → lỗi thì khôi phục → khởi động lại → chờ xác thực.
# Tiến trình ghi vào data/reregister.json (dashboard đọc), log đầy đủ ở data/logs/reregister.log.
set -uo pipefail
PATH="$PATH:/usr/sbin:/sbin"

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
# shellcheck source=../lib/board.sh
source "$SCRIPT_DIR/../lib/board.sh"
INSTALL_DIR="$(dronebridge_root)"
CONFIG="$INSTALL_DIR/config.yaml"
BIN="$INSTALL_DIR/dronebridge"
SECRET="$INSTALL_DIR/.drone_secret"
STATE="$INSTALL_DIR/data/reregister.json"
LOG="$INSTALL_DIR/data/logs/reregister.log"
UUID="${1:-}"
UUID="${UUID,,}"
VTYPE="${2:-}"
STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OLD_UUID=""

# UUID chỉ hiện 2 ký tự đầu, 2 ký tự cuối (journal, file trạng thái, log).
UUID_SED='s/\b([0-9a-fA-F]{2})[0-9a-fA-F]{6}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{10}([0-9a-fA-F]{2})\b/\1••••\2/g'
mask_uuids() { sed -E "$UUID_SED"; }

write_state() {
    local state="$1" msg="$2"
    mkdir -p "$(dirname "$STATE")"
    python3 - "$STATE" "$state" "$UUID" "$OLD_UUID" "$msg" "$STARTED" "$LOG" <<'PY'
import json, os, re, sys, datetime
path, state, uuid, old, msg, started, log = sys.argv[1:]
mask = lambda s: re.sub(r"(?i)\b([0-9a-f]{2})[0-9a-f]{6}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{10}([0-9a-f]{2})\b",
                        "\\1••••\\2", s)
tail = ""
if state != "running" and os.path.exists(log):
    with open(log, errors="replace") as f:
        tail = "".join(f.readlines()[-25:])
out = {"state": state, "uuid": mask(uuid), "old_uuid": mask(old), "message": mask(msg), "started_at": started,
       "finished_at": "" if state == "running" else datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"),
       "log": mask(tail)}
tmp = path + ".tmp"
with open(tmp, "w") as f:
    json.dump(out, f, ensure_ascii=False, indent=1)
os.chmod(tmp, 0o644)
os.replace(tmp, path)
PY
    echo "[REREGISTER] $state: $msg" | mask_uuids
}

fail() {
    write_state failed "$1"
    exit 1
}

[ "$EUID" -eq 0 ] || { echo "Cần sudo"; exit 1; }
[[ "$UUID" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] ||
    fail "UUID không hợp lệ: '${1:-}'"
if [ -n "$VTYPE" ]; then
    [[ "$VTYPE" =~ ^[0-9]{1,3}$ ]] && [ "$VTYPE" -le 255 ] || fail "vehicle_type không hợp lệ: '$VTYPE'"
fi
[ -x "$BIN" ] || fail "thiếu $BIN"
[ -f "$CONFIG" ] || fail "thiếu $CONFIG"

OLD_UUID="$(python3 - "$CONFIG" <<'PY' 2>/dev/null
import sys, yaml
print(((yaml.safe_load(open(sys.argv[1])) or {}).get("auth") or {}).get("uuid") or "")
PY
)"
write_state running "Bắt đầu đăng ký lại (UUID cũ: ${OLD_UUID:-trống})"

# 1. Timestamp trong REGISTER_RESPONSE phải đúng giờ server (RTC CM2 về 1970 sau mỗi boot).
for _ in $(seq 60); do
    [ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" = yes ] && break
    sleep 1
done
[ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" = yes ] ||
    echo "[REREGISTER] ⚠️ đồng hồ chưa đồng bộ NTP — server có thể từ chối timestamp"

# 2. Sao lưu để khôi phục khi đăng ký thất bại.
BACKUP="$(mktemp -d "${TMPDIR:-/run}/dronebridge-reregister.XXXXXX")" || fail "không tạo được thư mục sao lưu"
trap 'rm -rf "$BACKUP"' EXIT
cp -a "$CONFIG" "$BACKUP/config.yaml" || fail "không sao lưu được $CONFIG"
if [ -f "$SECRET" ]; then
    cp -a "$SECRET" "$BACKUP/drone_secret" || fail "không sao lưu được $SECRET"
fi

restore() {
    cp -a "$BACKUP/config.yaml" "$CONFIG"
    if [ -f "$BACKUP/drone_secret" ]; then
        cp -a "$BACKUP/drone_secret" "$SECRET"
    fi
}

# 3. Dừng service (auth client đang giữ UUID cũ).
systemctl stop dronebridge 2>/dev/null || true

# 4. Ghi UUID mới vào config.yaml (giữ comment và chủ file).
if [ "$UUID" != "$OLD_UUID" ]; then
    dronebridge_config_set "$CONFIG" auth uuid "\"$UUID\""
    now="$(python3 -c "import sys,yaml;print((yaml.safe_load(open(sys.argv[1])).get('auth') or {}).get('uuid') or '')" "$CONFIG")"
    if [ "$now" != "$UUID" ]; then
        restore
        systemctl start dronebridge
        fail "không ghi được auth.uuid vào $CONFIG"
    fi
fi

# 4b. Ghi loại phương tiện gửi trong REGISTER_INIT (file cũ thiếu khóa thì thêm dưới auth.uuid).
if [ -n "$VTYPE" ]; then
    python3 - "$CONFIG" "$VTYPE" <<'PY' || { restore; systemctl start dronebridge; fail "không ghi được auth.vehicle_type"; }
import os, re, sys
path, value = sys.argv[1:]
with open(path) as f:
    lines = f.read().split("\n")
in_auth, uuid_at, done = False, None, False
for i, s in enumerate(lines):
    if re.match(r"^[A-Za-z_]", s):
        in_auth = s.split(":")[0] == "auth"
        continue
    if not in_auth:
        continue
    m = re.match(r"^(\s+)vehicle_type:\s*[^#]*?(\s*#.*)?$", s)
    if m:
        lines[i] = f"{m.group(1)}vehicle_type: {value}{m.group(2) or ''}"
        done = True
        break
    m = re.match(r"^(\s+)uuid:", s)
    if m:
        uuid_at = (i, m.group(1))
if not done:
    if uuid_at is None:
        sys.exit(1)
    lines.insert(uuid_at[0] + 1, f"{uuid_at[1]}vehicle_type: {value}")
tmp = path + ".dronebridge.tmp"
with open(tmp, "w") as f:
    f.write("\n".join(lines))
st = os.stat(path)
os.chown(tmp, st.st_uid, st.st_gid)
os.chmod(tmp, st.st_mode & 0o777)
os.replace(tmp, path)
PY
    now="$(python3 -c "import sys,yaml;print((yaml.safe_load(open(sys.argv[1])).get('auth') or {}).get('vehicle_type') or 0)" "$CONFIG")"
    if [ "$now" != "$VTYPE" ]; then
        restore
        systemctl start dronebridge
        fail "không ghi được auth.vehicle_type vào $CONFIG"
    fi
fi

# 5. Đăng ký. Chạy bằng user dịch vụ để .drone_secret thuộc user đó.
mkdir -p "$(dirname "$LOG")"
SVC_USER="$(dronebridge_service_user)"
write_state running "Đang gửi REGISTER tới server (UUID $UUID, vehicle_type ${VTYPE:-theo config})"
if [ -n "$SVC_USER" ]; then
    chown "$SVC_USER" "$(dirname "$LOG")" 2>/dev/null
    (cd "$INSTALL_DIR" && timeout 90 runuser -u "$SVC_USER" -- "$BIN" --config "$CONFIG" --register) > "$LOG" 2>&1
else
    (cd "$INSTALL_DIR" && timeout 90 "$BIN" --config "$CONFIG" --register) > "$LOG" 2>&1
fi
rc=$?
sed -i -E "$UUID_SED" "$LOG" 2>/dev/null

secret_uuid="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('drone_uuid',''))" "$SECRET" 2>/dev/null)"
if [ "$rc" -ne 0 ] || [ "$secret_uuid" != "$UUID" ]; then
    reason="$(grep -E 'FATAL|failed|error=' "$LOG" | tail -1 | sed -E 's/^[0-9/]+ [0-9:]+ //')"
    [ "$rc" -eq 124 ] && reason="hết 90 s chưa xong (không tới được server?)"
    restore
    systemctl start dronebridge
    fail "Đăng ký thất bại: ${reason:-mã thoát $rc}. Đã khôi phục UUID ${OLD_UUID:-cũ} và secret cũ."
fi
[ -n "$SVC_USER" ] && chown "$SVC_USER:" "$SECRET"
chmod 600 "$SECRET"

# 6. Khởi động lại và chờ phiên xác thực mới.
since="$(date '+%Y-%m-%d %H:%M:%S')"
systemctl start dronebridge
write_state running "Đã đăng ký UUID $UUID — chờ DroneBridge xác thực"
for _ in $(seq 60); do
    if journalctl -u dronebridge --since "$since" --no-pager -q -o cat 2>/dev/null |
        grep -q 'Authentication successful'; then
        write_state ok "Đã đăng ký và xác thực với server (UUID $UUID)"
        exit 0
    fi
    sleep 1
done
write_state ok "Đã đăng ký UUID $UUID; DroneBridge chưa xác thực xong sau 60 s — xem log dịch vụ"
