#!/bin/bash
# Kiểm tra camera — dùng API detect nếu dronebridge chạy, không thì rpicam trực tiếp
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "========== Camera registry =========="
REG=$(python3 -c "import json; r=json.load(open('Find_landing/camera_registry.json')); print(len(r['sensors']))" 2>/dev/null || echo 0)
echo "Hỗ trợ $REG loại sensor (xem Find_landing/camera_registry.json)"
echo ""

if curl -sf http://127.0.0.1:8080/api/camera/detect?refresh=1 >/tmp/cam_detect.json 2>/dev/null; then
    echo "========== Kết quả (web API / lưu cache) =========="
    python3 -m json.tool /tmp/cam_detect.json
    exit 0
fi

echo "========== rpicam-hello (trực tiếp) =========="
rpicam-hello --list-cameras 2>&1 || true
echo ""
echo "Chạy DroneBridge để dùng /api/camera/detect trên web local."
