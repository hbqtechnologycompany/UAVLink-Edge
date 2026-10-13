# Network Management — Hướng dẫn Vận hành

> **Áp dụng cho:** Pi CM5 DroneBridgeService  
> **Phiên bản:** 2.0 (Policy-Based Routing)  
> **Cập nhật:** 2026-03-23

---

## Tổng quan

DroneBridge sử dụng **Policy-Based Routing (PBR)** để quản lý mạng theo nguyên tắc:

```
┌──────────────────────────────────────────────────────────────┐
│  SSH / apt / hệ thống                                        │
│  → default route: wlan0 (WiFi)   ← KHÔNG BAO GIỜ thay đổi  │
│                                                              │
│  DroneBridge app (MAVLink + VPN)                             │
│  → routing table 100 (dronebridge)                          │
│     Khi 4G OK   : wwan0                                     │
│     Khi 4G down : wlan0 (fallback, tự động)                 │
└──────────────────────────────────────────────────────────────┘
```

**Kết quả:** SSH qua WiFi luôn ổn định dù DroneBridge đang dùng 4G.

---

## Kiến trúc 3 tầng

| Tầng | Service | File | Quyền |
|------|---------|------|-------|
| 1 — Hardware Init | `dronebridge-4g-init.service` | `Module_4G/enable_4g_auto.py` | root |
| 2 — Routing Policy | `dronebridge-netmon.service` | `Module_4G/connection_manager.py` | root |
| 3 — Application | `dronebridge.service` | `dronebridge` binary | user `pi` |

Thứ tự khởi động: **4g-init → netmon → dronebridge**

---

## Cài đặt & Đăng ký

### Bước 1: Deploy từ máy tính Ubuntu

Thay vì cài đặt thủ công, bạn hãy dùng script `deploy.sh` để tự động hóa toàn bộ (Build → Sync → Install).

1. **Chuẩn bị:** Đảm bảo máy Ubuntu có thể SSH vào Pi (`pi@cm5gw`).
2. **Chạy Deploy:**
   ```bash
   chmod +x deploy.sh
   ./deploy.sh pi@cm5gw
   ```
   *Script này sẽ tự động khởi tạo thư mục `/opt/dronebridge`, cài đặt 3 Systemd Services và cấu hình PBR (Table 100) cho bạn.*

### Bước 2: Đăng ký Drone (Một lần duy nhất)

Nếu đây là lần đầu cài đặt, bạn cần cấp `secret_key` cho drone:

1. **Dừng app:** `sudo systemctl stop dronebridge`
2. **Chạy đăng ký:**
   ```bash
   cd /opt/dronebridge
   sudo ./dronebridge --config config.yaml --register
   ```
3. **Khởi động lại:** `sudo systemctl start dronebridge`

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
sudo systemctl status "dronebridge*"

# Hoặc dùng script status của connection_manager
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py status
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
journalctl -u dronebridge-4g-init -f

# Log network monitor (failover events, PBR changes)
journalctl -u dronebridge-netmon -f

# Log DroneBridge application
journalctl -u dronebridge -f

# Tất cả DroneBridge services
journalctl -u "dronebridge*" -f --since "10 minutes ago"
```

### Xem routing thực tế

```bash
# Route hệ thống (phải luôn có wlan0)
ip route show
# default via 192.168.1.1 dev wlan0 proto dhcp metric 100  ← SSH đi đây

# Route của DroneBridge (table 100)
ip route show table dronebridge
# default dev wwan0  ← khi 4G active
# default via 192.168.1.1 dev wlan0  ← khi fallback WiFi

# ip rule (phải có fwmark)
ip rule show
# 0:       from all lookup local
# 100:     from all fwmark 0x1 lookup dronebridge  ← DroneBridge rule
# 32766:   from all lookup main
```

### Kiểm tra trạng thái mạng từ file

```bash
cat /run/dronebridge/network_status.json
```

Kết quả mẫu:
```json
{
  "timestamp": 1711180200,
  "active_interface": "wwan0",
  "pbr_table": 100,
  "4g": {"ip": "10.100.x.x", "online": true},
  "wifi": {"ip": "192.168.1.x", "online": true},
  "ssh_route": "wlan0 (system default — unchanged)"
}
```

---

## Test & Debug

### Test failover thủ công

```bash
# Giả lập 4G down (mô phỏng mất sóng)
sudo ip link set wwan0 down

# Chờ ~30s → connection_manager sẽ detect và switch DroneBridge sang WiFi
ip route show table dronebridge
# Kết quả: default via 192.168.1.1 dev wlan0  ← đã switch

# Kiểm tra SSH KHÔNG bị ảnh hưởng (vẫn ping được từ laptop)
ping <pi-wifi-ip>  # từ máy tính qua WiFi → vẫn OK

