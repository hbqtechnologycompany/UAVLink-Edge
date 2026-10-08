# UAVLink-Edge V1.2.0

Phần mềm cạnh (edge) trên Raspberry Pi CM5. Nó nối máy bay với QCloud Station: nhận MAVLink từ PX4, xác thực với router, dựng tunnel WireGuard, rồi chuyển tiếp telemetry và điều khiển. Cùng tiến trình còn phục vụ trang cấu hình, camera CSI và màn hình LCD.

Trên máy, binary, unit systemd và thư mục cài vẫn tên `dronebridge` và `/opt/dronebridge`. Tài liệu này dùng đúng các tên đó để lệnh chạy được.

Cần Go 1.24 trở lên khi build. Thư mục `vendor/` đã đủ dependency, build bằng `-mod=vendor`.

## Việc phần mềm làm

* Nhận MAVLink từ PX4 qua Ethernet (`end0` trên CM5) hoặc serial, rồi gửi lên router trong tunnel.
* Xác thực TCP với router trước khi cấp WireGuard. Auth lỗi thì tiến trình vẫn chạy và thử lại, không chặn MAVLink.
* Tách đường máy bay và đường quản trị. SSH và dashboard đi theo bảng định tuyến chính (thường là WiFi). Traffic tới router đi theo policy routing.
* Modem 4G/5G qua driver `sim7600_qmi` hoặc `sim8262_pcie`.
* Trang web cổng `8080`: trạng thái, thông số PX4, mạng, camera, hạ cánh.
* Hai camera CSI, phát qua MediaMTX, nhận diện marker hạ cánh.
* Màn hình SH1107 trên I2C.

`vpn.enabled` phải là `true`. Nếu tắt, tiến trình thoát ngay sau khi tạo forwarder.

## Thứ tự khi máy boot

```
Pi CM5 boot
    │
    ├─ dronebridge-4g-init.service          (oneshot, root)
    │     Module_Cellular/modem_init.py
    │     connection_manager.py once
    │
    ├─ dronebridge-netmon.service           (root, restart luôn)
    │     install/network/setup_pbr.sh
    │     connection_manager.py             (vòng lặp failover)
    │
    └─ dronebridge.service                  (user trên Pi)
          /opt/dronebridge/dronebridge --config /opt/dronebridge/config.yaml
```

`dronebridge.service` chờ Ethernet PX4, overlay camera, I2C LCD và netmon. Netmon vẫn chạy khi init modem thất bại.

Song song, `dronebridge-camera-watch.service` (root) theo dõi cấu hình camera. Khi web lưu loại cảm biến, nó ghi overlay vào boot config. Overlay camera và LCD chỉ có hiệu lực sau một lần reboot.

Target gộp: `drone-stack.target`.

## Tiến trình `dronebridge` sau khi start

1. Đọc `config.yaml`. Cờ `--log` ghi đè `log.level`.
2. Cờ `--register`: đăng ký một lần, ghi `.drone_secret`, rồi thoát. Unit systemd không được mang cờ này.
3. Mở listener MAVLink và trang web ngay, không chờ Pixhawk hay mạng mây.
4. Nếu `camera.enabled` và `camera.auto_start`, khởi động stream trong goroutine.
5. Tạo forwarder và bắt đầu chuyển tiếp.
6. Nếu `ethernet.allow_missing_pixhawk` là `false` và hết `pixhawk_connection_timeout` giây vẫn chưa có heartbeat, tiến trình thoát.
7. Auth chạy nền. Có đường ra mạng thì xác thực ngay. Chưa có thì vẫn thử, rồi gắn lại uplink khi `cloud_ready` bật.
8. Auth xong: xin cấp VPN (message `0xB0`). Thất bại thì dùng `vpn_config.json` đã cache. Sau đó bật WireGuard userspace và bind lại đường gửi.
9. Bridge MAVLink cho camera và phát hiện hạ cánh chạy cùng tiến trình.
10. `SIGINT` / `SIGTERM`: dừng streamer và forwarder.

