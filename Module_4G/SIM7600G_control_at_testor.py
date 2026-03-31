#!/usr/bin/env python3
"""
SIM7600G-H Control Script
==============================
GPIO Logic (qua transistor Q8, Q9 đảo logic):
  - CM5_ON_OFF_4G (GPIO10): LOW = Power ON, HIGH = Power OFF
  - CM5_RESET_4G (GPIO22):  LOW = Normal,   HIGH = Reset
  - W_DISABLE1 (GPIO2):     HIGH = RF Enabled
  - W_DISABLE2 (GPIO3):     HIGH = GNSS Enabled
"""

import subprocess
import time
import serial
import serial.tools.list_ports
from datetime import datetime

GPIOCHIP = "gpiochip0"
BAUD_RATE = 115200

# GPIO của CM5 điều khiển SIM7600
GPIO_POWER_MAIN = 27       # HIGH = Power ON, LOW = Power OFF (nguồn chính)
GPIO_CM5_ON_OFF_4G = 10    # LOW = Power ON (Q8 đảo logic)
GPIO_CM5_RESET_4G = 22     # LOW = Normal (Q9 đảo logic)
GPIO_W_DISABLE1 = 2        # HIGH = RF Enabled (trực tiếp)
GPIO_W_DISABLE2 = 3        # HIGH = GNSS Enabled (trực tiếp)

def gpio_set(line, value):
    """Set GPIO line to value (0 or 1)"""
    try:
        subprocess.run(f"gpioset {GPIOCHIP} {line}={'1' if value else '0'}".split(),
                      capture_output=True, timeout=2)
        return True
    except:
        return False

def power_control(on=True):
    """
    Điều khiển nguồn chính module qua GPIO27
    on=True: Bật nguồn và đợi 40 giây
    on=False: Tắt nguồn
    """
    if on:
        print("\n[POWER] Tắt nguồn module (GPIO27 = 0)...")
        gpio_set(GPIO_POWER_MAIN, 0)
        time.sleep(5)
        print("\n[POWER] Bật nguồn module (GPIO27 = 1)...")
        gpio_set(GPIO_POWER_MAIN, 1)
        print("  ✓ GPIO27 = HIGH")
        print("  Đợi 25 giây cho module boot...")
        for i in range(25, 0, -5):
            print(f"  {i}s...", end="\r")
            time.sleep(5)
        print("  ✓ Hoàn tất          ")
    else:
        print("\n[POWER] Tắt nguồn module (GPIO27 = 0)...")
        gpio_set(GPIO_POWER_MAIN, 0)
        print("  ✓ GPIO27 = LOW")
        time.sleep(3)


def gpio_get(line):
    """Get GPIO line value, returns 0, 1 or None"""
    try:
        result = subprocess.run(f"gpioget {GPIOCHIP} {line}".split(),
                               capture_output=True, text=True, timeout=2)
        return int(result.stdout.strip()) if result.returncode == 0 else None
    except:
        return None

def check_usb_connection(port=None):
    """Kiểm tra kết nối USB, trả về serial object hoặc None"""
    try:
        if not port:
            ports = [p.device for p in serial.tools.list_ports.comports() if 'ttyUSB' in p.device]
            port = '/dev/ttyUSB2' if '/dev/ttyUSB2' in ports else (ports[0] if ports else None)
        if not port:
            return None
        ser = serial.Serial(port, BAUD_RATE, timeout=2, rtscts=False, dsrdtr=False)
        ser.setDTR(False)
        ser.setRTS(False)
        time.sleep(0.3)
        ser.reset_input_buffer()
        return ser
    except:
        return None

def send_at(ser, cmd, wait=2.0, verbose=True):
    """Gửi AT command và trả về response. verbose=True sẽ in ra kết quả."""
    try:
        ser.reset_input_buffer()
        ser.write((cmd + "\r\n").encode())
        ser.flush()
        response = []
        start = time.time()
        while (time.time() - start) < wait:
            if ser.in_waiting > 0:
                line = ser.readline().decode('utf-8', errors='ignore').strip()
                if line:
                    response.append(line)
                if 'OK' in line or 'ERROR' in line:
                    break
            time.sleep(0.05)
        
        result = '\n'.join(response) if response else None
        
        # In ra kết quả nếu verbose=True
        if verbose and result:
            # Kiểm tra OK hay ERROR
            if 'OK' in result:
                status = "✓"
            elif 'ERROR' in result:
                status = "✗"
            else:
                status = "?"
            # In ngắn gọn trên 1 dòng
            short_result = result.replace('\n', ' | ')[:60]
            print(f"    [{status}] {cmd}: {short_result}")
        
        return result
    except Exception as e:
        if verbose:
            print(f"    [✗] {cmd}: Exception - {e}")
        return None

