# Chính sách 3 Interface — PX4 eth / wlan0 / wwan0

> **Cập nhật:** 2026-07-03  
> **Nguyên tắc:** 5 khối độc lập — chỉ giao tiếp qua `config.yaml` và file runtime JSON.  
> **Ownership chi tiết + lộ trình Go:** [NETWORK_OWNERSHIP.md](./NETWORK_OWNERSHIP.md)

---

## 0. Năm khối — không chồng trách nhiệm

```
┌─────────────┐   config.yaml    ┌─────────────┐
│  Khối A     │◄────────────────►│  Khối B     │
│  setup_eth  │  ethernet.*    │  4g-init    │
│  (end0 IP)  │                │  (wwan0 IP) │
└──────┬──────┘                └──────┬──────┘
       │                              │ modem_net.json (debug)
       │ netdev có IP                 │
       ▼                              ▼
┌─────────────┐   setup_pbr.sh   ┌─────────────┐
│  Khối C     │◄─── iptables ───►│  Khối D     │
│  netmon     │  network_status  │  dronebridge│
│  PBR/probe  │─────────────────►│  Go app     │
└─────────────┘      .json       └─────────────┘
       ▲
       │ table 100/101/103, ip rule (một lần + netmon duy trì)
┌─────────────┐
│  Khối E     │
│  setup_pbr  │
└─────────────┘
```

| Khối | File / unit | Một việc duy nhất | Không được làm |
|------|-------------|-------------------|----------------|
| **A** | `setup_eth.sh` + `dronebridge-eth-setup` | Gán IP tĩnh PX4 (`end0` CM5 / `eth0` Pi4) | 4G, PBR, probe |
| **B** | `enable_4g_auto.py` + `dronebridge-4g-init` | Modem QMI → `wwan0` + IP | PBR, eth IP, JSON app |
| **C** | `connection_manager.py` + `dronebridge-netmon` | Route 3 plane + probe + `network_status.json` | Gán IP eth/wwan, rename modem |
| **D** | `dronebridge` (Go) + Web + LCD | Đọc JSON, MAVLink/VPN/auth | `qmicli`, `ip route`, đổi PBR |
| **E** | `setup_pbr.sh` | iptables MARK + ip rule fwmark (bootstrap) | Chọn wwan vs wlan |

**Điểm chạm duy nhất giữa các khối:**

| Artifact | Writer | Reader |
|----------|--------|--------|
| `config.yaml` | User / UI | A, B, C |
| `/run/dronebridge/modem_net.json` | B (4g-init) | Human debug |
| `/run/dronebridge/network_status.json` | C (netmon) | D (Go/Web/LCD) |

Sửa khối B **không** được sửa logic eth trong C. Sửa khối A **không** được đụng wwan0.

---

## 1. Ba mặt phẳng (không trộn lẫn)

```
┌─────────────────────────────────────────────────────────────────┐
│ end0/eth0 — PIXHAWK PLANE (LAN 10.41.10.0/24)                   │
│   MAVLink HEARTBEAT 1Hz CM5 → PX4 (:14550)                      │
│   MAVLink nhận từ PX4                                           │
│   KHÔNG fwmark PBR | table 103 eth_pixhawk                      │
├─────────────────────────────────────────────────────────────────┤
│ wlan0 — ADMIN PLANE                                             │
│   SSH, apt, dashboard :8080 reply                               │
│   main table default | table 101 wlan_reply                     │
│   KHÔNG phải cloud uplink (trừ khi PBR fallback wifi_only)      │
├─────────────────────────────────────────────────────────────────┤
│ wwan0 | wlan0 — CLOUD DATA PLANE                                │
│   Auth TCP, WireGuard UDP, MAVLink ↔ server                     │
│   table 100 dronebridge + fwmark 0x1 (uid dronebridge)          │
│   netmon chọn wwan0 hoặc wlan0 theo network.mode                │
└─────────────────────────────────────────────────────────────────┘
```

