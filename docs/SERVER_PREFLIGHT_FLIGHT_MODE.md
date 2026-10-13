# Server bug: pre-flight `system.flight_mode` = UNKNOWN

**Ngày:** 2026-06-30  
**Drone thật:** CM5 + PX4 (sys_id=1), QGC Inspector decode đúng mode  
**Cloud QCC pre-flight:** `Chế độ robot: UNKNOWN` — `thiếu system.flight_mode`

---

## Triệu chứng

| Nơi | Kết quả |
|-----|---------|
| QGC MAVLink Inspector | `HEARTBEAT` sys 1, `autopilot=12` (PX4), `custom_mode=50593792` → **Hold / LOITER** |
| CM5 `/api/status` | `flight_mode: "Hold"`, `flight_mode_px4: "LOITER"`, `custom_mode: 50593792` |
| Cloud QCC pre-flight | `UNKNOWN` — server **không map** `HEARTBEAT` PX4 → `system.flight_mode` |

CM5 **đã forward** hàng trăm frame `HEARTBEAT` sys 1 lên server (không sửa `custom_mode`).

---

## Nguyên nhân (server)

Server ingest MAVLink nhưng **chưa implement decode chế độ bay PX4** giống QGroundControl:

1. Không đọc `HEARTBEAT` từ `sys_id == pixhawk_sys_id` (thường **1**).
2. Không kiểm tra `autopilot == MAV_AUTOPILOT_PX4` (**12**).
3. Không giải mã `custom_mode` theo layout PX4 (`main` @ bit 16–23, `sub` @ bit 24–31).
4. (Tuỳ chọn) Chưa dùng `CURRENT_MODE` (msg **436**) khi PX4 stream message này.

**Không phải lỗi CM5:** companion sys 245 / msg 42998 **không** chứa flight mode (chỉ GPS diagnosis + camera).

---

## Chuẩn decode — giống QGC + PX4

### Bước 1: Chọn nguồn

```
Ưu tiên CURRENT_MODE (436) từ sys_id PX4 nếu có custom_mode != 0
Ngược lại: HEARTBEAT (0) từ sys_id PX4 khi autopilot == 12 (PX4)
```

### Bước 2: Tách `custom_mode` (uint32)

```python
def decode_px4_custom_mode(custom_mode: int) -> tuple[int, int]:
    main = (custom_mode >> 16) & 0xFF
    sub  = (custom_mode >> 24) & 0xFF
    return main, sub
```

Tham chiếu: PX4 `px4_custom_mode`, QGC `FirmwarePluginPX4`.

### Bước 3: Map sang tên hiển thị (QGC toolbar)

| main | sub | `flight_mode_px4` | `flight_mode` (QGC UI) |
|------|-----|-------------------|------------------------|
| 1 | 0 | MANUAL | Manual |
| 2 | 0 | ALTCTL | Altitude |
| 3 | 0 | POSCTL | Position |
| 4 | 1 | READY | Ready |
| 4 | 2 | TAKEOFF | Takeoff |
| 4 | 3 | LOITER | **Hold** |
| 4 | 4 | MISSION | Mission |
| 4 | 5 | RTL | Return |
| 4 | 6 | LAND | Land |
| 4 | 7 | RTGS | Return to Groundstation |
| 4 | 8 | FOLLOW | Follow Me |
| 5 | 0 | ACRO | Acro |
| 6 | 0 | OFFBOARD | Offboard |
| 7 | 0 | STAB | Stabilized |
| 8 | 0 | RATTITUDE | Rattitude |

**Drone hiện tại:** `custom_mode = 50593792` (= `0x03040000`) → main=**4**, sub=**3** → `flight_mode_px4=LOITER`, `flight_mode=Hold`.

### Bước 4: Ghi telemetry builder

```python
if heartbeat.sysid == pixhawk_sys_id and heartbeat.autopilot == MAV_AUTOPILOT_PX4:
    main, sub = decode_px4_custom_mode(heartbeat.custom_mode)
    system.flight_mode = qgc_name(main, sub)      # "Hold" — dùng cho UI pre-flight
    system.flight_mode_px4 = px4_name(main, sub) # "LOITER" — debug
    system.custom_mode = heartbeat.custom_mode
    system.main_mode = main
    system.sub_mode = sub
    system.flight_mode_source = "HEARTBEAT"
    system.autopilot = "PX4"
```

