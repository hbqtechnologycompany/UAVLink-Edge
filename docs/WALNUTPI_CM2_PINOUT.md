# WalnutPi CM2 trên đế CM5 — đối chiếu chân

Đế DroneBridge được thiết kế cho Raspberry Pi CM5. Tài liệu này ghi lại những gì của đế dùng được khi cắm module WalnutPi CM2 (Allwinner T527), để quyết định phần cứng sau.

## Nguồn dữ liệu và giả định

- Bảng chân lấy từ lệnh `gpio pin uart|i2c|spi|pwm` trên Walnut Pi OS (Debian 12, kernel 5.15.147, dtb `sun55i-t527-walnutpi-2b`). Lệnh in theo số chân vật lý của header 40 chân kiểu Raspberry Pi.
- Overlay có sẵn trong `/boot/overlays`: `i2c1 i2c2 i2c4 uart2..uart8 spidev1_0 spidev1_1 pcie disable_uart0`. Bật bằng dòng `overlays=` trong `/boot/config.txt` (ví dụ `overlays= i2c1 uart5`), hoặc bằng `set-device`. WalnutPi **không** đọc dòng `dtoverlay=` kiểu Raspberry Pi.
- **Giả định chưa kiểm chứng:** CM2 đưa chân `GPIOx` ra đúng vị trí của CM5 trên đầu nối B2B, nên đế CM5 nối tới GPIOx nào thì CM2 cũng nhận ở GPIOx đó. Cần xác nhận bằng pinout B2B chính thức của CM2 ([wiki WalnutPi](https://wiki.walnutpi.com/en/docs/walnutpi_2/intro/hw-parameter/#walnut-pi-cm2)).

## Bảng GPIO (số GPIO kiểu CM5 → chân T527)

| GPIO | Chân header | T527 | Chức năng trên WalnutPi |
| --- | --- | --- | --- |
| 0 | 27 | PI16 | I2C2_SDA, PWM1-1 |
| 1 | 28 | PI15 | I2C2_SCL, PWM1-0 |
| 2 | 3 | PB5 | I2C1_SDA |
| 3 | 5 | PB4 | I2C1_SCL, PWM0-8 |
| 4 | 7 | PB6 | PWM0-10 |
| 5 | 29 | PL6 | PWM3-4 |
| 6 | 31 | PL5 | PWM3-3 |
| 7 | 26 | PI6 | UART6_TX, SPI1_CS1, PWM0-7 |
| 8 | 24 | PI2 | UART5_TX, SPI1_CS0, PWM0-3 |
| 9 | 21 | PI5 | SPI1_MISO, PWM0-6 |
| 10 | 19 | PI4 | SPI1_MOSI, PWM0-5 |
| 11 | 23 | PI3 | UART5_RX, SPI1_CLK, PWM0-4 |
| 12 | 32 | PI14 | PWM0-15 |
| 13 | 33 | PL4 | PWM3-2 |
| 14 | 8 | PB0 | UART2_TX |
| 15 | 10 | PB1 | UART2_RX |
| 16 | 36 | PI13 | PWM0-14 |
| 17 | 11 | PB13 | UART7_TX |
| 18 | 12 | PB14 | UART7_RX |
| 19 | 35 | PL2 | UART8_TX, PWM3-0 |
| 20 | 38 | PI0 | UART4_TX, I2C4, PWM0-1 |
| 21 | 40 | PI1 | UART4_RX, I2C4, PWM0-2 |
| 22 | 15 | PI11 | UART3_TX, PWM0-12 |
| 23 | 16 | PI10 | PWM0-11 |
| 24 | 18 | PI9 | — |
| 25 | 22 | PI7 | UART6_RX, PWM0-8 |
| 26 | 37 | PL3 | UART8_RX, PWM3-1 |
| 27 | 13 | PI12 | UART3_RX, PWM0-13 |

Trên WalnutPi, `uartN` xuất hiện thành `/dev/ttySN`.

## Chức năng của đế CM5

| Chức năng | Chân trên đế CM5 | Trên CM2 | Trạng thái |
| --- | --- | --- | --- |
| Ethernet nối PX4 | PHY trên module | `eth0` | Chạy, `10.41.10.10/24` |
| TELEM2 (CM5 UART2, `/dev/ttyAMA2`) | TX GPIO4, RX GPIO5, CTS GPIO6, RTS GPIO7 | PB6, PL6, PL5, PI6 | Không dùng được: TX/RX không thuộc UART nào. **Thay bằng UART2 trên J23 chân 4/5 (`/dev/ttyS2`)** |
| TELEM3 (CM5 UART3, `/dev/ttyAMA3`) | TX GPIO8, RX GPIO9, CTS GPIO10, RTS GPIO11 | PI2, PI5, PI4, PI3 | Không dùng: `UART5_RX` rơi vào chân RTS. CM2 chạy không có TELEM3 |
| LCD SH1107 (CM5 I2C3) | SDA GPIO22, SCL GPIO23 | PI11, PI10 | Không dùng được: không có I2C phần cứng. **Thay bằng TWI4 trên J23 chân 6/8 (`/dev/i2c-4`)** |
| Camera CSI (IMX219) | CAM0: DPHY0 D0–D3 + CLK, I2C `SCL0`/`SDA0`, nguồn `CAM_GPIO0` | MIPI A 2 làn (`PK0`–`PK5`), I2C TWI3 (`PK10`/`PK11`), nguồn `PK22` | Chạy: driver VIN imx219 2 làn + ISP AWIspApi, stream cam0 lên MediaMTX |
| Modem 4G SIM8262 (PCIe) | PCIe | PCIe Gen2 x1, dùng chung với USB 3.0 | Chưa kiểm tra (overlay `pcie`) |

## Đầu nối J23 (CM5 EXT PORT)

Mỗi đường tín hiệu đi qua chip chống sét RCLAMP0524 (D8, D55) và có điện trở kéo lên 10k về 3,3 V. Chân 1 là 5 V (qua bộ lọc C143), chân 10 và vỏ nối GND.

| J23 | Tên net trên đế CM5 | GPIO | T527 | Dùng được trên CM2 |
| --- | --- | --- | --- | --- |
| 1 | `CM5_VDD5V` | — | — | Nguồn 5 V |
| 2 | `UART4_TX / I2C2_SDA / SPI5_CSn` | 12 | PI14 | Chỉ GPIO, PWM0-15 |
| 3 | `UART4_RX / I2C2_SCL / SPI5_MISO` | 13 | PL4 | Chỉ GPIO, PWM3-2 |
| 4 | `UART4_CTS / I2C3_SDA / SPI5_MOSI` | 14 | PB0 | **UART2_TX — TELEM2 (chốt)** |
| 5 | `UART4_RTS / I2C3_SCL / SPI5_SCLK` | 15 | PB1 | **UART2_RX — TELEM2 (chốt)** |
| 6 | `SPI1_SCLK` | 21 | PI1 | **TWI4 — LCD (chốt)**; có thể là UART4_RX, PWM0-2 |
| 7 | `SPI1_MISO` | 19 | PL2 | UART8_TX (chỉ TX; RX là PL3 = GPIO26, không có trên J23), PWM3-0 |
| 8 | `SPI1_MOSI` | 20 | PI0 | **TWI4 — LCD (chốt)**; có thể là UART4_TX, PWM0-1 |
| 9 | `SPI1_CS` | 18 | PB14 | UART7_RX (chỉ RX; TX là PB13 = GPIO17, không có trên J23) |
| 10 | GND | — | — | GND |

Chức năng đã chốt trên J23 khi chạy WalnutPi CM2:

- **TELEM2 = UART2: chân 4 (TX của Pi) và chân 5 (RX của Pi)**, `/dev/ttyS2`, 1500000 8N1, không flow control. Trên đế CM5 hai chân này mang tên CTS/RTS của UART4, nên cáp sang PX4 đấu theo vai trò mới: chân 4 → RX của PX4, chân 5 ← TX của PX4.
- **LCD = I2C4 (TWI4): chân 6 (PI1) và chân 8 (PI0)**, `/dev/i2c-4`, SH1107 ở `0x3C`. Đế đã có điện trở kéo lên 10k. Thứ tự SDA/SCL trên PI0/PI1 chưa xác nhận bằng datasheet: nếu `install/test_board.sh` báo không thấy `0x3C`, đảo hai dây.
- Không có TELEM3 trên CM2.

Các chân còn lại của J23 không dùng trong DroneBridge:

- Chân 7 và 9 là nửa UART: chân 7 chỉ phát (UART8_TX), chân 9 chỉ nhận (UART7_RX).
- Chân 2 và 3: chỉ GPIO hoặc PWM.
- Không có SPI trên J23.

## CM5 và CM2 trong UAVLink-Edge

Một `config.yaml` chạy được trên cả hai board. DroneBridge nhận board qua `/proc/device-tree/model` (ghi đè bằng khóa `board: cm5 | walnutpi`) rồi đổi các giá trị mặc định của board kia sang board đang chạy. Giá trị tự đặt (ví dụ `/dev/ttyUSB0`) giữ nguyên.

| Mục | CM5 | WalnutPi CM2 |
| --- | --- | --- |
| Ethernet PX4 | `end0` | `eth0` (chỉ đổi khi tên trong config không tồn tại) |
| TELEM2 | `/dev/ttyAMA2` (GPIO4–7) | `/dev/ttyS2` (J23 chân 4/5) |
| TELEM3 | `/dev/ttyAMA3` (GPIO8–11) | không có |
| LCD | `/dev/i2c-3` (GPIO22/23, `i2c3-pi5,pins_22_23`) | `/dev/i2c-4` (J23 chân 6/8, overlay `i2c4`) |
| Camera cam0 | rpicam / Picamera2 | IMX219 trên VIN + ISP hãng (`walnutpi.isp`) |
| Quạt | firmware Raspberry Pi | overlay `dronebridge-fan`: VBUS_EN trên PI8, PWM 25 kHz trên PL7, không đo RPM |
| Overlay | `dtoverlay=` trong `/boot/firmware/config.txt` | `set-device enable` (dựng lại DTB boot) |

Cài và kiểm tra trên cả hai board giống nhau:

- Cài, hoặc sửa tại chỗ: `sudo bash install/install.sh` (lần đầu) hoặc `sudo bash install/install.sh --check`. Trên CM2, script UART, LCD, camera và quạt bật overlay `uart2`, `i2c4`, `imx219`, `dronebridge-fan`, ghi luật udev (`ttyS2` → nhóm `dialout`, `i2c-*` → `i2c`, video → `video`), rồi đặt các khóa tương ứng trong `config.yaml`. Nếu DTB vừa đổi thì phải reboot một lần.
- Kiểm tra, chỉ đọc: `bash install/test_board.sh`. Thử quạt bằng mắt: `sudo bash install/test_board.sh --fan-spin` (100% rồi dừng, mỗi mức 5 s, xong trả lại như cũ).

### Quạt trên carrier CM5

| B2B | CM5 | CM2 | Kết quả |
| --- | --- | --- | --- |
| 19 | Fan_PWM (open-drain) | PL7 / FAN_PWM (`mcu_pwm0` kênh 5) | điều khiển được |
| 16 | Fan_Tacho (kéo lên 1,8 kΩ trong CM5) | ADC22 (analog) | không đọc được RPM |
| 14 | GND | PL8 / FAN_FRQ | PL8 bị nối đất |
| 111 | VBUS_EN (ra, mức cao = bật) | IR-RX (PI8) | PI8 giữ mức cao thay VBUS_EN |

DTB hãng có sẵn node `/pwm-fan` (PWM PL7 100 kHz, đọc tach bằng ngắt trên PL8). Trên carrier CM5 thì PL8 nằm ở chân GND, nên `fan1_input` luôn bằng 0, và 100 kHz cũng nằm ngoài chuẩn quạt 4 dây (21–28 kHz). Ngoài ra carrier lấy nguồn 5 V cho quạt (và cổng USB3) qua công tắc do VBUS_EN bật, mà CM2 không có VBUS_EN: chân 111 của CM2 là IR-RX, tức PI8 nối vào bộ thu hồng ngoại `irrx`.

Overlay `dronebridge-fan` (`install/fan/walnutpi/`) làm các việc sau:

- Tắt `irrx` và tạo regulator `dronebridge-vbus-en` luôn bật trên PI8 (mức cao 3,3 V từ lúc kernel khởi động), thay cho VBUS_EN.
- Tắt node `/pwm-fan` hãng.
- Tạo `/dronebridge-fan`: PWM 25 kHz trên PL7, `fan-supply` là regulator ở trên, không tach, gắn lại 4 mức làm mát của `cpul_thermal_zone`.

Trên carrier CM5 thì CM2 mất bộ thu hồng ngoại, nhưng chân 111 vốn không nối tới đầu thu hồng ngoại nào.

| CPU | dưới 50 °C | 50 °C | 60 °C | 70 °C trở lên |
| --- | --- | --- | --- | --- |
| PWM | 1/255 (dừng) | 150 | 200 | 255 |

Mức thấp nhất là 1 chứ không phải 0: duty 0 tắt hẳn PWM, PL7 trả về trạng thái sleep (input, kéo xuống yếu), và quạt 4 dây sẽ chạy hết tốc. PL7 là ngõ ra push-pull 3,3 V, khác Fan_PWM open-drain của CM5. Quạt 4 dây thông thường nhận được mức này. Muốn đo RPM thì phải nối dây tach của quạt vào một GPIO trống, có điện trở kéo lên 3,3 V.

Camera trên CM2: driver `imx219.ko` (bản build sẵn cho kernel 5.15.147) và overlay nằm ở `install/camera/walnutpi/imx219/`. Khi kernel đổi, dựng lại bằng `build.sh` trong thư mục đó. Module nạp qua `/etc/modules-load.d/dronebridge-imx219.conf`, còn `ov5647_mipi` bị chặn trong `/etc/modprobe.d/`. Cách này giữ nguyên `vin.conf` của gói `libisp-t527`, nên nâng cấp gói không làm mất camera.

## Cần kiểm chứng bằng pinout B2B chính thức

1. Số chân B2B của GPIO0–27 trên CM2 có trùng CM5 không.
2. Các làn CSI0/CSI1 và I2C camera trên B2B của CM2 so với CM5.
3. Các chân PCIe và USB cho modem 4G.