def init_module(wait_network=True, network_timeout=60, power_on_first=False):
    """
    Khởi tạo module SIM7600:
    0. Bật nguồn GPIO27 (nếu power_on_first=True)
    1. Set GPIO đúng logic
    2. Kiểm tra GPIO
    3. Đợi đăng ký mạng (nếu wait_network=True)
    
    Returns: (gpio_ok, network_ok, ser)
    """
    print("=" * 50)
    print("  KHỞI TẠO MODULE SIM7600G-H")
    print("=" * 50)
    
    # === BƯỚC 0: Bật nguồn chính (nếu cần) ===
    if power_on_first:
        power_control(on=True)
    
    # === BƯỚC 1: Set GPIO ===
    print("\n[1/4] Cấu hình GPIO...")
    gpio_config = [
        (GPIO_CM5_ON_OFF_4G, 0, "CM5_ON_OFF_4G", "Power ON"),
        (GPIO_CM5_RESET_4G, 0, "CM5_RESET_4G", "Normal"),
        (GPIO_W_DISABLE1, 1, "W_DISABLE1", "RF ON"),
        (GPIO_W_DISABLE2, 1, "W_DISABLE2", "GNSS ON"),
    ]
    
    for pin, value, name, desc in gpio_config:
        gpio_set(pin, value)
        print(f"  GPIO{pin:2} = {value} ({desc})")
    
    time.sleep(1)
    
    # === BƯỚC 2: Kiểm tra GPIO ===
    print("\n[2/4] Kiểm tra GPIO...")
    gpio_ok = True
    for pin, value, name, desc in gpio_config:
        actual = gpio_get(pin)
        ok = actual == value
        print(f"  {'✓' if ok else '✗'} GPIO{pin:2} = {actual} (expected {value})")
        if not ok:
            gpio_ok = False
    
    if not gpio_ok:
        print("\n✗ GPIO không đúng!")
        return False, False, None
    
    # === BƯỚC 3: Kết nối USB ===
    print("\n[3/4] Kết nối USB...")
    time.sleep(2)  # Đợi USB enumerate
    
    ser = check_usb_connection()
    if not ser:
        print("  ✗ Không tìm thấy USB port")
        return True, False, None
    
    print(f"  ✓ Đã kết nối {ser.port}")
    
    # Kiểm tra module respond
    resp = send_at(ser, "AT")
    if not resp or "OK" not in resp:
        print("  ✗ Module không phản hồi")
        ser.close()
        return True, False, None
    print("  ✓ Module phản hồi OK")
    
    # Kiểm tra RF mode hiện tại
    cfun_resp = send_at(ser, "AT+CFUN?", wait=1) or ""
    current_cfun = -1
    if "+CFUN:" in cfun_resp:
        try:
            current_cfun = int(cfun_resp.split("+CFUN:")[1].split()[0].strip())
        except:
            pass
    
    print(f"  RF Mode: CFUN={current_cfun}", end="")
    
    # Nếu không phải full functionality, bật lại
    if current_cfun != 1:
        print(" → Bật RF...")
        send_at(ser, "AT+CFUN=1", wait=3)
        time.sleep(5)  # Đợi module thoát Low Power Mode và quét mạng
        print("  ✓ RF đã bật, đợi ổn định...")
    else:
        print(" (OK)")
    
    # === TẮT CHẾ ĐỘ TIẾT KIỆM NĂNG LƯỢNG ===
    print("  Tắt chế độ tiết kiệm năng lượng...")
    
    # Tắt Power Saving Mode (PSM)
    send_at(ser, "AT+CPSMS=0", wait=2)  # 0 = Disable PSM
    
    # Tắt eDRX mode (Extended Discontinuous Reception)
    send_at(ser, "AT+CEDRXS=0", wait=2)  # 0 = Disable eDRX
    
    # Tắt Sleep mode
    send_at(ser, "AT+CSCLK=0", wait=2)  # 0 = Disable slow clock (no sleep)
    
    # === CẤU HÌNH LTE BANDS CHO VIỆT NAM ===
    print("  Cấu hình LTE bands cho Việt Nam...")
    
    # Viettel VN sử dụng LTE Band 3 (1800 MHz) làm band chính
    # Default bands của module THIẾU Band 3 và Band 7
    # LTE bands bitmap: Band 1,2,3,7,8,20,28 = 0x00000000080800C7
    # - Band 1 (2100MHz) = bit 0 = 0x1
    # - Band 2 (1900MHz) = bit 1 = 0x2  
    # - Band 3 (1800MHz) = bit 2 = 0x4  ← QUAN TRỌNG cho Viettel
    # - Band 7 (2600MHz) = bit 6 = 0x40
    # - Band 8 (900MHz)  = bit 7 = 0x80
    # - Band 20 (800MHz) = bit 19 = 0x80000
    # - Band 28 (700MHz) = bit 27 = 0x8000000
    gsm_bands = "0x0002000000400183"  # Giữ nguyên GSM bands
    lte_bands = "0x00000000080800C7"  # Bands 1,2,3,7,8,20,28
    tds_bands = "0x0000000000000021"  # Giữ nguyên TDS bands
    send_at(ser, f"AT+CNBP={gsm_bands},{lte_bands},{tds_bands}", wait=3)
    
    # === CẤU HÌNH MẠNG ỔN ĐỊNH (AUTO MODE với LTE ưu tiên) ===
    print("  Cấu hình mạng ổn định...")
    
    # 1. Set AUTO mode (LTE/GSM/WCDMA) để module có thể fallback nếu LTE mất
    # AT+CNMP: 2=Auto, 13=GSM only, 38=LTE only, 39=GSM+WCDMA+LTE, 51=GSM+LTE
    # Dùng AUTO thay vì LTE Only để tránh detach hoàn toàn khi LTE yếu
    send_at(ser, "AT+CNMP=2", wait=2)  # 2 = Auto (ổn định hơn LTE Only)
    
    # 2. Auto operator selection
    send_at(ser, "AT+COPS=0", wait=5)  # Auto selection
    
    # 3. Enable network registration report
    send_at(ser, "AT+CREG=2", wait=1)   # Enable GSM registration URC with location
    send_at(ser, "AT+CEREG=2", wait=1)  # Enable LTE registration URC with location
    
    # 4. Attach to PS domain
    send_at(ser, "AT+CGATT=1", wait=5)  # Attach to PS domain
    
    # 5. Set APN (Viettel default APN)
    send_at(ser, 'AT+CGDCONT=1,"IP","v-internet"', wait=2)  # Viettel APN
    
    print("  ✓ Đã cấu hình (Auto Mode + LTE Band 3 enabled)")
    
    # Đợi module ổn định trước khi check
    time.sleep(5)
    
    # === BƯỚC 4: Đợi đăng ký mạng ===
    if not wait_network:
        print("\n[4/4] Bỏ qua đợi mạng")
        return True, False, ser
    
    print(f"\n[4/4] Đợi đăng ký mạng (tối đa {network_timeout}s)...")
    
    network_ok = False
    start = time.time()
    
    while (time.time() - start) < network_timeout:
        elapsed = int(time.time() - start)
        
        # Kiểm tra CREG (GSM/3G) và CEREG (LTE) - verbose=False trong vòng lặp
        creg = send_at(ser, "AT+CREG?", wait=1, verbose=False) or ""
        cereg = send_at(ser, "AT+CEREG?", wait=1, verbose=False) or ""
        csq = send_at(ser, "AT+CSQ", wait=1, verbose=False) or ""
        cnsmod = send_at(ser, "AT+CNSMOD?", wait=1, verbose=False) or ""  # Network system mode
        
        # Parse signal
        signal = "??"
        if "+CSQ:" in csq:
            try:
                signal = csq.split("+CSQ:")[1].split(",")[0].strip()
            except:
                pass
        
        # Parse network mode từ CNSMOD
        # 0=No service, 1=GSM, 2=GPRS, 3=EGPRS, 4=WCDMA, 5=HSDPA, 6=HSUPA, 7=HSPA, 8=LTE
        net_mode = "Đang tìm"
        if "+CNSMOD:" in cnsmod:
            try:
                mode_val = int(cnsmod.split("+CNSMOD:")[1].split(",")[1].strip())
                mode_names = {0:"No Service", 1:"GSM", 2:"GPRS", 3:"EDGE", 
                             4:"WCDMA", 5:"HSDPA", 6:"HSUPA", 7:"HSPA+", 8:"LTE"}
                net_mode = mode_names.get(mode_val, f"Mode{mode_val}")
            except:
                net_mode = "Đang tìm"
        
        # Kiểm tra đăng ký (1=home, 5=roaming)
        gsm_ok = ",1" in creg or ",5" in creg
        lte_ok = ",1" in cereg or ",5" in cereg
        
        status = "LTE" if lte_ok else ("GSM/3G" if gsm_ok else "...")
        print(f"  [{elapsed:2}s] Signal: {signal}/31 | {net_mode} | {status}", end="\r")
        
        if gsm_ok or lte_ok:
            network_ok = True
            net_type = "4G LTE" if lte_ok else net_mode
            print(f"  [{elapsed:2}s] Signal: {signal}/31 | ✓ Đã đăng ký {net_type}      ")
            break
        
        time.sleep(3)
    
    if not network_ok:
        print(f"\n  ✗ Không đăng ký được mạng sau {network_timeout}s")
    
    # In thông tin cuối
    print("\n" + "-" * 50)
    print("THÔNG TIN MODULE:")
    
    info_cmds = [
        ("AT+CSQ", "Tín hiệu"),
        ("AT+COPS?", "Nhà mạng"),
        ("AT+CPSI?", "Chi tiết"),
        ("AT+CNUM", "SĐT SIM"),
        ("AT+CGMR", "Firmware"),
        ("AT+CSUB", "Build"),
    ]
    
    for cmd, desc in info_cmds:
        resp = send_at(ser, cmd, wait=1) or ""
        for line in resp.split("\n"):
            if line.startswith("+") or (cmd in ["AT+CGMR", "AT+CSUB"] and line and not line.startswith("AT")):
                print(f"  {desc:10}: {line.strip()}")
                break
    
    print("=" * 50)
    result = "THÀNH CÔNG" if (gpio_ok and network_ok) else "CÓ LỖI"
    print(f"KẾT QUẢ: {result}")
    print("=" * 50)
    
    return gpio_ok, network_ok, ser


