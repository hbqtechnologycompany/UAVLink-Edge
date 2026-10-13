#!/bin/bash
# Tự áp dụng boot overlay + reboot khi camera_detected.json đổi overlay (web UI lưu loại CAM).
# Chạy bởi dronebridge-camera-watch.service (root).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
# shellcheck source=../lib/root.sh
source "$SCRIPT_DIR/../lib/root.sh"
INSTALL_DIR="$(dronebridge_root)"
DETECTED="$INSTALL_DIR/Find_landing/camera_detected.json"
CONFIG="$INSTALL_DIR/config.yaml"
FP_FILE="/run/dronebridge/camera-overlay.fp"
DEBOUNCE_SEC=2

[ -f "$DETECTED" ] || exit 0

if ! command -v inotifywait >/dev/null 2>&1; then
    echo "inotifywait chưa cài — apt install inotify-tools" >&2
    exit 1
fi

overlay_fingerprint() {
    python3 - "$1" <<'PY'
import json, sys
st = json.load(open(sys.argv[1]))
parts = []
for port in ("cam0", "cam1"):
    cfg = st.get("ports", {}).get(port, {})
    ov = cfg.get("overlay") or cfg.get("sensor") or ""
    en = "1" if cfg.get("enabled", True) else "0"
    parts.append(f"{port}:{ov}:{en}")
print("|".join(parts))
PY
}

logger -t dronebridge "Camera overlay watcher started ($DETECTED)"
TRIGGER="/run/dronebridge/apply-camera-overlay.request"
mkdir -p /run/dronebridge
overlay_fingerprint "$DETECTED" > "$FP_FILE"

apply_overlay() {
    logger -t dronebridge "applying camera overlay"
    if bash "$SCRIPT_DIR/apply_camera_overlay.sh" "$CONFIG" 2>&1 | logger -t dronebridge; then
        overlay_fingerprint "$DETECTED" > "$FP_FILE"
        rm -f "$TRIGGER" 2>/dev/null || true
    fi
}

apply_once() {
    sleep "$DEBOUNCE_SEC"
    local fp
    fp="$(overlay_fingerprint "$DETECTED")"
    if [ -f "$FP_FILE" ] && [ "$(cat "$FP_FILE")" = "$fp" ]; then
        logger -t dronebridge "camera_detected.json touch only — overlay unchanged, skip"
        return 0
    fi
    logger -t dronebridge "camera overlay changed"
    apply_overlay
}

while true; do
    inotifywait -q -e close_write,moved_to,create -t 3600 "$DETECTED" "$TRIGGER" 2>/dev/null || continue
    if [ -f "$TRIGGER" ]; then
        apply_overlay
    else
        apply_once
    fi
done
