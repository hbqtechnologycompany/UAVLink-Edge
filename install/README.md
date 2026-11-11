# Thư mục `install`

Mọi script cài đặt và script chạy lúc boot của DroneBridge nằm ở đây. Không đặt thêm file `.sh` ở gốc repo.

Cây này được copy nguyên vào `/opt/dronebridge/install/` khi cài. Script tự tìm thư mục ứng dụng (nơi có `config.yaml`) bằng `lib/root.sh`, nên cùng một file chạy được trong repo lẫn trên máy đã cài.

Cài và cấu hình máy:

```bash
sudo bash install/install.sh
```

Lệnh này copy bộ cài, cài gói, bật service, rồi cấu hình PBR, IP cổng PX4, TELEM UART, LCD và camera. Không ghi đè `config.yaml` đã có trên máy. Overlay camera, LCD và UART chỉ có hiệu lực sau một lần reboot.

Cùng một bộ cài chạy cho cả CM5 và WalnutPi CM2 (đặt trên đế CM5). Script tự nhận board. Bảng chân và thiết bị của từng board nằm trong `docs/WALNUTPI_CM2_PINOUT.md`.

Kiểm tra phần cứng, chỉ đọc, không cần sudo:

```bash
bash install/test_board.sh
```

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
| `lib/board.sh` | `dronebridge_board` (`rpi` hoặc `walnutpi`), thiết bị theo board (`dronebridge_telem2_dev`, `dronebridge_lcd_bus`, …; khớp `config/board.go`), luật udev, nhóm của user dịch vụ, sửa một khóa trong `config.yaml`, và `walnutpi_ensure_overlay` (bật overlay bằng `set-device`, kiểm tra DTB boot). |

## Gốc `install/`

| File | Việc |
| --- | --- |
| `install.sh` | Cài đủ bộ lên `/opt/dronebridge`: binary, Python, unit systemd, cây `install/`, sudoers camera, bật service. Không ghi đè `config.yaml` nếu máy đã có file này. |
| `deploy.sh` | Từ máy build: biên dịch ARM64, rsync sang Pi, chạy `install.sh` ở đó. |
| `test_board.sh` | [TEST] Kiểm tra TELEM UART, bus LCD (dò `0x3C`), camera, quạt, service theo board. Chỉ đọc. `sudo … --fan-spin`: chạy quạt 100% rồi dừng (5 s mỗi mức) để nhìn, xong trả lại. Trên WalnutPi chưa có overlay thì tạm kéo PI8 (VBUS_EN) lên trong lúc thử. |
| `service/setup_journald.sh` | Giới hạn journald (lưu bền, tối đa 300 MB, luôn chừa 1 GB trống) và tắt stdout của `w2b-dis-eth-led` (WalnutPi, ~500k dòng/ngày; vẫn giữ chức năng LED). `--check` chỉ kiểm tra. Lỗi/cảnh báo của DroneBridge còn được ghi riêng vào `/opt/dronebridge/data/logs/dronebridge-errors.log` (2 MB × 5 file, gộp dòng lặp trong 60 s; chỉnh bằng `log.error_file`, `log.error_file_max_mb`, `log.error_file_keep`). Chỉ xem lỗi trong journal: `journalctl -u dronebridge -p warning`. |
| `service/reregister.sh <uuid>` | Đăng ký lại drone với server, UUID cũ hoặc mới. Các bước: chờ NTP, sao lưu `config.yaml` và `.drone_secret`, dừng dronebridge, ghi `auth.uuid`, chạy `dronebridge --register` bằng user dịch vụ, rồi khởi động lại và chờ xác thực. Nếu đăng ký lỗi thì khôi phục UUID và secret cũ. Dashboard (Token & Phiên → Đổi UUID / đăng ký lại) gọi script này qua `systemd-run`, để nó chạy ngoài cgroup của dronebridge. Chạy tay: `sudo bash /opt/dronebridge/install/service/reregister.sh <uuid>`. Tiến trình ghi ở `data/reregister.json`, log ở `data/logs/reregister.log`. |
| `fan/setup_fan.sh` | WalnutPi: cài overlay `dronebridge-fan` (PI8/IR-RX giữ mức cao thay VBUS_EN để carrier cấp nguồn quạt, PWM 25 kHz PL7 theo nhiệt CPU, tắt node `/pwm-fan` hãng có tach PL8 = GND trên carrier CM5). CM5: chỉ báo trạng thái. `--check` chỉ kiểm tra. |

