# Kiến trúc Mạng — DroneBridge CM5 (WiFi + 4G)

> **Phiên bản:** 1.0 (đang triển khai)  
> **Tham chiếu gốc:** [UAVLink-Edge](https://github.com/hbqtechnologycompany/UAVLink-Edge.git)  
> **Cập nhật:** 2026-07-02

Tài liệu này mô tả **kiến trúc mạng thực tế đang hướng tới** trên Pi CM5 — tách rõ trách nhiệm từng tầng, tránh nhầm lẫn giữa admin (SSH/dashboard) và data plane (MAVLink, auth, VPN).

Phác thảo mở rộng (Tailscale admin plane): xem `docs/NETWORK_ARCHITECTURE_SPEC.md`.

So sánh flowchart ý tưởng (WiFi/4G → ưu tiên → đăng ký → kiểm tra → đẩy data) với code thực tế: xem **[NETWORK_FLOWCHART_COMPARISON.md](./NETWORK_FLOWCHART_COMPARISON.md)**.

Chính sách 3 interface (`eth0` / `wlan0` / `wwan0`): xem **[INTERFACE_POLICY.md](./INTERFACE_POLICY.md)**.

---

## 1. Nguyên tắc thiết kế (từ UAVLink-Edge)

| Nguyên tắc | Ý nghĩa |
|------------|---------|
| **SSH/admin không đi theo 4G** | `main` table luôn default qua `wlan0` — SSH, apt, curl hệ thống ổn định |
| **App DroneBridge tách route** | Chỉ traffic process `dronebridge` (uid-owner) → `fwmark 0x1` → **table 100** |
| **4G init ≠ routing policy** | Modem/QMI (`enable_4g_auto.py`) và PBR (`connection_manager.py`) là 2 service riêng |
| **Auth TCP ≠ VPN UDP** | Auth TCP dial public IP trực tiếp; WireGuard UDP đi theo PBR — tránh kéo auth theo tunnel flap |
| **Failover có kiểm soát** | 4G mất → reinit modem (systemd); chỉ fallback WiFi khi **không** bật `4g_only` |

---

## 2. Ba tầng systemd

```
multi-user.target
│
├── dronebridge-4g-init.service     [Tầng 1 — Hardware]
│     enable_4g_auto.py             GPIO → QMI → wwan0 IP
│     ExecStartPost: connection_manager.py once
│
├── dronebridge-netmon.service      [Tầng 2 — Policy]
│     ExecStartPre: setup_pbr.sh      ip rule + iptables mark
│     connection_manager.py           loop 30s: probe + PBR table 100
│
└── dronebridge.service             [Tầng 3 — Application]
      dronebridge binary              auth → VPN → MAVLink forward
```

### Tầng 1 — `enable_4g_auto.py`

- Power GPIO SIM7600, QMI WDS start, cấp IP `wwan0`
- APN theo SIM: `Module_4G/vn_carriers.py` (MobiFone `m-wap` + PAP, Viettel `v-internet`, …)
- **Không** sửa default route hệ thống
- Log: `journalctl -u dronebridge-4g-init -f`

### Tầng 2 — `connection_manager.py` + `setup_pbr.sh`

- `setup_pbr.sh`: tạo `ip rule fwmark 0x1 lookup dronebridge` + `iptables -t mangle OUTPUT -m owner --uid-owner <user> -j MARK --set-mark 0x1`
- `connection_manager.py`: mỗi 30s:
  1. `ensure_system_wifi_route()` — giữ SSH qua WiFi
  2. Probe 4G: ICMP ping `8.8.8.8` qua `wwan0`, fallback TCP tới auth server
  3. Cập nhật **chỉ table 100** (`default dev wwan0` hoặc `via <wifi_gw> dev wlan0`)
  4. Trigger `dronebridge-4g-init` reinit nếu 4G fail liên tục
- Ghi trạng thái: `/run/dronebridge/network_status.json`
- Log chẩn đoán:
  - `logs/4g_link_events.log` — trạng thái link định kỳ
  - `logs/route_decisions.log` — lý do chọn interface
  - `logs/egress_path.log` — UDP counter qua wwan0

### Tầng 3 — `dronebridge` binary

- **Thứ tự QCC:** Auth TCP `:5770` → VPN provision `0xB0` (cùng TCP) → WG userspace UP → MAVLink `10.8.0.1:14550`
- Auth TCP dial **public** — không qua tunnel
- WireGuard UDP — socket đi theo PBR (uid mark)
- Web `:8080` bind `0.0.0.0` — admin local only

---

## 3. Hai mặt phẳng routing

```
┌─────────────────────────────────────────────────────────────┐
│ ADMIN / SYSTEM (main table)                                  │
│   sshd, apt, tailscaled, python netmon, enable_4g_auto      │
│   default → wlan0 (metric thấp)                              │
│   KHÔNG default qua wwan0                                    │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ DATA PLANE (table 100 — dronebridge)                         │
│   dronebridge process (uid mark 0x1)                         │
│   default → wwan0 khi 4G probe OK                            │
│   default → wlan0 khi fallback (mode ≠ 4g_only)            │
└─────────────────────────────────────────────────────────────┘
```

### `ip rule` tối thiểu

| Priority | Rule | Table |
|----------|------|-------|
| 100 | `fwmark 0x1 lookup dronebridge` | 100 |
| 32766 | `from all lookup main` | main |

### Table 100 nội dung điển hình

```bash
# 4G active
default dev wwan0

# WiFi fallback
default via 192.168.x.1 dev wlan0
```

---

## 4. Chế độ mạng (`config.yaml`)

```yaml
network:
  mode: prefer_4g   # prefer_4g | 4g_only | wifi_only
  fallback_delay: 300
```

| `mode` | Hành vi PBR table 100 |
|--------|------------------------|
| `prefer_4g` | 4G probe OK → wwan0; sau `fallback_delay` có thể → wlan0 |
| `4g_only` | Luôn target wwan0; **không** fallback WiFi cho app |
| `wifi_only` | Luôn wlan0 |

**Lưu ý:** `main` table **không đổi** theo mode — SSH vẫn WiFi.

---

## 5. Luồng Auth trên 4G

```
dronebridge (uid mark)
  → ip rule fwmark 0x1 → table 100 → wwan0
  → TCP connect 45.117.171.237:5770
  → AUTH_INIT → AUTH_CHALLENGE → AUTH_RESPONSE → AUTH_ACK
```

### Kẹt `AUTH_ACK timeout`

| Đã xác nhận | Ý nghĩa |
|-------------|---------|
| TCP connect OK | Routing tới server OK |
| Nhận AUTH_CHALLENGE | Downlink 4G OK ít nhất 1 lần |
| Gửi AUTH_RESPONSE | Uplink OK |
| AUTH_ACK timeout | Server không trả ACK, hoặc mất gói downlink, hoặc secret/UUID sai (server im lặng) |

Log chẩn đoán (sau bản build mới):

```bash
journalctl -u dronebridge -f | grep AUTH
```

Gợi ý env timeout:

```ini
Environment=DRONEBRIDGE_AUTH_CHALLENGE_TIMEOUT_S=45
Environment=DRONEBRIDGE_AUTH_ACK_TIMEOUT_S=45
```

Đăng ký lại nếu secret mất:

```bash
sudo systemctl stop dronebridge
cd /opt/dronebridge
sudo ./dronebridge --config config.yaml --register
sudo systemctl start dronebridge
```

---

## 6. So sánh với UAVLink-Edge gốc

| Thành phần | UAVLink-Edge | DroneBridge (hiện tại) |
|------------|--------------|------------------------|
| 3 tầng systemd | ✅ 4g-init / netmon / app | ✅ Tương đương |
| PBR table 100 + fwmark | ✅ | ✅ `setup_pbr.sh` |
| SSH qua WiFi | ✅ | ✅ `ensure_system_wifi_route()` |
| `connection_manager.py` | ✅ | ✅ (phải có trên `/opt`) |
| APN auto theo carrier | Cơ bản | ✅ `vn_carriers.py` |
| Tailscale admin plane | Chưa | Phác thảo trong SPEC |
| `connection_config.json` priority | Không | Đang thử nghiệm (chưa merge ổn định) |

**Không nên:** đưa `default dev wwan0` vào **main** table — phá SSH và gây “4G lúc có lúc không” do tranh route.

---

## 7. Lệnh vận hành & chẩn đoán

```bash
# Trạng thái nhanh
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py status

# Chẩn đoán đầy đủ (QMI reg, probe, PBR, routes)
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py diag

# Áp dụng routing một lần (sau 4g-init)
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py once

# Log realtime
journalctl -u "dronebridge*" -f

# Kiểm tra PBR
ip rule show | grep fwmark
ip route show table dronebridge
iptables -t mangle -L OUTPUT -n -v | grep MARK
```

---

## 8. Sự cố thường gặp

| Triệu chứng | Nguyên nhân | Cách xử lý |
|-------------|-------------|------------|
| Netmon crash `can't open connection_manager.py` | File thiếu trên `/opt` | Deploy lại `Module_4G/connection_manager.py` |
| 4G có IP nhưng auth fail | PBR table 100 trống / sai | `connection_manager.py once` + kiểm tra table 100 |
| AUTH_ACK timeout | Server / secret / carrier downlink | Log AUTH mới + server log; thử `--register` |
| SSH mất khi bật app | PBR chưa setup / main bị wwan0 | `setup_pbr.sh`; xóa default wwan0 khỏi main |
| 4G flap liên tục | Sóng yếu + reinit backoff | `diag` xem RSRP; kiểm tra `4g_link_events.log` |

---

## 9. File tham chiếu

| File | Vai trò |
|------|---------|
| `Module_4G/enable_4g_auto.py` | Init modem QMI |
| `Module_4G/vn_carriers.py` | APN/PAP theo nhà mạng VN |
| `Module_4G/connection_manager.py` | PBR monitor + failover |
| `etc/systemd/setup_pbr.sh` | ip rule + iptables mark |
| `auth/client.go` | Auth TCP client + log AUTH_ACK |
| `docs/NETWORK_MANAGEMENT.md` | Hướng dẫn vận hành |
| `docs/NETWORK_ARCHITECTURE_SPEC.md` | Phác thảo Tailscale admin (phase 2) |
