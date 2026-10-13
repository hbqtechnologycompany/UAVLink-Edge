# Quản lý mạng — ai sở hữu gì

> **Vấn đề hiện tại:** thư mục `Module_4G/` chứa modem + WiFi + ETH + PBR → tên sai, không có owner trung tâm, Web/Go gọi Python trực tiếp lung tung.

> **Mục tiêu:** **Go package `network/`** là não điều phối duy nhất cho app. Python/shell chỉ là **công nhân root** theo từng khối, không quyết sách.

---

## 1. Mô hình 3 tầng

```
┌─────────────────────────────────────────────────────────────┐
│  TẦNG 1 — GO (DroneBridge/network)                          │
│  Đọc trạng thái, quyết định app (auth/VPN/forwarder)        │
│  API cho Web UI — KHÔNG exec Python từ handler              │
│  File: manager.go, egress.go, pbr_dial.go, status.go        │
└───────────────────────────┬─────────────────────────────────┘
                            │ đọc network_status.json
                            │ (sau này: ghi desired_mode nếu cần)
┌───────────────────────────▼─────────────────────────────────┐
│  TẦNG 2 — NETMON DAEMON (root, systemd)                     │
│  Một process: probe + PBR table 100/101/103 + ghi JSON       │
│  Hiện tại: Module_4G/connection_manager.py  → đổi tên netmon│
└───────────────────────────┬─────────────────────────────────┘
                            │ gọi systemd restart
┌───────────────────────────▼─────────────────────────────────┐
│  TẦNG 3 — INIT ONESHOT (root, từng khối tách file)           │
│  A: setup_eth.sh           — IP end0/eth0 (PX4)              │
│  B: enable_4g_auto.py      — modem QMI → wwan0               │
│  E: setup_pbr.sh           — iptables MARK + ip rule         │
└─────────────────────────────────────────────────────────────┘
```

**Go không thay thế netmon ngay** (cần root + iptables + loop 30s). Go **sở hữu contract** và **mọi logic app**; netmon chỉ thi hành routing kernel.

---

## 2. Năm khối chức năng (tách file, không gộp Module_4G)

| Khối | Tên đúng | File hiện tại | File mục tiêu |
|------|----------|---------------|---------------|
| **A — PX4 ETH** | `eth-setup` | `setup_eth.sh` | `scripts/network/setup_eth.sh` |
| **B — Modem 4G** | `modem-init` | `Module_4G/enable_4g_auto.py` | `scripts/network/modem_init.py` |
| **C — Netmon** | `netmon` | `Module_4G/connection_manager.py` | `scripts/network/netmon.py` |
| **D — App** | `network` (Go) | `network/*.go` | giữ, mở rộng `manager.go` |
| **E — PBR bootstrap** | `pbr-setup` | `etc/systemd/setup_pbr.sh` | `scripts/network/setup_pbr.sh` |

`Module_4G/` → **deprecated**, chỉ giữ symlink/wrapper 1 release để systemd cũ không gãy.

**Không thuộc mạng:** `vn_carriers.py`, `set_4g_mode.py`, `4G_control_at_testor.py` → `scripts/modem/` (tool phụ).

---

## 3. Go `network` package — API duy nhất cho app

| Hàm / type | Mục đích |
|------------|----------|
| `ReadStatus()` | Parse `network_status.json` → struct typed |
| `CloudReady()` | Cloud uplink sẵn sàng (auth/VPN) |
| `PhysicalEgress()` | iface + IP cloud |
| `EthPixhawk()` | Trạng thái plane PX4 (từ `planes.eth_pixhawk`) |
| `WaitForCloudEgress()` | Boot: chờ netmon |
| `DialTCP()` | Auth qua PBR mark |

**Web (`server.go`)** phải gọi `network.ReadStatus()` — **cấm** `exec python3 ... connection_manager.py` trong handler (đang nợ refactor).

**Đổi `network.mode`:** ghi `config.yaml` + `systemctl restart dronebridge-netmon` (hoặc SIGHUP sau này) — không CLI `priority` ẩn trong Python.

---

## 4. Vì sao `Module_4G` không ổn

| Trong `connection_manager.py` | Thuộc khối |
|------------------------------|------------|
| `ensure_eth_pixhawk_plane` | A + C (chỉ **route** table 103; IP do A) |
| `ensure_wlan_admin_reply` | C (admin WiFi) |
| `set_pbr_route(wwan0\|wlan0)` | C (cloud) |
| `trigger_4g_reinit` | C → gọi systemd khối B |
| `get_4g_signal_metrics` | C (đo sóng cho JSON) |

→ File này là **netmon đa plane**, không phải "module 4G". Đặt trong `Module_4G/` gây hiểu nhầm và sửa 4G làm vỡ WiFi/ETH.

---

## 5. Lộ trình (không rewrite một lúc)

### Phase 1 — Ngay (đã / đang làm)
- [x] `docs/INTERFACE_POLICY.md` §0 — 5 khối
- [x] `network/manager.go` + `status.go` — Go typed status
- [ ] Web dùng `network.ReadStatus()` thay exec Python (từng endpoint)

### Phase 2 — Đổi tên thư mục
- Copy `connection_manager.py` → `scripts/network/netmon.py`
- Copy `enable_4g_auto.py` → `scripts/network/modem_init.py`
- `Module_4G/*.py` → wrapper 3 dòng import/run
- Cập nhật systemd paths

### Phase 3 — Go điều phối
- `config.yaml` `network.mode` hot-reload qua netmon (inotify hoặc restart có kiểm soát)
- Xóa CLI `priority` / `set_4g_mode.py` khỏi Web API

---

## 6. Kiểm tra sau refactor

```bash
# Go đọc status (không Python)
go run ./cmd/netstatus  # hoặc curl /api/network/status

# Khối tách systemd
systemctl status dronebridge-eth-setup   # A
systemctl status dronebridge-4g-init     # B
systemctl status dronebridge-netmon      # C
```

---

## 7. Quy tắc commit sau này

1. Sửa **modem/QMI** → chỉ `modem_init.py` + `4g-init.service`
2. Sửa **PBR/probe/JSON** → chỉ `netmon.py` + `netmon.service`
3. Sửa **app/UI** → chỉ `network/*.go` + `web/`
4. **Không** thêm file `.py` mới trong `Module_4G/`