`install.sh` đặt cả cây `/opt/dronebridge/install/` thuộc root. Các script ở đây chạy bằng root hoặc qua `sudo -n`, nên user dịch vụ không được phép sửa chúng.

## `camera/` — cổng CSI, overlay boot, reboot

| File | Việc |
| --- | --- |
| `setup_camera.sh` | CM5: đọc `Find_landing/camera_detected.json` và ghi block camera trong `/boot/firmware/config.txt`. CAM0 dùng `dtoverlay=<sensor>,cam0`. CAM1 dùng `dtoverlay=<sensor>` vì overlay chính thức không có tham số `cam1`. Không đoán loại cảm biến khi chưa thấy chip. WalnutPi: chuyển sang `walnutpi/setup_imx219.sh`. |
| `walnutpi/setup_imx219.sh` | WalnutPi CM2: cài `imx219.ko`, overlay `imx219` (qua `set-device`), file nạp module riêng, luật udev video, gói ISP `libisp-t527` + `walnutpi.isp`. `--check` chỉ kiểm tra. |
| `walnutpi/imx219/` | Mã nguồn driver IMX219 2 làn cho VIN (`imx219.c`), bản build sẵn `imx219.ko` (5.15.147), overlay `.dts`/`.dtbo`, và `build.sh` để dựng lại khi kernel đổi. |
| `apply_camera_overlay.sh` | Chạy `setup_camera.sh`, rồi reboot nếu overlay vừa đổi. |
| `apply_host_reboot.sh` | Reboot máy khi đã có marker `.need_camera_reboot` hoặc `.need_host_reboot`. |
| `post_boot_clear.sh` | Sau khi máy đã boot lại với overlay mới, xóa marker (kiểm tra thiết bị theo board). Gọi `setup_camera.sh --boot-settled`. `dronebridge-uart-setup.service` chạy nó mỗi lần boot. |
| `watch_camera_overlay.sh` | Chạy nền (root). Khi web lưu loại camera, gọi `apply_camera_overlay.sh`. |
| `install_camera_sudoers.sh` | Ghi `/etc/sudoers.d/dronebridge-camera`. Cho phép user dịch vụ gọi đúng script camera, không qua `bash`, không cần mật khẩu. |
| `install_camera_watch.sh` | Cài unit `dronebridge-camera-watch.service` và bật watcher. |

Service gọi các script này bằng `sudo -n <đường-dẫn-script>`. Sudoers phải khớp đúng đường dẫn đó.

## `network/` — mạng và màn hình

| File | Việc |
| --- | --- |
| `setup_eth.sh` | Gán IP tĩnh cho cổng nối PX4 (`end0` trên CM5, `eth0` trên Pi 4 và WalnutPi). Không đụng 4G hay PBR. |
| `setup_pbr.sh` | Tạo bảng định tuyến 100 / 101 / 103 và iptables mark. `dronebridge-netmon.service` gọi file này trước khi giám sát mạng. |
| `setup_lcd_i2c.sh` | CM5: bật overlay I2C3 (GPIO22/23). WalnutPi: bật overlay `i2c4` (J23 chân 6/8), luật udev `i2c-*` → nhóm `i2c`, `lcd.bus: 4`. Ghi marker reboot nếu boot config / DTB đổi. |
| `setup_mavlink_uart.sh` | CM5: bật UART2 và UART3 4 chân (TX RX CTS RTS) cho TELEM2/TELEM3. WalnutPi: bật overlay `uart2` → `/dev/ttyS2` (J23 chân 4/5), luật udev → nhóm `dialout`, không có TELEM3. Chạy mỗi lần boot qua `dronebridge-uart-setup.service`. |
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
- `dronebridge-uart-setup.service` → `install/network/setup_mavlink_uart.sh`, rồi `install/camera/post_boot_clear.sh`
- `dronebridge-lcd.service` → `lcd_monitor/run_lcd_monitor.py` (thoát ngay nếu `lcd.enabled: false`)
- `dronebridge-camera-watch.service` → `install/camera/watch_camera_overlay.sh`

## Không nằm trong `install/`

Các file sau không phải bộ cài, nên không chuyển vào đây:

- `docker/` — script của container
- `third_party/sim8262/scripts/` — driver modem, đi cùng thư viện đó
- `test_cameras.sh`, `udp_mavlink_rate.sh`, `tcp_auth_sniff.sh`, `tcp_auth_rate.sh`, `Module_4G/test_4g_diagnose.sh` — lệnh kiểm tra thủ công