## Cài trên Pi

Từ thư mục repo, trên chính CM5:

```bash
sudo bash install/install.sh
```

Script copy bộ cài vào `/opt/dronebridge`, build binary nếu máy có Go và source mới hơn binary, cài gói hệ thống, bật service, rồi cấu hình PBR, IP cổng PX4, LCD và overlay camera. `config.yaml` đã có trên máy thì không bị ghi đè.

Gói hệ thống gồm `iproute2`, `python3-yaml`, `python3-serial`, `libqmi-utils`, `python3-opencv`, `python3-picamera2`, GStreamer, `libx264-dev`, `ffmpeg`.

Kiểm tra máy đang chạy và áp lại cấu hình bị lệch. Lệnh này không cài lại gói và không đụng UUID hay secret:

```bash
sudo bash install/install.sh --check
```

Từ máy build, biên dịch ARM64 rồi cài sang Pi:

```bash
bash install/deploy.sh pi@<địa-chỉ-pi>
```

`deploy.sh` rsync binary, `config.yaml`, `install/`, `Find_landing/`, `Module_4G/` và `etc/` vào `/tmp/dronebridge_staging`, sau đó chạy installer bằng sudo.

Docker, trên Pi:

```bash
cd docker
sudo bash setup.sh
```

Container `dronebridge` dùng `network_mode: host` và mount `/opt/dronebridge`.

## Chạy tay khi đang phát triển

Dừng service trước, tránh hai tiến trình cùng giữ cổng `14550` và `8080`.

```bash
sudo systemctl stop dronebridge dronebridge-netmon dronebridge-4g-init
cd /home/cm5drone6425/Pi_CM5_DroneBridgeService
make run-debug
```

`make run-debug` build rồi chạy `./dronebridge --config config.yaml --log debug`. Không khởi tạo modem và không dựng PBR.

Đăng ký lần đầu:

```bash
sudo systemctl stop dronebridge
make run-register
```

Tương đương `./dronebridge --config config.yaml --register`. Secret nằm ở `.drone_secret` cạnh binary. Không commit file này và không commit `vpn_config.json`.

Nhiều máy bay dùng chung thư mục đồng bộ:

```bash
make run-sync DRONE_ID=drone-01
```

Cờ dòng lệnh:

| Cờ | Ý nghĩa |
| --- | --- |
| `--config` | File cấu hình. Mặc định `config.yaml`. |
| `--log` | `debug`, `info`, `warn`, `error`. |
| `--register` | Đăng ký rồi thoát. |
| `--sync-dir` | Bật chế độ đồng bộ nhiều máy bay. |
| `--drone-id` | Bắt buộc khi có `--sync-dir`. |

`make run` và `make sync-opt` trong Makefile vẫn trỏ `etc/systemd/setup_pbr.sh` và `scripts/sync-runtime.sh`. Hai đường đó đã chuyển vào `install/`. Dùng `install/install.sh` và `install/service/sync-runtime.sh`.

## Cấu hình

Sửa `/opt/dronebridge/config.yaml` trên máy đã cài, hoặc file cùng tên trong repo khi chạy tay. Giá trị dưới đây là chỗ cần điền, không phải secret có sẵn.

```yaml
log:
  level: info                  # debug | info | warn | error
  server_connection_only: false
  show_web_interaction_logs: true
  show_packet_stats: false

auth:
  enabled: true
  host: <router-host>
  port: 5770
  uuid: <uuid-drone>
  shared_secret: <secret-đăng-ký>
  vehicle_type: 0              # MAV_TYPE khi REGISTER_INIT
  model: ""
  keepalive_interval: 30
  reconnect_interval: 300
  startup_retry_interval: 15

cellular:
  enabled: true
  driver: sim8262_pcie         # sim8262_pcie | sim7600_qmi
  iface: wwan0
  apn: ""

network:
  local_listen_port: 14550
  target_host: 10.8.0.1
  target_port: 14550
  protocol: udp
  connection_type: prefer_ethernet
  serial_port: /dev/ttyAMA2
  serial_baud: 57600
  mode: prefer_4g              # prefer_4g | 4g_only | wifi_only
  cloud_wifi_fallback: false   # false = không đưa wlan0 vào PBR tới router
  forward_gps_raw_int: false
  fallback_delay: 300

ethernet:
  interface: end0
  local_ip: 10.41.10.10
  pixhawk_ip: 10.41.10.2
  pixhawk_port: 14550
  allow_missing_pixhawk: true
  pixhawk_connection_timeout: 30

web:
  port: 8080

vpn:
  enabled: true
  config_file: vpn_config.json
  server_endpoint: <host>:<cổng-wireguard>
  router_vpn_ip: 10.8.0.1
```