# Restore 4G
sudo ip link set wwan0 up
# Chờ ~30s → monitor sẽ switch DroneBridge về 4G
```

### Test PBR có hoạt động không

```bash
# Chạy tcpdump để xem DroneBridge traffic đi interface nào
sudo tcpdump -i wwan0 udp port 14550 -nn -c 5
# Phải thấy MAVLink packets khi 4G đang active

# Nếu 4G down, phải thấy trên wlan0:
sudo tcpdump -i wlan0 udp port 14550 -nn -c 5
```

### Test SSH không bị gián đoạn khi switch

```bash
# Trên laptop, mở SSH và giữ kết nối, rồi chạy:
watch -n1 echo "SSH OK"

# Trên Pi, force switch 4G off/on và quan sát SSH
sudo ip link set wwan0 down && sleep 35 && sudo ip link set wwan0 up
# SSH phải KHÔNG bị ngắt
```

---

## Xử lý sự cố thường gặp

### SSH mất kết nối khi switch mạng

**Nguyên nhân:** PBR chưa được setup (mark chưa có) nên khi connection_manager thay đổi route, SSH traffic cũng bị ảnh hưởng.

**Kiểm tra:**
```bash
ip rule show | grep fwmark
# Phải có: 100: from all fwmark 0x1 lookup dronebridge
```

**Fix:**
```bash
sudo bash /opt/dronebridge/setup_pbr.sh
# Hoặc restart netmon service:
sudo systemctl restart dronebridge-netmon
```

---

### DroneBridge vẫn đi WiFi dù 4G đã UP

**Kiểm tra PBR table:**
```bash
ip route show table dronebridge
# Nếu trống → connection_manager chưa set
```

**Kiểm tra iptables mark:**
```bash
iptables -t mangle -L OUTPUT -n | grep MARK
# Phải có dòng mark --set-xmark 0x1
```

**Fix:**
```bash
sudo systemctl restart dronebridge-netmon
```

---

### 4G không kết nối được sau khi modem reset

**Kiểm tra 4g-init:**
```bash
journalctl -u dronebridge-4g-init --since "5 minutes ago"
sudo systemctl restart dronebridge-4g-init
```

**Kiểm tra wwan0:**
```bash
ip addr show wwan0
# Phải có inet x.x.x.x
```

---

### ip rule mất sau reboot

**Nguyên nhân:** `ip rule` và `iptables mangle` không persist qua reboot mặc định.

**Fix lâu dài — cách 1: Dùng netplan (Ubuntu/Debian):**
```yaml
# /etc/netplan/99-dronebridge-pbr.yaml
network:
  version: 2
  ethernets:
    wlan0:
      routing-policy:
        - from: 0.0.0.0/0
          mark: 1
          table: 100
```

**Fix lâu dài — cách 2: iptables-persistent:**
```bash
sudo apt install iptables-persistent
sudo netfilter-persistent save
# Sẽ tự load iptables rules khi boot
```

> Cách đơn giản nhất hiện tại: để `setup_pbr.sh` chạy qua `ExecStartPre` trong `dronebridge-netmon.service` — đây là cách đã được cấu hình sẵn.

---

## Cấu trúc file

```
Pi_CM5_DroneBridgeService/
├── Module_4G/
│   ├── enable_4g_auto.py       ← Tầng 1: 4G HW init (6 bước QMI)
│   └── connection_manager.py  ← Tầng 2: PBR routing policy (v2.0)
├── etc/
│   └── systemd/
│       ├── dronebridge-4g-init.service   ← Systemd service Tầng 1
│       ├── dronebridge-netmon.service    ← Systemd service Tầng 2
│       ├── dronebridge.service           ← Systemd service Tầng 3
│       └── setup_pbr.sh                 ← Script khởi tạo PBR
├── dronebridge                ← Binary compiled (Tầng 3)
└── docs/
    ├── NETWORK_MANAGEMENT.md  ← File này (hướng dẫn vận hành)
```

---

## Tham khảo nhanh

| Việc cần làm | Lệnh |
|---|---|
| Xem status 3 services | `systemctl status "dronebridge*"` |
| Xem log netmon live | `journalctl -u dronebridge-netmon -f` |
| Xem route DroneBridge | `ip route show table dronebridge` |
| Xem route hệ thống | `ip route show` |
| Force reinit 4G | `sudo systemctl restart dronebridge-4g-init` |
| Restart toàn bộ | `sudo systemctl restart "dronebridge*"` |
| Status nhanh | `sudo python3 /opt/dronebridge/Module_4G/connection_manager.py status` |
| Debug PBR | `ip rule show && iptables -t mangle -L OUTPUT -n` |
| Xem network_status | `cat /run/dronebridge/network_status.json` |
