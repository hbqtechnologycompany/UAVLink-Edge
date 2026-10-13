# Landing detection — v1 / v2

> Cập nhật: 2026-06-25  
> Liên quan: [CAMERA_PIPELINE_ARCHITECTURE.md](./CAMERA_PIPELINE_ARCHITECTURE.md) · [CAMERA_KNOWN_ISSUES.md](./CAMERA_KNOWN_ISSUES.md)

---

## Hai bản xử lý bãi đáp

Cùng pipeline Hướng 2 (lores CV), cùng output (offset, direction, overlay, telemetry). Khác **dấu hiệu vật lý** và **thuật toán**:

| Bản | Config | Bãi đáp in | Thuật toán |
|-----|--------|------------|------------|
| **v1** | `contour_h` | `templates/H.png` — chữ H + vòng tròn | Contour + so khớp hình H |
| **v2** | `aruco` | `templates/aruco_board_dict_4x4_50_0-11.png` — bảng 3×4 marker | ArUco board kiểu [ArduPilot AprilTag](https://ardupilot.org/dev/docs/ros-apriltag-detection.html) |

```
Find_landing/processing/detectors/
├── contour_h/           # v1
│   ├── detect.py        # detect_circles + recognize_H
│   └── template.py      # H.png
└── aruco/               # v2
    ├── detect.py        # bảng DICT_4X4_50, ID 0–11
    ├── marker.py        # sinh file in bảng marker
    └── processor.py
```

**OpenCV (v2):** cần `opencv-contrib` **≥ 4.7** (khuyến nghị **4.10**). `apt python3-opencv` 4.6 trên bookworm segfault với ArUco.

---

## Chuyển bản xử lý

### Web UI

1. **Settings → Camera setup → CV tuning (Hướng 2)**
2. **Detection method:**
   - `v1 — H contour` → `contour_h`
   - `v2 — ArUco ID 0` … `v2 — ArUco ID 11` → `aruco` + `aruco_marker_id`
3. **Save** → **Restart stream**

### config.yaml

```yaml
landing_detection_mode: aruco    # contour_h (v1) | aruco (v2)
detection_threshold: 0.8         # chỉ v1
landing_fullframe_fallback: true # chỉ v1
aruco_marker_id: 0               # chỉ v2 (0–11)
aruco_dictionary: DICT_4X4_50    # chỉ v2
```

### Kiểm tra

```bash
docker logs dronebridge 2>&1 | grep -E "Landing detection|aruco v2|contour"
curl -s http://127.0.0.1:8080/api/camera/landing?camera_id=0 | python3 -m json.tool
```

---

## In bãi đáp

### v1 — chữ H

```
Find_landing/templates/H.png
```

In và đặt trên sàn. Dùng khi chưa có marker ArUco.

### v2 — ArUco ID 0–11

Streamer tự tạo **12 marker đơn** + **1 bảng ghép**:

```
Find_landing/templates/aruco_dict_4x4_50_id0.png … id11.png
Find_landing/templates/aruco_board_dict_4x4_50_0-11.png
```

Settings → **Detection method** → chọn `v2 — ArUco ID N` (N = 0…11). Hệ thống chỉ track đúng ID đã chọn.

---

## Output chung

```python
{
  "detected": True,
  "detector": "contour_h" | "aruco",
  "h_position": (x, y),
  "offset_x": int,
  "offset_y": int,
  "direction": "LEFT UP" | "CENTER" | ...,
  "similarity": float,
}
```

v2 thêm: `aruco_visible_ids`, `aruco_marker_count`, `mode: "board"`.

API `/api/camera/landing` và overlay đọc schema này.