`camera`, `landing` và `lcd` cũng nằm trong cùng file. Camera khai báo từng stream `cam0`, `cam1` (kích thước, fps, bitrate, ArUco, overlay). LCD mặc định bus I2C `3`, địa chỉ `0x3c` (`address: 60`).

## Trang web

Mở `http://<ip-pi>:8080/`. `/` chuyển tới `/dashboard.html`.

| Trang | Việc |
| --- | --- |
| `dashboard.html` | Trạng thái link, mạng, camera |
| `settings.html` | Cấu hình |
| `connect.html` | Kết nối |
| `mavlink.html`, `mavlink_settings.html` | Luồng MAVLink |
| `params.html` | Đọc và ghi tham số PX4 |
| `tokens.html` | API key của drone |

Nhóm API chính trên cùng cổng:

| Nhóm | Đường dẫn |
| --- | --- |
| Trạng thái | `/api/health`, `/api/status`, `/api/connection` |
| Tham số PX4 | `/api/param/request-list`, `/api/param/list`, `/api/param/get`, `/api/param/set` |
| API key | `/api/v1/drone/api-key/status`, `request`, `revoke`, `delete` |
| Camera | `/api/camera/detect`, `start`, `stop`, `restart`, `status`, `preview`, `config/load`, `config/save` |
| Hạ cánh | `/api/landing/config/load`, `config/save`, `/api/landing/templates` |
| Mạng | `/api/network/status`, `mode`, `switch`, `reconnect`, `test` |
| Cấu hình | `/api/config/get`, `/api/config/network/update` |

## Mạng

Ba bảng policy:

| Bảng | Việc |
| --- | --- |
| 100 | Đường mây của ứng dụng (4G, hoặc WiFi nếu được phép fallback) |
| 101 | Đường phụ của policy |
| 103 | Mặt phẳng Ethernet tới PX4 |

`install/network/setup_pbr.sh` tạo rule và iptables mark. `dronebridge-netmon` gọi script này trước vòng giám sát. Trạng thái netmon ghi ở `/run/dronebridge/network_status.json`. Package Go `network` chỉ đọc file đó (`CloudReady`, `PhysicalEgress`). Không đoán interface trong ứng dụng.

`network.mode`:

* `prefer_4g` — ưu tiên modem, fallback theo `fallback_delay` nếu `cloud_wifi_fallback` bật.
* `4g_only` — không chuyển mây sang WiFi.
* `wifi_only` — mây đi WiFi.

`cloud_wifi_fallback: false` giữ traffic tới router khỏi `wlan0`. SSH vẫn ở bảng chính.

Init modem là `Module_Cellular/modem_init.py`. Nó đọc `cellular.driver` rồi gọi SIM7600 (QMI, `wwan0`) hoặc SIM8262 (PCIe, `rmnet0` trên `mhi_hwip0`). `Module_4G/enable_4g_auto.py` vẫn là phần thực thi của SIM7600.

Ethernet PX4 do `install/network/setup_eth.sh`: gán IP tĩnh cho `end0` (CM5) hoặc `eth0` (Pi 4). Không đụng 4G hay PBR.

## Xác thực và VPN

Kênh TCP `auth.host:auth.port` (mặc định cổng `5770`).

