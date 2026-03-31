# Network Management — Hướng dẫn Vận hành

> **Áp dụng cho:** Pi CM5 UAVLink-EdgeService  
> **Phiên bản: 3.0 (TCP Auth & Direct Forwarding)**  
> **Cập nhật: 2026-03-27**

---

## Tổng quan

UAVLink-Edge sử dụng **Policy-Based Routing (PBR)** để quản lý mạng theo nguyên tắc:

```
┌──────────────────────────────────────────────────────────────┐
│  SSH / apt / hệ thống                                        │
│  → default route: wlan0 (WiFi)   ← KHÔNG BAO GIỜ thay đổi  │
│                                                              │
│  UAVLink-Edge app (MAVLink + Auth)                            │
│  → routing table 100 (UAVLink-Edge)                          │
│     Khi 4G OK   : wwan0                                     │
│     Khi 4G down : wlan0 (fallback, tự động)                 │
└──────────────────────────────────────────────────────────────┘
```

**Kết quả:** SSH qua WiFi luôn ổn định dù UAVLink-Edge đang dùng 4G.

---

## Kiến trúc 3 tầng

| Tầng | Service | File | Quyền |
|------|---------|------|-------|
| 1 — Hardware Init | `UAVLink-Edge-4g-init.service` | `Module_4G/enable_4g_auto.py` | root |
| 2 — Routing Policy | `UAVLink-Edge-netmon.service` | `Module_4G/connection_manager.py` | root |
| 3 — Application | `UAVLink-Edge.service` | `UAVLink-Edge` binary | user `pi` |

Thứ tự khởi động: **4g-init → netmon → UAVLink-Edge**

---

## Cài đặt & Vận hành

### Bước 1: Deploy từ máy tính Ubuntu

Thay vì cài đặt thủ công, bạn hãy dùng script `deploy.sh` để tự động hóa toàn bộ (Build → Sync → Install).

1. **Chuẩn bị:** Đảm bảo máy Ubuntu có thể SSH vào Pi (`pi@cm5gw`).
2. **Chạy Deploy:**
   ```bash
   chmod +x deploy.sh
   ./deploy.sh pi@cm5gw
   ```
   *Script này sẽ tự động khởi tạo thư mục `/opt/UAVLink-Edge`, cài đặt 3 Systemd Services và cấu hình PBR (Table 100) cho bạn.*

### Bước 2: Cấu hình Fleet Server

Chỉnh sửa file `/opt/UAVLink-Edge/config.yaml` để khai báo địa chỉ Fleet Server và API Key.

---

## Quản lý từ xa (Makefile)

Nếu bạn đang ở trên máy Ubuntu (cùng mạng WiFi/LAN), bạn có thể quản lý Pi nhanh bằng `make`:

- **Xem log:** `make logs PI_HOST=pi@cm5gw`
- **Xem trạng thái:** `make status PI_HOST=pi@cm5gw`
- **Khởi động lại:** `make restart PI_HOST=pi@cm5gw`
- **Kiểm tra mạng:** `make network-status PI_HOST=pi@cm5gw`

---

## Vận hành hằng ngày

### Xem trạng thái tổng quan

```bash
# Status 3 services cùng lúc
sudo systemctl status "UAVLink-Edge*"

# Hoặc dùng script status của connection_manager
sudo python3 /opt/UAVLink-Edge/Module_4G/connection_manager.py status
```

Kết quả mẫu:
```
4G:   ✅ 10.100.x.x
WiFi: ✅ 192.168.1.x
PBR table 100: default dev wwan0
System default: default via 192.168.1.1 dev wlan0
```

### Xem log real-time

```bash
# Log 4G init
journalctl -u UAVLink-Edge-4g-init -f

# Log network monitor (failover events, PBR changes)
journalctl -u UAVLink-Edge-netmon -f

# Log UAVLink-Edge application
journalctl -u UAVLink-Edge -f
```

### Xem routing thực tế

```bash
# Route hệ thống (phải luôn có wlan0)
ip route show

# Route của UAVLink-Edge (table 100)
ip route show table UAVLink-Edge

# ip rule (phải có fwmark)
ip rule show
# 100:     from all fwmark 0x1 lookup UAVLink-Edge
```

---

## Test & Debug

### Test failover thủ công

1. **Giả lập 4G down:** `sudo ip link set wwan0 down`
2. **Chờ ~30s:** `connection_manager` sẽ detect và switch UAVLink-Edge sang WiFi.
3. **Kiểm tra SSH:** Kết nối SSH qua WiFi phải KHÔNG bị ảnh hưởng.
4. **Restore 4G:** `sudo ip link set wwan0 up` (Hệ thống sẽ tự switch về 4G).

### Test PBR có hoạt động không

```bash
# Chạy tcpdump để xem UAVLink-Edge traffic đi interface nào
sudo tcpdump -i wwan0 udp port 14550 -nn -c 5
# Phải thấy MAVLink packets khi 4G đang active
```

---

## Xử lý sự cố thường gặp

### SSH mất kết nối khi switch mạng

**Nguyên nhân:** PBR chưa được setup (mark chưa có) nên khi connection_manager thay đổi route, SSH traffic cũng bị ảnh hưởng.

**Fix:**
```bash
sudo systemctl restart UAVLink-Edge-netmon
```

### UAVLink-Edge vẫn đi WiFi dù 4G đã UP

**Kiểm tra PBR table:**
```bash
ip route show table UAVLink-Edge
```

**Fix:**
```bash
sudo systemctl restart UAVLink-Edge-netmon
```

---

## Cấu trúc file

```
/opt/UAVLink-Edge/
├── Module_4G/
│   ├── enable_4g_auto.py       ← Tầng 1: 4G HW init
│   └── connection_manager.py  ← Tầng 2: PBR routing policy
├── etc/
│   └── systemd/
│       ├── UAVLink-Edge-4g-init.service   ← Systemd service Tầng 1
│       ├── UAVLink-Edge-netmon.service    ← Systemd service Tầng 2
│       └── UAVLink-Edge.service           ← Systemd service Tầng 3
├── UAVLink-Edge                ← Binary compiled (Tầng 3)
└── docs/
    ├── NETWORK_MANAGEMENT.md  ← File này
```

---

## Tham khảo nhanh

| Việc cần làm | Lệnh |
|---|---|
| Xem status 3 services | `systemctl status "UAVLink-Edge*"` |
| Xem log netmon live | `journalctl -u UAVLink-Edge-netmon -f` |
| Xem route UAVLink-Edge | `ip route show table UAVLink-Edge` |
| Force reinit 4G | `sudo systemctl restart UAVLink-Edge-4g-init` |
| Status nhanh | `sudo python3 /opt/UAVLink-Edge/Module_4G/connection_manager.py status` |