def monitor_network(ser, interval=30):
    """
    Monitor mạng liên tục mỗi interval giây
    Trả về danh sách logs để tạo báo cáo
    """
    print("\n" + "=" * 70)
    print("  CHẾ ĐỘ MONITOR MẠNG (Nhấn Ctrl+C để dừng và xem báo cáo)")
    print("=" * 70)
    print(f"\nKiểm tra mỗi {interval} giây...")
    print(f"{'Thời gian':<20} | {'Tín hiệu':<8} | {'Trạng thái':<12} | {'Nhà mạng':<15} | {'Loại':<6}")
    print("-" * 70)
    
    logs = []
    check_count = 0
    
    try:
        while True:
            check_count += 1
            timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
            
            # Lấy thông tin (verbose=False để không in trong monitor)
            csq_resp = send_at(ser, "AT+CSQ", wait=1, verbose=False) or ""
            creg_resp = send_at(ser, "AT+CREG?", wait=1, verbose=False) or ""
            cereg_resp = send_at(ser, "AT+CEREG?", wait=1, verbose=False) or ""
            cops_resp = send_at(ser, "AT+COPS?", wait=1, verbose=False) or ""
            cpsi_resp = send_at(ser, "AT+CPSI?", wait=1, verbose=False) or ""
            
            # Parse signal
            signal = "??"
            if "+CSQ:" in csq_resp:
                try:
                    signal = csq_resp.split("+CSQ:")[1].split(",")[0].strip()
                except:
                    pass
            
            # Parse network mode từ CNSMOD
            cnsmod_resp = send_at(ser, "AT+CNSMOD?", wait=1, verbose=False) or ""
            net_mode = "-"
            if "+CNSMOD:" in cnsmod_resp:
                try:
                    mode_val = int(cnsmod_resp.split("+CNSMOD:")[1].split(",")[1].strip())
                    mode_names = {0:"No Svc", 1:"GSM", 2:"GPRS", 3:"EDGE", 
                                 4:"WCDMA", 5:"HSDPA", 6:"HSUPA", 7:"HSPA+", 8:"LTE"}
                    net_mode = mode_names.get(mode_val, f"M{mode_val}")
                except:
                    net_mode = "-"
            
            # Parse network status
            gsm_ok = ",1" in creg_resp or ",5" in creg_resp
            lte_ok = ",1" in cereg_resp or ",5" in cereg_resp
            
            if lte_ok or net_mode == "LTE":
                net_status = "LTE OK"
                net_type = "4G"
            elif gsm_ok:
                net_status = f"{net_mode} OK"
                net_type = net_mode
            else:
                net_status = "NO SERVICE"
                net_type = "-"
            
            # Parse operator
            operator = "-"
            if '+COPS:' in cops_resp:
                try:
                    parts = cops_resp.split('"')
                    if len(parts) >= 2:
                        operator = parts[1][:15]
                except:
                    pass
            
            # Parse tech detail
            tech_detail = ""
            if "+CPSI:" in cpsi_resp:
                try:
                    tech_detail = cpsi_resp.split("+CPSI:")[1].split(",")[0].strip()
                except:
                    pass
            
            # Log entry
            log_entry = {
                'time': timestamp,
                'signal': signal,
                'status': net_status,
                'operator': operator,
                'type': net_type,
                'tech': tech_detail,
                'csq_raw': csq_resp,
                'creg': creg_resp,
                'cereg': cereg_resp,
            }
            logs.append(log_entry)
            
            # In ra
            signal_str = f"{signal}/31"
            print(f"{timestamp:<20} | {signal_str:<8} | {net_status:<12} | {operator:<15} | {net_type:<6}")
            
            # === AUTO RECOVERY: Nếu mất mạng 3 lần liên tiếp → reset module ===
            if len(logs) >= 3:
                last_3 = logs[-3:]
                if all(log['status'] == 'NO SERVICE' for log in last_3):
                    print("\n⚠️  Mất mạng 3 lần liên tiếp → Auto recovery...")
                    
                    # Reset RF nhanh (không hard reset GPIO để tránh mất USB)
                    print("  [1] Reset RF (AT+CFUN=0 → AT+CFUN=1)...")
                    send_at(ser, "AT+CFUN=0", wait=3, verbose=False)
                    time.sleep(2)
                    send_at(ser, "AT+CFUN=1", wait=5, verbose=False)
                    time.sleep(20)  # Đợi module quét lại mạng
                    
                    # Set lại LTE bands (quan trọng!)
                    print("  [2] Set LTE bands (Band 3 cho Viettel)...")
                    gsm_bands = "0x0002000000400183"
                    lte_bands = "0x00000000080800C7"  # Bands 1,2,3,7,8,20,28
                    tds_bands = "0x0000000000000021"
                    send_at(ser, f"AT+CNBP={gsm_bands},{lte_bands},{tds_bands}", wait=3, verbose=False)
                    
                    # Set Auto mode (ổn định hơn LTE Only)
                    print("  [3] Set Auto mode + attach PS...")
                    send_at(ser, "AT+CNMP=2", wait=2, verbose=False)  # Auto
                    send_at(ser, "AT+COPS=0", wait=5, verbose=False)  # Auto operator
                    send_at(ser, "AT+CGATT=1", wait=5, verbose=False)  # Attach PS
                    time.sleep(10)
                    
                    print("  ✓ Auto recovery hoàn tất, tiếp tục monitor...\n")
                    
                    # Clear consecutive fail counter by removing last 3 logs
                    logs.clear()
            
            # Đợi interval giây
            time.sleep(interval)
            
    except KeyboardInterrupt:
        print("\n" + "=" * 70)
        print("  ĐÃ DỪNG MONITOR")
        print("=" * 70)
    
    return logs


