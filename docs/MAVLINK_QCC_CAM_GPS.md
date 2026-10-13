# MAVLink QCC — CAM & GPS (tóm tắt kỹ thuật)

> Drone: `9f14f259-576c-41ad-90f9-783e6d831455` · VPN `10.8.1.18` → server `10.8.0.1:14550`  
> Cập nhật: 2026-06-30

---

## 1. CM5 đang đẩy gì — đúng chuẩn nào?

### Camera (generated CM5, không qua PX4 firmware)

| Trường | Giá trị | Chuẩn |
|--------|---------|-------|
| `system_id` | **1** (cùng PX4) | Camera trên cùng máy bay |
| `component_id` | **100** | `MAV_COMP_ID_CAMERA` |

| msg_id | Tên | Hz | Payload chính |
|--------|-----|----|---------------|
| **0** | `HEARTBEAT` | ~1 | `type=MAV_TYPE_CAMERA`, `system_status=ACTIVE` |
| **270** | `VIDEO_STREAM_STATUS` | ~1/stream | `flags=RUNNING`, FPS, `resolution_h/v`, `bitrate` |
| **269** | `VIDEO_STREAM_INFORMATION` | ~0.1 | `type=RTSP`, `uri=rtsp://…/cam0`, `encoding=H264` |

Code: `camera/camera_mavlink.go` → `forwarder.PublishCamera()`.

**CM5 xác nhận** (`GET /api/status`):

```
camera_sys_id=1  camera_comp_id=100
camera0_live=true  camera1_live=true
VideoStreamStatus=381+  VideoStreamInformation=38+
```

### Companion diagnostic (generated CM5)

| Trường | Giá trị |
|--------|---------|
| `system_id` | **245** |
| `component_id` | **191** |

| msg_id | Tên | Hz | Payload chính |
|--------|-----|----|---------------|
| **42998** | `DRONEBRIDGE_STATUS` | 1 | `gps_diagnosis=4`, `gps_fix_type=255`, `gps_satellites=0`, `camera0_live=1`, `camera1_live=1` |
| **42999** | `MAVLINK_KEEPALIVE` | ~0.33 | session token — server **đã xử lý** |

### PX4 (forward nguyên xi, `sys=1`, `comp=1`)

Ví dụ 30s gần nhất trên server: `ATTITUDE` ~1306, `LOCAL_POSITION_NED` ~324, `GPS_RAW_INT` ~294, `GLOBAL_POSITION_INT` ~294.

---

## 2. Server nhận được gì — log thực tế (30s, 16:15:28)

```
Parse: Success 4933 / Failure 0
Drone[1]:   4901 msg    ← PX4 + camera (sys=1)
Drone[245]:   30 msg    ← chỉ 42998

VIDEO_STREAM_STATUS:      30  ✅ parse OK → Redis → DB (11)
VIDEO_STREAM_INFORMATION:  3  ✅ parse OK → Redis → DB (3)
HEARTBEAT:                90  ✅ (PX4 + camera gộp chung)
UNKNOWN_42998:            30  ❌ không parse
RAW_MSG_ID_42998:         30  ❌ classified unknown
```

**Kết luận uplink camera:** gói **đúng chuẩn**, server **decode được** msg 269/270 trên `sys=1`. Không thiếu frame, không sai `system_id`.

---

## 3. Vấn đề — server nhận nhưng không xử lý cho UI

### CAM — đèn không nháy

| Bước | Trạng thái | Chi tiết |
|------|------------|----------|
| UDP nhận | ✅ | `VIDEO_STREAM_STATUS` 30 gói/30s từ `10.8.1.18` |
| MAVLink parse | ✅ | Không nằm trong `Unknown message type` |
| Redis publish | ✅ | Có trong `Message Types Received by Redis` |
| DB ghi | ✅ | `VIDEO_STREAM_STATUS: 11` |
| **Viewer đèn CAM** | ❌ | **Không có topic/handler camera** — Redis chỉ có `telemetry.attitude`, `telemetry.hud`, `telemetry.position`… **không có** `telemetry.camera` hay rule map msg **270** → đèn CAM |