| Interface | Vai trò | Ai quản lý | Routing table |
|-----------|---------|------------|---------------|
| **end0** (CM5) / **eth0** (Pi4) | MAVLink PX4 local | Khối A gán IP; Khối C `ensure_eth_pixhawk_plane` | main + 103 `eth_pixhawk` |
| **wlan0** | Admin + SSH | netmon `ensure_system_wifi_route` + `ensure_wlan_admin_reply` | main + 101 `wlan_reply` |
| **wwan0** | Cloud uplink 4G | netmon `set_pbr_route(wwan0)` | 100 `dronebridge` |

---

## 2. iptables — exempt trước khi MARK

Thứ tự trong `setup_pbr.sh` (quan trọng):

1. `RETURN` — `tcp sport 8080` (dashboard reply)
2. `RETURN` — `-d 10.41.10.0/24` (MAVLink PX4 LAN)
3. `RETURN` — `-d 127.0.0.0/8`
4. `MARK 0x1` — mọi traffic còn lại của user `dronebridge`

**Trước đây:** chỉ exempt `:8080` → gói tới `10.41.10.2` bị mark → route table 100 default `wwan0` → **MAVLink không ra eth0**.

**Hiện tại:** destination PX4 LAN không bị mark → kernel dùng main table → `dev eth0`.

---

## 3. Table 100 — route PX4 LAN (lớp an toàn)

Mỗi lần `set_pbr_route()`, netmon thêm **trước** default:

```
10.41.10.0/24 dev eth0   table 100
default dev wwan0        table 100   # hoặc via wlan gw
```

Nếu packet vẫn bị mark (edge case), destination cụ thể hơn default → vẫn đi `eth0`.

---

## 4. ip rule bổ sung

| Priority | Rule | Table | Mục đích |
|----------|------|-------|----------|
| 45 | `from <eth_ip>/32 lookup eth_pixhawk` | 103 | Reply MAVLink source eth |
| 50 | `from <wlan_ip>/32 lookup wlan_reply` | 101 | Reply SSH/HTTP source wlan |
| 100 | `fwmark 0x1 lookup dronebridge` | 100 | Cloud uplink |

---

## 5. Chu kỳ netmon (mỗi 30s)

```
1. ensure_admin_plane()
     ├─ ensure_system_wifi_route()     # SSH main table
     ├─ ensure_wlan_admin_reply()        # table 101
     └─ ensure_eth_pixhawk_plane()      # table 103
2. verify_pbr_rule()
3. probe wwan0 / wlan0
4. set_pbr_route(wwan0|wlan0) + ensure_pbr_eth_lan_route()
5. maybe_reinit_4g()
6. _save_status() → network_status.json (trường "planes")
```

---

## 6. `network_status.json` — contract (single source of truth)

**Writer:** `connection_manager.py` (`_save_status`)  
**Readers:** Go `network/egress.go`, Web `ReadNetworkStatus()`, LCD `collector.py`  
**Không** đọc qmicli/iw trực tiếp ở consumer — netmon đo sóng và probe.

```json
{
  "active_interface": "wwan0",
  "network_mode": "prefer_4g",
  "route_reason": "4G_PROBE_OK",
  "wwan_probe_mode": "PING_OK",
  "cloud_ready": true,
  "cloud_signal_dbm": -85,
  "cloud_signal_source": "4g",
  "4g": { "ip": "10.x.x.x", "online": true, "signal_dbm": -85 },
  "wifi": { "ip": "192.168.x.x", "online": true, "signal_dbm": -55 },
  "planes": {
    "eth_pixhawk": { "interface": "eth0", "ip": "10.41.10.10", "link_up": true },
    "wlan_admin": { "ip": "192.168.x.x", "reply_rule": "ok" },
    "cloud_uplink": {
      "interface": "wwan0",
      "table": 100,
      "ip": "10.x.x.x",
      "online": true,
      "probe": "PING_OK",
      "ready": true,
      "signal_dbm": -85,
      "signal_source": "4g"
    }
  }
}
```

