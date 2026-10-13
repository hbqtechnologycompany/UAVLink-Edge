#!/bin/bash
# DroneBridge container entrypoint — verify deps, seed config, exec main process.
set -euo pipefail

INSTALL_DIR="/opt/dronebridge"
cd "$INSTALL_DIR"

mkdir -p Find_landing data

if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
    echo "[entrypoint] config.yaml missing — copy from image example"
    cp -f "$INSTALL_DIR/config.yaml.example" "$INSTALL_DIR/config.yaml"
fi

echo "[entrypoint] Dependency check:"
python3 -c "import cv2; print('  opencv:', cv2.__version__, '(pip contrib)')" 2>/dev/null \
    || echo "  WARN: cv2 not importable"
if command -v verify-opencv-aruco.py >/dev/null; then
    verify-opencv-aruco.py || echo "  WARN: ArUco verify failed — landing_detection_mode=aruco may crash"
fi
python3 -c "import picamera2; print('  picamera2: OK')" 2>/dev/null || echo "  WARN: python3-picamera2 not importable"
command -v gst-launch-1.0 >/dev/null && echo "  gstreamer: $(gst-launch-1.0 --version 2>&1 | head -1)" || echo "  WARN: gst-launch-1.0 missing"
command -v rpicam-vid >/dev/null && echo "  rpicam-vid: OK" || echo "  WARN: rpicam-vid missing (HW passthrough may fail)"

if [ ! -x "$INSTALL_DIR/dronebridge" ]; then
    echo "[entrypoint] ERROR: dronebridge binary not found" >&2
    exit 1
fi

echo "[entrypoint] Starting: $*"
exec "$@"