**Nguyên nhân:** pipeline server **lưu telemetry** nhưng **viewer LED bar chưa subscribe** `VIDEO_STREAM_STATUS` / `HEARTBEAT MAV_TYPE_CAMERA` từ `comp=100`.

**Không phải lỗi drone:** RTSP có hình; MAVLink camera đã lên server.

### GPS — đèn nháy sai / text 0 vệ tinh

| Dữ liệu server nhận (30s) | Server làm gì | Đúng? |
|---------------------------|---------------|-------|
| `LOCAL_POSITION_NED` ×324 | Map → đèn position/GPS | ❌ không phải GPS global |
| `ALTITUDE` ×30 | Có thể kích hoạt đèn position | ❌ baro, không phải GPS |
| `GPS_RAW_INT` ×294 | Parse OK | Viewer cần đọc `fix_type` + `satellites_visible` cho đèn GPS |
| `DRONEBRIDGE_STATUS` ×30 | `UNKNOWN_42998` — **bỏ qua** | ❌ CM5 đã gửi `gps_diagnosis=4` (không có GPS thật) nhưng server không đọc |

**Nguyên nhân:** viewer bật đèn GPS theo **hoạt động message position** (`LOCAL_POSITION_NED`), không theo `GPS_RAW_INT.fix_type`. Msg **42998** chưa có parser → không override logic sai.

### 42998 — custom message

```
Server log: RAW_MSG_ID_42998: 30  →  UNKNOWN_42998: 30
Drone[245]: 30  (= đúng 30 gói companion, không lẫn camera)
```

Server **chưa đăng ký** msg_id 42998 trong dialect → mọi field (`gps_diagnosis`, `camera0_live`) bị drop.

---

## 4. Đối chiếu nhanh

| | CM5 gửi | Server parse | Server → đèn UI |
|--|---------|--------------|-----------------|
| `VIDEO_STREAM_STATUS` (270) | ✅ sys=1 comp=100 ~1Hz | ✅ | ❌ chưa map CAM |
| `VIDEO_STREAM_INFORMATION` (269) | ✅ ~0.1Hz | ✅ | ❌ chưa map CAM |
| `HEARTBEAT` camera (0) | ✅ type=CAMERA | ✅ (gộp HB) | ❌ không tách comp=100 |
| `DRONEBRIDGE_STATUS` (42998) | ✅ sys=245 ~1Hz | ❌ UNKNOWN | ❌ |
| `LOCAL_POSITION_NED` (32) | forward PX4 | ✅ | ⚠️ nháy GPS sai |
| `GPS_RAW_INT` (24) | forward PX4 | ✅ | ⚠️ viewer chưa dùng đúng cho đèn |

---

## 5. Server cần sửa gì

**CAM**
1. Map `VIDEO_STREAM_STATUS` (`comp=100`, `flags&RUNNING`) → đèn CAM nháy.
2. Hoặc parse `DRONEBRIDGE_STATUS.camera0_live` (42998) sau khi thêm dialect.

**GPS**
1. Đèn GPS ← `GPS_RAW_INT.fix_type ≥ 3` **và** `satellites_visible > 0`.
2. **Không** dùng `LOCAL_POSITION_NED` / `ALTITUDE` cho đèn GPS.
3. Thêm parser msg **42998**; khi `gps_diagnosis=4` → tắt đèn GPS.

**42998 layout** (`mavlink_custom/custom.go`):  
`timestamp_ms(u32) | gps_fix_type(u8) | gps_satellites(u8) | gps_px4_streaming(u8) | gps_diagnosis(u8) | camera0_live(u8) | camera1_live(u8) | reserved(u8)`

---

## 6. Kiểm tra

```bash
# CM5
curl -s http://127.0.0.1:8080/api/status | python3 -m json.tool | grep -A12 mavlink_diagnostics

# Server — tìm trong log 30s stats:
#   VIDEO_STREAM_STATUS > 0  → drone OK
#   UNKNOWN_42998            → server chưa parse companion
#   telemetry.* topics       → không có camera → CAM LED sẽ tối
```

**Kết luận:** Drone **đã đẩy đúng**. CAM/GPS sai trên QCC là **thiếu handler UI + chưa parse 42998** phía server.
