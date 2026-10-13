# Camera — Triệu chứng & chẩn đoán

> Cập nhật: **2026-06-24**  
> **Kiến trúc:** [CAMERA_PIPELINE_ARCHITECTURE.md](./CAMERA_PIPELINE_ARCHITECTURE.md)  
> **Detection modes:** [LANDING_DETECTION.md](./LANDING_DETECTION.md)

---

## Triệu chứng

| Thời điểm | QCC viewer | Publisher Pi (`fps_window`) |
|-----------|------------|----------------------------|
| 0–2 phút | ~30 FPS | ~25–30 |
| ~5 phút | **~13 FPS** (đỏ) | tuột tương ứng |
| Sau đó | Mất hình / phải reset web | Streamer chết hoặc gst orphan |

```bash
curl -s http://127.0.0.1:8080/api/camera/status | python3 -m json.tool
```

---

## Root cause đã xác minh

### RC-1 — Backpressure end-to-end

Mạng chậm → pipe/RTSP block → encode stall → FPS giảm.

**Giảm nhẹ:** `h264_cv_loop` (HW H264, không raw 900KB/frame).

### RC-3 — CV + stream cùng process

Đã tách: CV trên lores async; main chỉ burn overlay latest.

### RC-6 — CAM0 vs CAM1 khác config

Cùng code `camera_streamer.py`; khác `camera_config_*.json` (`detection_enabled`, `overlay_enabled`).

### RC-7 — Detection nhảy sai (contour_h)

- Thuật toán **không dùng màu** — chỉ grayscale + Canny + `matchShapes`
- Nền nhiễu → false positive; đã thêm ranking + stability 4/5 frame
- **Khuyến nghị nền nhiễu:** chuyển `landing_detection_mode: aruco` — xem [LANDING_DETECTION.md](./LANDING_DETECTION.md)

### RC-8 — Stream format không đổi màu (cũ)

Trước 2026-06-24: H264 path bỏ qua `format`. **Đã fix:** `apply_sensor_to_ui_wire()` trong `h264_cv_loop` pre_callback.

### RC-9 — Dual-cam libcamera

Nếu stream **H2 ON** (Picamera2) khởi động sau stream **H1 HW** (`rpicam-vid`) trên cam khác, Picamera2 có thể chết sau ~1–2s. **Giải pháp:** Go launcher khởi động stream có CV trước, chờ stagger, rồi stream HW — **không** đổi path H1 của từng camera vì camera kia có ArUco/CV.

### RC-10 — ArUco + OpenCV trong Docker

`apt python3-opencv` trên bookworm+rpi là **4.6.0** — `cv2.aruco.detectMarkers` **segfault** trên ARM64. Image DroneBridge dùng pip `opencv-contrib-python-headless==4.10.0.84` (khớp host Pi). Build chạy `verify-opencv-aruco.py`. Đổi version: `docker compose build --build-arg OPENCV_CONTRIB_PIP_VERSION=...`.

---

## Lệnh chẩn đoán

```bash
# FPS + landing
curl -s http://127.0.0.1:8080/api/camera/landing?camera_id=1 | python3 -m json.tool
docker logs dronebridge 2>&1 | grep -E "Stats:|H264\+CV|Landing detection|contour_h|aruco"

# Process
ps aux | grep -E "camera_streamer|ffmpeg" | grep -v grep

# Config runtime
cat /opt/dronebridge/Find_landing/camera_config_1.json | python3 -m json.tool

# Mode + format
jq '{mode:.landing_detection_mode, format:.format, det:.detection_enabled}' \
  /opt/dronebridge/Find_landing/camera_config_1.json
```

---

## Đã làm (2026-06-24)

- [x] `h264_cv_loop` ~30 FPS + lores CV
- [x] Detectors tách folder: `contour_h`, `aruco`
- [x] `landing_detection_mode` + UI Detection method
- [x] Wire format RGB/BGR trên H264 path
- [x] `/api/camera/landing` + overlay burn
- [x] Stability + jump filter giảm nhảy detection

## Chưa làm

- [ ] MediaMTX local hop (I-7)