def generate_report(logs):
    """Tạo báo cáo từ logs"""
    if not logs:
        print("\nKhông có dữ liệu để tạo báo cáo")
        return
    
    print("\n" + "=" * 70)
    print("  BÁO CÁO MẠNG")
    print("=" * 70)
    
    # Thời gian
    start_time = logs[0]['time']
    end_time = logs[-1]['time']
    duration = len(logs) * 30  # giây
    
    print(f"\n📅 Thời gian:")
    print(f"   Bắt đầu: {start_time}")
    print(f"   Kết thúc: {end_time}")
    print(f"   Tổng số lần kiểm tra: {len(logs)}")
    print(f"   Thời gian monitor: ~{duration // 60} phút {duration % 60} giây")
    
    # Tín hiệu
    signals = [int(log['signal']) if log['signal'].isdigit() else 99 for log in logs]
    valid_signals = [s for s in signals if s != 99]  # Loại bỏ chỉ 99 (unknown)
    
    if valid_signals:
        avg_signal = sum(valid_signals) / len(valid_signals)
        min_signal = min(valid_signals)
        max_signal = max(valid_signals)
        
        print(f"\n📶 Tín hiệu:")
        print(f"   Trung bình: {avg_signal:.1f}/31 ({avg_signal/31*100:.0f}%)")
        print(f"   Tốt nhất: {max_signal}/31")
        print(f"   Kém nhất: {min_signal}/31")
        
        # Đánh giá (chấp nhận từ 0-31, loại trừ 99)
        if avg_signal >= 20:
            quality = "Rất tốt ✓✓✓"
        elif avg_signal >= 15:
            quality = "Tốt ✓✓"
        elif avg_signal >= 10:
            quality = "Trung bình ✓"
        elif avg_signal >= 5:
            quality = "Yếu ✓"
        else:
            quality = "Rất yếu (nhưng vẫn có)"
        print(f"   Đánh giá: {quality}")
    else:
        print(f"\n📶 Tín hiệu:")
        print(f"   Không có tín hiệu hợp lệ (tất cả đều 99)")
    
    # Trạng thái mạng
    service_count = sum(1 for log in logs if log['status'] != 'NO SERVICE')
    no_service_count = len(logs) - service_count
    uptime_percent = (service_count / len(logs) * 100) if logs else 0
    
    print(f"\n📡 Độ ổn định mạng:")
    print(f"   Có dịch vụ: {service_count}/{len(logs)} lần ({uptime_percent:.1f}%)")
    print(f"   Mất mạng: {no_service_count}/{len(logs)} lần")
    
    if uptime_percent >= 95:
        stability = "Rất ổn định ✓✓✓"
    elif uptime_percent >= 80:
        stability = "Ổn định ✓✓"
    elif uptime_percent >= 50:
        stability = "Khá ổn định ✓"
    else:
        stability = "Không ổn định ✗"
    print(f"   Đánh giá: {stability}")
    
    # Nhà mạng
    operators = [log['operator'] for log in logs if log['operator'] != '-']
    if operators:
        most_common = max(set(operators), key=operators.count)
        print(f"\n📞 Nhà mạng: {most_common}")
    
    # Loại mạng
    gsm_count = sum(1 for log in logs if log['type'] == '2G/3G')
    lte_count = sum(1 for log in logs if log['type'] == '4G')
    
    print(f"\n🔗 Loại kết nối:")
    print(f"   2G/3G: {gsm_count} lần")
    print(f"   4G LTE: {lte_count} lần")
    
    # Các lần mất mạng
    if no_service_count > 0:
        print(f"\n⚠️  Các lần mất mạng:")
        for i, log in enumerate(logs):
            if log['status'] == 'NO SERVICE':
                print(f"   {i+1}. {log['time']} - Signal: {log['signal']}/31")
    
    print("\n" + "=" * 70)


# === MAIN ===
if __name__ == "__main__":
    gpio_ok, network_ok, ser = init_module(wait_network=True, network_timeout=60, power_on_first=True)
    
    if ser and network_ok:
        # Bắt đầu monitor
        logs = monitor_network(ser, interval=30)
        
        # Tạo báo cáo
        generate_report(logs)
        
        ser.close()
    elif ser:
        print("\nModule chưa kết nối mạng, không thể monitor")
        ser.close()
