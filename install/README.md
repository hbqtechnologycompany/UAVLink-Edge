# Thư mục `install`

Mọi script cài đặt và script chạy lúc boot của DroneBridge nằm ở đây. Không đặt thêm file `.sh` ở gốc repo.

Cây này được copy nguyên vào `/opt/dronebridge/install/` khi cài. Script tự tìm thư mục ứng dụng (nơi có `config.yaml`) bằng `lib/root.sh`, nên cùng một file chạy được trong repo lẫn trên máy đã cài.

Cài và cấu hình máy:

```bash
sudo bash install/install.sh
```

Lệnh này copy bộ cài, cài gói, bật service, rồi cấu hình PBR, IP cổng PX4, LCD và overlay camera. Không ghi đè `config.yaml` đã có trên máy. Overlay camera và LCD chỉ có hiệu lực sau một lần reboot.

Kiểm tra máy đang chạy và áp lại cấu hình bị lệch, không cài lại gói và không đụng uuid/secret:

```bash
sudo bash install/install.sh --check
```

Đẩy từ máy build sang Pi:

```bash
bash install/deploy.sh pi@<địa-chỉ-pi>
```

## `lib/`

| File | Việc |
| --- | --- |
| `lib/root.sh` | Hàm `dronebridge_root`. Các script khác source file này để biết thư mục gốc ứng dụng. |

## Gốc `install/`

| File | Việc |
| --- | --- |
| `install.sh` | Cài đủ bộ lên `/opt/dronebridge`: binary, Python, unit systemd, cây `install/`, sudoers camera, bật service. Không ghi đè `config.yaml` nếu máy đã có file này. |
| `deploy.sh` | Từ máy build: biên dịch ARM64, rsync sang Pi, chạy `install.sh` ở đó. |

## `camera/` — cổng CSI, overlay boot, reboot

| File | Việc |
| --- | --- |
| `setup_camera.sh` | Đọc `Find_landing/camera_detected.json` và ghi block camera trong `/boot/firmware/config.txt`. CAM0 dùng `dtoverlay=<sensor>,cam0`. CAM1 dùng `dtoverlay=<sensor>` vì overlay chính thức không có tham số `cam1`. Không đoán loại cảm biến khi chưa thấy chip. |
| `apply_camera_overlay.sh` | Chạy `setup_camera.sh`, rồi reboot nếu overlay vừa đổi. |
| `apply_host_reboot.sh` | Reboot máy khi đã có marker `.need_camera_reboot` hoặc `.need_host_reboot`. |
| `post_boot_clear.sh` | Sau khi máy đã boot lại với overlay mới, xóa marker. Gọi `setup_camera.sh --boot-settled`. |
| `watch_camera_overlay.sh` | Chạy nền (root). Khi web lưu loại camera, gọi `apply_camera_overlay.sh`. |
| `install_camera_sudoers.sh` | Ghi `/etc/sudoers.d/dronebridge-camera`. Cho phép user dịch vụ gọi đúng script camera, không qua `bash`, không cần mật khẩu. |
| `install_camera_watch.sh` | Cài unit `dronebridge-camera-watch.service` và bật watcher. |

Service gọi các script này bằng `sudo -n <đường-dẫn-script>`. Sudoers phải khớp đúng đường dẫn đó.

## `network/` — mạng và màn hình

| File | Việc |
| --- | --- |
| `setup_eth.sh` | Gán IP tĩnh cho cổng nối PX4 (`end0` trên CM5, `eth0` trên Pi 4). Không đụng 4G hay PBR. |
| `setup_pbr.sh` | Tạo bảng định tuyến 100 / 101 / 103 và iptables mark. `dronebridge-netmon.service` gọi file này trước khi giám sát mạng. |
| `setup_lcd_i2c.sh` | Bật overlay I2C3 cho LCD. Ghi marker reboot nếu boot config đổi. |
| `setup_mavlink_uart.sh` | Bật UART2 và UART3 4 chân (TX RX CTS RTS) cho TELEM2/TELEM3. `install.sh` gắn symlink và `dronebridge-uart-setup.service`, giống Ethernet và camera. |
| `install_tailscale.sh` | Cài Tailscale cho đường quản trị (SSH, dashboard). |
| `recover-network.sh` | Gỡ capability lỗi trên binary, đồng bộ code, dựng lại PBR, restart service. Dùng khi service chết với mã 203/EXEC. |

## `service/` — tiến trình DroneBridge

| File | Việc |
| --- | --- |
| `dronebridge-ctl.sh` | Hàm dừng và khởi động lại tiến trình (systemd hoặc container). `install.sh` source file này. |
| `sync-runtime.sh` | Copy code từ repo sang `/opt/dronebridge` và build binary. Giữ `camera_detected.json` và `camera_config_*.json` đang chạy. |
| `deploy-restart.sh` | Cài binary vừa build rồi restart. Không reboot cả máy. |

Unit systemd vẫn nằm ở `etc/systemd/`. Chúng trỏ vào script trong thư mục này:

- `dronebridge-netmon.service` → `install/network/setup_pbr.sh`
- `dronebridge-uart-setup.service` → `setup_mavlink_uart.sh`
- `dronebridge-camera-watch.service` → `install/camera/watch_camera_overlay.sh`

## Không nằm trong `install/`

Các file sau không phải bộ cài, nên không chuyển vào đây:

- `docker/` — script của container
- `third_party/sim8262/scripts/` — driver modem, đi cùng thư viện đó
- `test_cameras.sh`, `udp_mavlink_rate.sh`, `tcp_auth_sniff.sh`, `tcp_auth_rate.sh`, `Module_4G/test_4g_diagnose.sh` — lệnh kiểm tra thủ công