1. `0x01`–`0x04`: UUID, challenge, HMAC, kết quả.
2. `0x10`: xin session.
3. `0xA0`: đăng ký lần đầu bằng `shared_secret`. Secret phiên sau nằm trong `.drone_secret`.
4. `0xB0`: xin cấu hình WireGuard sau khi đã có session.
5. `0x20`: xin, thu hồi hoặc xóa API key.

Tunnel là WireGuard userspace. Peer và IP được cấp phát ghi vào `vpn_config.json`.

## Camera

`Find_landing/` phát hiện marker và xuất hình. Go (`camera/`, `web/camera_runtime.go`) khởi động và giám sát tiến trình Python.

`install/camera/setup_camera.sh` đọc `Find_landing/camera_detected.json` và ghi block camera trong `/boot/firmware/config.txt`. CAM0 dùng `dtoverlay=<sensor>,cam0`. CAM1 dùng `dtoverlay=<sensor>`. Script không đoán loại cảm biến khi chưa thấy chip.

MediaMTX: RTSP `8554`, WebRTC `8889`, HLS `8888`. Host trống thì dùng `auth.host`.

## Vận hành

```bash
sudo systemctl status "dronebridge*"
sudo systemctl restart dronebridge-4g-init dronebridge-netmon dronebridge
journalctl -u dronebridge -f
journalctl -u dronebridge-netmon -f
journalctl -u dronebridge-4g-init -f
sudo python3 /opt/dronebridge/Module_4G/connection_manager.py status
```

| Việc | Lệnh |
| --- | --- |
| Log ứng dụng | `journalctl -u dronebridge -f` |
| Log failover | `journalctl -u dronebridge-netmon -f` |
| Log modem | `journalctl -u dronebridge-4g-init -f` |
| Trạng thái định tuyến | `connection_manager.py status` |
| Mất SSH sau khi bật app | `sudo bash /opt/dronebridge/install/network/setup_pbr.sh` rồi restart netmon |
| Service chết mã 203/EXEC | `sudo bash /opt/dronebridge/install/network/recover-network.sh` |
| Auth lỗi lúc boot | Tiến trình vẫn lên. Xem log `[AUTH]`. Kiểm tra `auth.host`, cổng `5770`, và `cloud_ready` trong `/run/dronebridge/network_status.json` |
| VPN không lên | Xóa `vpn_config.json` rồi restart để xin cấp lại sau auth |
| Không thấy Pixhawk | `allow_missing_pixhawk: true` để chạy tiếp khi thử. Khi bay thật, để `false` |
| Camera không ra hình sau khi đổi loại | Reboot một lần sau khi overlay được ghi |

## Cây thư mục

| Đường dẫn | Việc |
| --- | --- |
| `main.go` | Startup, auth nền, VPN, tín hiệu dừng |
| `config.yaml` | Cấu hình chạy |
| `auth/` | Giao thức TCP với router |
| `forwarder/` | Nhận MAVLink và gửi lên tunnel |
| `network/` | Đọc trạng thái netmon, dial có mark PBR |
| `vpn/` | WireGuard userspace |
| `web/` | HTTP `8080` và trang tĩnh |
| `camera/` | Khởi động stream và bridge MAVLink camera |
| `Find_landing/` | Stream và nhận diện hạ cánh (Python) |
| `Module_Cellular/` | Chọn driver modem và connect |
| `Module_4G/` | Netmon, SIM7600, nhà mạng |
| `install/install.sh` | Cài vào `/opt/dronebridge` |
| `install/deploy.sh` | Build ARM64 và cài từ xa |
| `install/network/` | PBR, Ethernet PX4, LCD, Tailscale |
| `install/camera/` | Overlay CSI, sudoers, watcher |
| `install/service/` | Dừng/chạy tiến trình, sync, restart |
| `etc/systemd/` | Unit. Netmon và camera-watch trỏ vào `install/` |
| `docker/` | Image và compose, network host |
| `third_party/sim8262/` | Driver và script modem PCIe |
| `docs/` | Kiến trúc mạng, camera, auth, startup |

Chi tiết từng script cài nằm ở `install/README.md`.