| Field | Ý nghĩa |
|-------|---------|
| `cloud_ready` | PBR table 100 default = `active_interface` **và** có IP **và** probe OK |
| `cloud_signal_*` | Sóng của **cloud uplink** (4G hoặc WiFi), không phải admin WiFi khi đang dùng 4G |
| `planes.cloud_uplink` | Chi tiết đầy đủ; Go auth chờ `cloud_ready` trước TCP |

Boot: `dronebridge-netmon` chạy `connection_manager.py once` trong `ExecStartPre` → file tồn tại trước khi `dronebridge` auth.

---

## 10. Modem layer — ai sở hữu gì (không vá ở netmon)

Chuẩn QMI / qmi_wwan (SIM7600 trên Pi CM5):

| Tầng | Unit / file | Trách nhiệm |
|------|-------------|-------------|
| **udev** | `etc/udev/rules.d/76-dronebridge-sim7600-wwan.rules` | Đặt tên `wwan0` khi kernel add netdev (`wwu*` → `wwan0`) |
| **4g-init** | `enable_4g_auto.py` (toàn bộ trong 1 file) | GPIO, bind driver, QMI WDS, IP `wwan0`, `modem_net.json` |
| **netmon** | `connection_manager.py` | Probe `wwan0`, PBR table 100, reinit — **không** rename modem |
| **app** | Go / Web / LCD | Đọc `network_status.json` only |

**Contract 4g-init:** exit 0 chỉ khi `wwan0` có IPv4. Thất bại → exit 1, netmon `trigger_4g_reinit()`.

**systemd oneshot:** `dronebridge-4g-init` + `RemainAfterExit=yes` → sau khi xong vẫn `ActiveState=active`, `SubState=exited`.  
Netmon coi service **busy** chỉ khi `ActiveState=activating` hoặc `SubState=start` — không dùng `is-active` (sai → không bao giờ reinit).

**Kernel mới:** `wwu*` → udev rule đặt `wwan0`; nếu chưa kịp, **chỉ** `enable_4g_auto.py` rename.

```bash
# Cài udev (một lần trên Pi)
sudo cp etc/udev/rules.d/76-dronebridge-sim7600-wwan.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger -c add -s net
```

---

## 7. Kiểm tra sau deploy

```bash
# Áp dụng PBR mới + một chu kỳ netmon
sudo /opt/dronebridge/setup_pbr.sh
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py once

# MAVLink PX4 phải đi eth0 (không mark)
ip route get 10.41.10.2
ip route get 10.41.10.2 mark 0x1    # vẫn dev eth0 nhờ /24 trong table 100

# Cloud vẫn đi 4G khi active
ip route get 45.117.171.237 mark 0x1

# Bắt gói thực (cần root)
sudo tcpdump -i eth0 -n 'udp port 14550 and host 10.41.10.2' -c 3
```

---

## 8. Cấu hình liên quan (`config.yaml`)

```yaml
ethernet:
  interface: eth0
  local_ip: "10.41.10.10"
  pixhawk_ip: "10.41.10.2"
  subnet: "24"

network:
  mode: prefer_4g    # chỉ ảnh hưởng cloud uplink — không đụng eth0
```

Override subnet (nếu đổi dải PX4):

```bash
export DRONEBRIDGE_ETH_LAN_CIDR=10.41.10.0/24
```

---

## 9. Tài liệu liên quan

- [NETWORK_ARCHITECTURE.md](./NETWORK_ARCHITECTURE.md)
- [NETWORK_FLOWCHART_COMPARISON.md](./NETWORK_FLOWCHART_COMPARISON.md)
- [SYSTEM_ARCHITECTURE.md](./SYSTEM_ARCHITECTURE.md)
