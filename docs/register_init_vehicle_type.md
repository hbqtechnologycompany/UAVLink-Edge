# REGISTER_INIT — `vehicle_type` & `model`

Hướng dẫn ngắn gọn để Drone client gửi loại phương tiện và tên model khi đăng ký lần đầu (`--register`).

## Tóm tắt

| Trường | Kiểu | Lưu vào DB | Mục đích |
|--------|------|------------|----------|
| `vehicle_type` | `uint8` (enum HEARTBEAT.type) | `drones.hardware_profile` | Chọn profile UI / hành vi (quadrotor, rover, …) |
| `model` | string ≤ 32 ký tự | `drones.model` | Nhãn tùy chỉnh (ví dụ `Pi5-UGV-Lab`) |

**Lưu ý:** `model` **không** dùng để suy ra profile. Pilot UI đọc `hardware_profile` (map từ `vehicle_type`).

---

## Wire format (REGISTER_INIT `0xA0`)

**Legacy (v1)** — chỉ UUID, vẫn hỗ trợ:

```
[0xA0][UUID_LEN:2 LE][UUID]
```

→ Backend mặc định `hardware_profile = quadrotor`, `model` rỗng.

**Mở rộng (v2)** — thêm trailer khi client có `vehicle_type` hoặc `model`:

```
[0xA0][UUID_LEN:2 LE][UUID][VehicleType:1][ModelLen:1][Model:0..32]
```

- Trailer chỉ gửi khi `vehicle_type != 0` **hoặc** `model != ""`.
- `vehicle_type = 0` trong trailer → xử lý như quadrotor (`2`).
- Enum không hỗ trợ → fallback `quadrotor`.

---

## Bảng `vehicle_type` (enum)

Giá trị khớp trường `type` trong MAVLink HEARTBEAT.

| Giá trị | Loại | `hardware_profile` |
|--------:|------|--------------------|
| 1 | Fixed wing | `fixed_wing` |
| 2 | Quadrotor | `quadrotor` *(mặc định)* |
| 4 | Helicopter | `helicopter` |
| 10 | Ground rover | `rover` |
| 12 | Submarine | `submarine` |
| 13 | Hexarotor | `hexarotor` |
| 14 | Octorotor | `octorotor` |
| 22 | VTOL fixed-rotor | `vtol` |

Hằng số tham chiếu (Go): `Pi_CM5_DroneBridgeService/auth/vehicletype.go`.

---

## Cách dùng với DroneBridge (Pi)

### 1. Cấu hình `config.yaml`

```yaml
auth:
  uuid: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
  shared_secret: "your-shared-secret"
  vehicle_type: 10          # Ground rover → hardware_profile = rover
  model: "Pi5-UGV-Lab"      # tùy chọn, ≤ 32 ký tự
```

Bỏ qua cả hai trường (hoặc `vehicle_type: 0` và `model: ""`) → gói legacy v1.

### 2. Chạy đăng ký một lần

```bash
./DroneBridge --register --config config.yaml
```

Flow không đổi: `REGISTER_INIT` → `REGISTER_CHALLENGE` → `REGISTER_RESPONSE` → `REGISTER_ACK`.

`main.go` đã gọi `authClient.SetRegistrationMeta(cfg.Auth.VehicleType, cfg.Auth.Model)` trước `Register()`.

---

## Tích hợp client tùy chỉnh (Go)

```go
init := &auth.RegisterInit{
    DroneUUID:   droneUUID,
    VehicleType: 10,              // uint8 enum
    Model:       "My-Robot-v1",
}
packet := auth.SerializeRegisterInit(init)
conn.Write(packet)
```

Hoặc qua auth client:

```go
client.SetRegistrationMeta(10, "My-Robot-v1")
client.Register()
```

Client khác ngôn ngữ: serialize đúng layout byte ở trên; không cần đổi `REGISTER_RESPONSE` / HMAC.

---

## Luồng server

```
Drone --REGISTER_INIT(v2)--> Router --POST /api/v1/drones/register-init--> Backend
                                                              (lưu pending vehicle_type + model)
Drone <--REGISTER_CHALLENGE-- Router
Drone --REGISTER_RESPONSE--> Router --POST /api/v1/drones/register-verify--> Backend
                                                              (tạo drone: model + hardware_profile)
```

JSON backend (`register-init`):

```json
{ "uuid": "...", "vehicle_type": 10, "model": "Pi5-UGV-Lab" }
```

---

## Kiểm tra sau đăng ký

- DB: `drones.hardware_profile` = profile map từ enum; `drones.model` = chuỗi custom (nếu có).
- Pilot UI: đọc `hardware_profile` khi login (`rover` → UGV UI nếu domain phù hợp).
- Client cũ (chỉ UUID): vẫn đăng ký được, profile mặc định `quadrotor`.