**QCC pre-flight nên đọc `system.flight_mode`** (chuỗi QGC, ví dụ `"Hold"`), không đọc sys 245 / 42998.

---

## Ví dụ MAVLink thật (drone đang test)

```
HEARTBEAT sys=1 comp=1
  type=2 (MAV_TYPE_QUADROTOR)
  autopilot=12 (MAV_AUTOPILOT_PX4)
  base_mode=0x01 (CUSTOM_MODE_ENABLED)
  custom_mode=50593792
  system_status=0 (MAV_STATE_UNINIT)   ← PX4 thật; simulator thường gửi STANDBY(3)
```

Decode:

```
main_mode = 4
sub_mode  = 3
flight_mode_px4 = LOITER
flight_mode     = Hold
```

---

## CM5 đã làm (reference implementation)

Package `px4mode/` — cùng thuật toán QGC:

- `FromHeartbeat()` — chỉ parse khi `autopilot == PX4`
- `FromCurrentMode()` — msg 436
- `NameQGC()` / `namePX4()` — bảng map ở trên

`/api/status` trả về:

```json
{
  "flight_mode": "Hold",
  "flight_mode_px4": "LOITER",
  "main_mode": 4,
  "sub_mode": 3,
  "custom_mode": 50593792,
  "flight_mode_source": "HEARTBEAT",
  "system": {
    "flight_mode": "Hold",
    "flight_mode_px4": "LOITER",
    "main_mode": 4,
    "sub_mode": 3,
    "custom_mode": 50593792,
    "flight_mode_source": "HEARTBEAT",
    "autopilot": "PX4"
  }
}
```

Server nên mirror cùng schema vào object `system` gửi QCC.

---

## So sánh simulator (không phải drone thật)

| | Drone thật (PX4) | PX4_Mavlink_Drone_Simulator |
|--|------------------|----------------------------|
| `HEARTBEAT` sys | 1 (Pixhawk) | 1 |
| `custom_mode` | **50593792** (LOITER) | **0** |
| `system_status` | 0 (UNINIT) | 3 (STANDBY) |
| Pre-flight mode | Cần decode `custom_mode` | Simulator không mô phỏng mode thật |

Server **không được** coi `custom_mode=0` là đủ khi drone thật đang ở LOITER.

---

## Lỗi phụ (không chặn flight mode)

| Vấn đề | Ảnh hưởng |
|--------|-----------|
| Msg **42998** chưa parse (`UNKNOWN_42998`) | GPS diagnosis / camera live trên cloud — **không** ảnh hưởng mode |
| Pre-flight yêu cầu `system_status >= STANDBY` | PX4 thật có thể báo `UNINIT(0)` trong khi mode vẫn hợp lệ — **không** dùng làm điều kiện duy nhất cho `flight_mode` |

---

## Checklist sửa server

- [ ] Parse `HEARTBEAT` sys **1**, `autopilot=12`, decode `custom_mode` như bảng trên
- [ ] (Khuyến nghị) Parse `CURRENT_MODE` (436) khi có
- [ ] Set `system.flight_mode` = nhãn QGC (`Hold`, `Position`, …)
- [ ] Không lấy mode từ sys **245** / msg **42998**
- [ ] Log 30s: đếm `HEARTBEAT` sys 1 + giá trị `custom_mode` sau decode

### Kiểm tra trên CM5

```bash
curl -s http://127.0.0.1:8080/api/status | python3 -c "
import json,sys
d=json.load(sys.stdin)
print('flight_mode:', d.get('flight_mode'))
print('flight_mode_px4:', d.get('flight_mode_px4'))
print('custom_mode:', d.get('custom_mode'))
print('system:', d.get('system'))
"
```

Kỳ vọng: `flight_mode=Hold`, `flight_mode_px4=LOITER`, `custom_mode=50593792`.

---

## Tài liệu liên quan

- `docs/MAVLINK_QCC_CAM_GPS.md` — phạm vi msg 42998, session HB
- `px4mode/mode.go` — implementation CM5 (copy logic sang server)
- QGC: `FirmwarePluginPX4` flight mode map
- PX4: `px4_custom_mode` union trong firmware
