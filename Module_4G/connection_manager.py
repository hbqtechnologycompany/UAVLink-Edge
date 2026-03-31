#!/usr/bin/env python3
"""
DroneBridge Network Monitor — Policy-Based Routing (PBR) Mode
=============================================================
Trách nhiệm:
  - Giữ WiFi default route hệ thống KHÔNG thay đổi → SSH luôn hoạt động
  - Chỉ quản lý routing table 100 (dronebridge) cho traffic của DroneBridge app
  - Monitor 4G/WiFi và trigger reinit 4G qua systemd khi cần

Yêu cầu:
  - /etc/iproute2/rt_tables phải có dòng "100 dronebridge"
  - setup_pbr.sh phải chạy trước (qua ExecStartPre trong systemd)
  - Chạy với quyền root (sudo)

Tác giả: DroneBridge Team
Phiên bản: 2.0 (PBR, không dùng metric)
"""

import subprocess
import time
import json
import re
import os
import sys
import logging
import socket
import yaml
from datetime import datetime


# ─── Cấu hình ───────────────────────────────────────────────────────────────
PBR_TABLE       = 100        # Routing table dành riêng cho DroneBridge
PBR_TABLE_NAME  = "dronebridge"
FWMARK          = "0x1"      # Packet mark tương ứng với ip rule (normalized)
FWMARK_MASK     = "0x1/0x1"  # Match bit 0, ignore other mark bits

PING_HOST           = "8.8.8.8"
MONITOR_INTERVAL_S  = 30     # Giây giữa các lần kiểm tra
FAIL_THRESHOLD      = 1      # Drone đang bay: phản ứng nhanh khi 4G mất
REINIT_TIMEOUT_S    = 120    # Giây tối đa chờ 4G phục hồi sau reinit
REINIT_RETRY_MIN    = 5      # Phút tối thiểu giữa 2 lần reinit
REINIT_RETRY_MIN_WIFI = 1    # Khi có WiFi fallback, retry 4G sớm (phút)
REINIT_RETRY_MAX_WIFI = 2    # Trần backoff khi đang fallback WiFi (phút)

try:
    REINIT_RETRY_MIN_WIFI = int(os.getenv("DRONEBRIDGE_REINIT_RETRY_MIN_WIFI", str(REINIT_RETRY_MIN_WIFI)))
except Exception:
    REINIT_RETRY_MIN_WIFI = 1

try:
    REINIT_RETRY_MAX_WIFI = int(os.getenv("DRONEBRIDGE_REINIT_RETRY_MAX_WIFI", str(REINIT_RETRY_MAX_WIFI)))
except Exception:
    REINIT_RETRY_MAX_WIFI = 2

SYSTEMD_4G_SERVICE  = "dronebridge-4g-init.service"
STATUS_FILE         = "/run/dronebridge/network_status.json"
LOG_DIR             = os.getenv("DRONEBRIDGE_LOG_DIR", "/home/pi/Run_serverGo/logs")
EVENT_LOG_FILE      = os.path.join(LOG_DIR, "4g_link_events.log")
EGRESS_LOG_FILE     = os.path.join(LOG_DIR, "egress_path.log")

WG_COUNTER_DPORT    = os.getenv("DRONEBRIDGE_WG_PORT", "51820")
HEALTH_TCP_HOST     = os.getenv("DRONEBRIDGE_HEALTH_TCP_HOST", "45.117.171.237")
HEALTH_TCP_PORT     = int(os.getenv("DRONEBRIDGE_HEALTH_TCP_PORT", "5770"))
HEALTH_TCP_FALLBACK_HOST = os.getenv("DRONEBRIDGE_HEALTH_TCP_FALLBACK_HOST", "1.1.1.1")
HEALTH_TCP_FALLBACK_PORT = int(os.getenv("DRONEBRIDGE_HEALTH_TCP_FALLBACK_PORT", "443"))

CONFIG_FILE         = os.getenv("DRONEBRIDGE_CONFIG", "/opt/dronebridge/config.yaml")
FORCE_4G_ONLY       = True # Default, will be overwritten by config.yaml
NETWORK_MODE        = "prefer_4g"
FALLBACK_DELAY_S    = 300

# ─── Logging ─────────────────────────────────────────────────────────────────
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
    handlers=[
        logging.StreamHandler(sys.stdout),
    ]
)
log = logging.getLogger("netmon")


# ─── Helpers ─────────────────────────────────────────────────────────────────
def run(cmd, timeout=10, check=False):
    """Chạy lệnh, trả về (returncode, stdout, stderr)."""
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        if check and r.returncode != 0:
            raise subprocess.CalledProcessError(r.returncode, cmd, r.stdout, r.stderr)
        return r.returncode, r.stdout.strip(), r.stderr.strip()
    except subprocess.TimeoutExpired:
        return -1, "", f"timeout {timeout}s"
    except Exception as e:
        return -1, "", str(e)


def run_root(cmd, timeout=10):
    return run(["sudo"] + cmd, timeout=timeout)


# ─── NetworkMonitor ────────────────────────────────────────────────────────────
class NetworkMonitor:
    def __init__(self):
        self._4g_failures  = 0
        self._last_reinit  = 0.0
        self._reinit_count = 0  # Exponential tracking
        self._active_iface = None
        self._4g_down_since = 0.0  # Time when 4G first went down
        # Non-blocking reinit tracking
        self._reinit_pending     = False  # True khi đang chờ 4G-init service phục hồi
        self._reinit_pending_t   = 0.0   # Thời điểm bắt đầu chờ
        self._last_udp_bytes_wwan0 = None
        self._last_udp_sample_ts = None
        self._wwan_probe_mode = "INIT"
        self._config = {}
        os.makedirs(os.path.dirname(STATUS_FILE), exist_ok=True)
        os.makedirs(LOG_DIR, exist_ok=True)
        self.load_config()

    def load_config(self):
        """Đọc config.yaml để lấy chế độ mạng."""
        try:
            if os.path.exists(CONFIG_FILE):
                with open(CONFIG_FILE, "r") as f:
                    self._config = yaml.safe_load(f) or {}
                
                net_cfg = self._config.get("network", {})
                global NETWORK_MODE, FALLBACK_DELAY_S, FORCE_4G_ONLY
                NETWORK_MODE = net_cfg.get("mode", "prefer_4g").lower()
                FALLBACK_DELAY_S = int(net_cfg.get("fallback_delay", 300))
                FORCE_4G_ONLY = (NETWORK_MODE == "4g_only")
                log.info(f"Loaded config: mode={NETWORK_MODE}, fallback={FALLBACK_DELAY_S}s")
            else:
                log.warning(f"Config file not found at {CONFIG_FILE}, using defaults")
        except Exception as e:
            log.error(f"Error loading config.yaml: {e}")

    @staticmethod
    def _format_bandwidth(bps: float) -> str:
        """Format tốc độ băng thông theo đơn vị dễ đọc (decimal)."""
        if bps >= 1_000_000_000:
            return f"{bps / 1_000_000_000:.2f}Gbps"
        if bps >= 1_000_000:
            return f"{bps / 1_000_000:.2f}Mbps"
        if bps >= 1_000:
            return f"{bps / 1_000:.2f}Kbps"
        return f"{bps:.0f}bps"

    def log_link_event(self, wwan_ip, wlan_ip, wwan_ok, wlan_ok):
        """Ghi log trạng thái link định kỳ với timestamp rõ ràng."""
        ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        sig = self.get_4g_signal_metrics() if wwan_ip else {
            "quality": "N/A", "rssi": "N/A", "rsrp": "N/A", "snr": "N/A"
        }
        line = (
            f"{ts} | 4G_state={'UP' if wwan_ok else 'DOWN'} 4G_ip={wwan_ip or 'none'} 4G_ping={'OK' if wwan_ok else 'FAIL'} "
            f"4G_probe={self._wwan_probe_mode} "
            f"4G_signal={sig['quality']} rssi={sig['rssi']} rsrp={sig['rsrp']} snr={sig['snr']} "
            f"| WiFi_state={'UP' if wlan_ok else 'DOWN'} WiFi_ip={wlan_ip or 'none'} WiFi_ping={'OK' if wlan_ok else 'FAIL'} "
            f"| active={self._active_iface or 'none'}"
        )
        try:
            with open(EVENT_LOG_FILE, "a") as f:
                f.write(line + "\n")
        except Exception as e:
            log.warning(f"Không ghi được event log file: {e}")

    def get_udp_wwan0_counter(self):
        """Lấy counter UDP out qua wwan0 (rule ACCEPT udp dpt:WG port)."""
        rc, out, err = run_root(["iptables", "-w", "-nvx", "-L", "OUTPUT"], timeout=8)
        if rc != 0 or not out:
            return None, None, err or "iptables output rỗng"

        for line in out.splitlines():
            if (
                "ACCEPT" in line
                and "wwan0" in line
                and f"udp dpt:{WG_COUNTER_DPORT}" in line
            ):
                m = re.match(r"\s*(\d+)\s+(\d+)\s+", line)
                if m:
                    return int(m.group(1)), int(m.group(2)), None
                return None, None, f"parse lỗi: {line.strip()}"

        return None, None, "không tìm thấy rule counter udp out wwan0"

    def log_udp_wwan0_counter(self):
        """Ghi log băng thông UDP out qua wwan0 ở dạng đơn giản."""
        ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        sample_ts = time.time()
        pkts, byts, err = self.get_udp_wwan0_counter()

        if pkts is None or byts is None:
            line = (
                f"{ts} | udp_out_wwan0_dport{WG_COUNTER_DPORT} bytes_total_kb=N/A "
                f"interval_s=N/A bandwidth=N/A "
                f"active={self._active_iface or 'none'} error={err or 'unknown'}"
            )
        else:
            if self._last_udp_bytes_wwan0 is None:
                delta_byts = 0
                elapsed_s = 0.0
            else:
                delta_byts = max(0, byts - self._last_udp_bytes_wwan0)
                if self._last_udp_sample_ts is None:
                    elapsed_s = float(MONITOR_INTERVAL_S)
                else:
                    elapsed_s = max(0.001, sample_ts - self._last_udp_sample_ts)

            bandwidth_bps = ((delta_byts * 8.0) / elapsed_s) if elapsed_s > 0 else 0.0
            bandwidth_human = self._format_bandwidth(bandwidth_bps)
            total_kb = byts / 1024.0

            self._last_udp_bytes_wwan0 = byts
            self._last_udp_sample_ts = sample_ts

            line = (
                f"{ts} | udp_out_wwan0_dport{WG_COUNTER_DPORT} bytes_total_kb={total_kb:.1f} "
                f"interval_s={elapsed_s:.1f} bandwidth={bandwidth_human} "
                f"active={self._active_iface or 'none'}"
            )

        try:
            with open(EGRESS_LOG_FILE, "a") as f:
                f.write(line + "\n")
        except Exception as e:
            log.warning(f"Không ghi được egress log file: {e}")

    def get_4g_signal_metrics(self):
        """Lấy chất lượng sóng 4G từ qmicli (nếu khả dụng)."""
        rc, out, _ = run(["qmicli", "-d", "/dev/cdc-wdm0", "--nas-get-signal-strength"], timeout=8)
        if rc != 0 or not out:
            return {"quality": "UNKNOWN", "rssi": "N/A", "rsrp": "N/A", "snr": "N/A"}

        rssi_match = re.search(r"RSSI:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dBm'", out)
        rsrp_match = re.search(r"RSRP:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dBm'", out)
        snr_match = re.search(r"SNR:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dB'", out)

        rssi = int(float(rssi_match.group(1))) if rssi_match else None
        rsrp = int(float(rsrp_match.group(1))) if rsrp_match else None
        snr = float(snr_match.group(1)) if snr_match else None

        quality = "UNKNOWN"
        if rsrp is not None:
            if rsrp >= -90:
                quality = "STRONG"
            elif rsrp >= -100:
                quality = "GOOD"
            elif rsrp >= -110:
                quality = "FAIR"
            else:
                quality = "WEAK"
        elif rssi is not None:
            if rssi >= -70:
                quality = "STRONG"
            elif rssi >= -85:
                quality = "GOOD"
            elif rssi >= -100:
                quality = "FAIR"
            else:
                quality = "WEAK"

        return {
            "quality": quality,
            "rssi": f"{rssi}dBm" if rssi is not None else "N/A",
            "rsrp": f"{rsrp}dBm" if rsrp is not None else "N/A",
            "snr": f"{snr}dB" if snr is not None else "N/A",
        }

    # ── Interface helpers ────────────────────────────────────────────────────
    def get_iface_ip(self, iface: str) -> str | None:
        """Trả về IP nếu interface UP và có địa chỉ IPv4, ngược lại None."""
        rc, out, _ = run(["ip", "-4", "addr", "show", iface])
        if rc != 0 or not out:
            return None
        # Chấp nhận UP, UNKNOWN (tunnel/raw-IP drivers như wwan0)
        if "state UP" not in out and "state UNKNOWN" not in out:
            if ",UP>" not in out and "<UP," not in out:
                return None
        m = re.search(r"inet (\d+\.\d+\.\d+\.\d+)", out)
        return m.group(1) if m else None

    def ping_via(self, iface: str, host=PING_HOST, count=2) -> bool:
        """Ping qua một interface cụ thể."""
        # Với wwan0 (raw-IP), main table thường không có default qua 4G.
        # Thêm route host tạm để tránh false-negative khi kiểm tra ping.
        if iface == "wwan0":
            route_added = False
            run_root(["ip", "route", "del", f"{host}/32", "dev", "wwan0"], timeout=3)
            rc_add, _, _ = run_root(["ip", "route", "add", f"{host}/32", "dev", "wwan0"], timeout=4)
            route_added = (rc_add == 0)
            rc, _, _ = run(["ping", "-c", str(count), "-W", "3", "-I", iface, host], timeout=12)
            if route_added:
                run_root(["ip", "route", "del", f"{host}/32", "dev", "wwan0"], timeout=3)
            return rc == 0

        rc, _, _ = run(["ping", "-c", str(count), "-W", "3", "-I", iface, host], timeout=12)
        return rc == 0

    def tcp_probe_via_wwan(self, wwan_ip: str, host: str = HEALTH_TCP_HOST, port: int = HEALTH_TCP_PORT, timeout_s: int = 4) -> bool:
        """Fallback health-check cho 4G khi ICMP bị chặn: thử TCP connect ép route qua wwan0."""
        if not wwan_ip:
            return False

        def probe_one(target_host: str, target_port: int) -> bool:
            route_added = False
            try:
                run_root(["ip", "route", "del", f"{target_host}/32", "dev", "wwan0"], timeout=3)
                rc_add, _, _ = run_root(["ip", "route", "add", f"{target_host}/32", "dev", "wwan0"], timeout=4)
                route_added = (rc_add == 0)
                if not route_added:
                    return False

                # Không bind source IP vì trên một số modem raw-IP việc bind có thể timeout giả.
                s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                s.settimeout(timeout_s)
                s.connect((target_host, target_port))
                s.close()
                return True
            except Exception:
                return False
            finally:
                if route_added:
                    run_root(["ip", "route", "del", f"{target_host}/32", "dev", "wwan0"], timeout=3)

        if probe_one(host, port):
            return True

        # Fallback public endpoint để tránh phụ thuộc hoàn toàn vào auth endpoint.
        if HEALTH_TCP_FALLBACK_HOST and (
            HEALTH_TCP_FALLBACK_HOST != host or HEALTH_TCP_FALLBACK_PORT != port
        ):
            return probe_one(HEALTH_TCP_FALLBACK_HOST, HEALTH_TCP_FALLBACK_PORT)
        return False

    def get_wwan_gateway(self) -> str | None:
        """Lấy peer/gateway của wwan0 từ 'ip addr show'."""
        _, out, _ = run(["ip", "-4", "addr", "show", "wwan0"])
        m = re.search(r"peer (\d+\.\d+\.\d+\.\d+)", out)
        return m.group(1) if m else None

    def get_wifi_gateway(self) -> str | None:
        """Lấy gateway của wlan0 từ route table."""
        _, out, _ = run(["ip", "route", "show", "dev", "wlan0"])
        m = re.search(r"via (\d+\.\d+\.\d+\.\d+)", out)
        if m:
            return m.group(1)
        # Fallback: tính từ IP theo quy ước .1
        ip = self.get_iface_ip("wlan0")
        if ip:
            return ip.rsplit(".", 1)[0] + ".1"
        return None

    # ── System WiFi route (SSH) ──────────────────────────────────────────────
    def ensure_system_wifi_route(self):
        """
        Đảm bảo default route hệ thống (main table) LUÔN trỏ vào wlan0.
        Đây là route dùng cho SSH và các ứng dụng hệ thống khác.
        KHÔNG BAO GIỜ xóa route này.
        """
        _, out, _ = run(["ip", "route", "show", "default", "dev", "wlan0"])
        if "default" in out:
            return  # Đã có, không cần làm gì

        gw = self.get_wifi_gateway()
        if not gw:
            log.warning("[SYS_ROUTE] Chưa tìm được WiFi gateway để restore system route")
            return

        rc, _, err = run_root(["ip", "route", "add", "default", "via", gw, "dev", "wlan0"])
        if rc == 0:
            log.info(f"[SYS_ROUTE] ✅ Restored system WiFi default route via {gw} (SSH OK)")
        else:
            log.warning(f"[SYS_ROUTE] Không restore được WiFi route: {err}")

    # ── PBR table 100 — chỉ ảnh hưởng DroneBridge ───────────────────────────
    def set_pbr_route(self, iface: str, gateway: str = None):
        """
        Cập nhật routing table 100 (dronebridge).
        CHỈ DroneBridge traffic (đã bị mark 0x01) mới đi theo route này.
        SSH và hệ thống KHÔNG bị ảnh hưởng.
        """
        # Flush table cũ
        run_root(["ip", "route", "flush", "table", str(PBR_TABLE)])

        if gateway:
            cmd = ["ip", "route", "add", "default", "via", gateway,
                   "dev", iface, "table", str(PBR_TABLE)]
        else:
            cmd = ["ip", "route", "add", "default",
                   "dev", iface, "table", str(PBR_TABLE)]

        rc, _, err = run_root(cmd)
        if rc == 0:
            gw_str = f"via {gateway}" if gateway else "dev-only"
            log.info(f"[PBR] table {PBR_TABLE}: {iface} ({gw_str}) → DroneBridge packets")
            return True
        else:
            log.error(f"[PBR] Không set được route table {PBR_TABLE}: {err}")
            return False

    def get_pbr_active_iface(self) -> str | None:
        """Đọc default route hiện tại trong table 100 để biết interface đang thực sự active."""
        rc, out, _ = run(["ip", "route", "show", "table", str(PBR_TABLE), "default"])
        if rc != 0 or not out.strip():
            return None
        m = re.search(r"\bdev\s+(\S+)", out)
        return m.group(1) if m else None

    def verify_pbr_rule(self):
        """Kiểm tra ip rule fwmark tồn tại, tự tạo lại nếu mất (sau reboot)."""
        _, out, _ = run(["ip", "rule", "show"])
        if re.search(r"fwmark\s+0x1(?:/0x1)?\s+lookup\s+" + re.escape(PBR_TABLE_NAME), out):
            return
        log.warning("[PBR] ip rule fwmark mất — tạo lại...")
        run_root(["ip", "rule", "add", "fwmark", FWMARK_MASK,
                  "table", str(PBR_TABLE), "priority", "100"])
        log.info(f"[PBR] ip rule restored: fwmark {FWMARK_MASK} → table {PBR_TABLE}")

    def verify_pbr_route(self, iface: str, gateway: str = None):
        """Đảm bảo table 100 luôn có default route đúng với interface active."""
        _, out, _ = run(["ip", "route", "show", "table", str(PBR_TABLE)])
        if not out.strip():
            log.warning(f"[PBR] table {PBR_TABLE} đang rỗng — khôi phục route cho {iface}")
            self.set_pbr_route(iface, gateway=gateway)
            return

        if iface in out and "default" in out:
            return

        log.warning(f"[PBR] table {PBR_TABLE} lệch interface active ({iface}) — sửa lại")
        self.set_pbr_route(iface, gateway=gateway)

    # ── 4G reinit via systemd ────────────────────────────────────────────────
    def trigger_4g_reinit(self, wlan_ok=False) -> bool:
        """
        Yêu cầu systemd restart dronebridge-4g-init.service.

        NON-BLOCKING: Hàm này trả về ngay lập tức để không chặn vòng lặp
        routing. Kết quả phục hồi được phát hiện ở các lần gọi tiếp theo
        (mỗi MONITOR_INTERVAL_S giây).

        Returns:
            True  — 4G vừa phục hồi (wwan0 có IP) trong lần gọi này.
            False — chưa phục hồi hoặc đang trong thời gian backoff/chờ.
        """
        now = time.time()

        # ── Đang chờ kết quả của một reinit trước đó ────────────────────
        if self._reinit_pending:
            elapsed = now - self._reinit_pending_t
            wwan_ip = self.get_iface_ip("wwan0")
            if wwan_ip:
                log.info(f"✅ [REINIT] 4G phục hồi sau {elapsed:.0f}s (IP: {wwan_ip})")
                self._reinit_pending   = False
                self._reinit_count     = 0  # reset backoff khi thành công
                return True

            if elapsed >= REINIT_TIMEOUT_S:
                log.error(
                    f"❌ [REINIT] 4G không phục hồi sau {elapsed:.0f}s "
                    f"(timeout {REINIT_TIMEOUT_S}s)"
                )
                self._reinit_pending = False
                # _reinit_count đã được tăng khi trigger, giữ nguyên để backoff
            else:
                log.debug(
                    f"[REINIT] ⏳ Đang chờ 4G: {elapsed:.0f}s / {REINIT_TIMEOUT_S}s"
                )
            return False

        # ── Kiểm tra Exponential Backoff ────────────────────────────────
        if wlan_ok:
            # Khi fallback WiFi (drone đang bay), retry 4G nhanh hơn để kéo lại sớm.
            base_wait_min = REINIT_RETRY_MIN_WIFI * (2 ** self._reinit_count)
            base_wait_min = min(base_wait_min, max(1, REINIT_RETRY_MAX_WIFI))
        else:
            base_wait_min = REINIT_RETRY_MIN * (2 ** self._reinit_count)
            if base_wait_min > 60:
                base_wait_min = 60

        elapsed_min = (now - self._last_reinit) / 60
        if self._last_reinit > 0 and elapsed_min < base_wait_min:
            if int(elapsed_min * 60) % 60 < MONITOR_INTERVAL_S:
                log.info(
                    f"[REINIT] Throttle: {elapsed_min:.1f}/{base_wait_min} min "
                    f"(Lvl {self._reinit_count}) — đang bảo vệ hardware"
                )
            return False

        # ── Trigger reinit ───────────────────────────────────────────────
        log.warning(
            f"⚡ [REINIT] Triggering {SYSTEMD_4G_SERVICE} restart "
            f"(Attempt #{self._reinit_count + 1})..."
        )
        self._last_reinit      = now
        self._reinit_count    += 1
        self._reinit_pending   = True
        self._reinit_pending_t = now

        # timeout=15s chỉ áp dụng cho subprocess systemctl (client), không phải
        # thời gian chạy thực của service — systemd tiếp tục chạy service nền.
        run_root(["systemctl", "restart", SYSTEMD_4G_SERVICE], timeout=15)
        log.info(
            f"[REINIT] dronebridge-4g-init.service đã được kích hoạt — "
            f"polling phục hồi mỗi {MONITOR_INTERVAL_S}s (tối đa {REINIT_TIMEOUT_S}s)"
        )
        return False  # chưa phục hồi ngay, sẽ detect ở lần gọi tiếp theo
    def apply_routing_policy(self):
        """
        Logic ưu tiên (áp dụng cho DroneBridge traffic qua PBR table 100):
        1. 4G_ONLY: Luôn dùng 4G, không fallback.
        2. WIFI_ONLY: Luôn dùng WiFi.
        3. PREFER_4G: Dùng 4G nếu ổn. Nếu 4G mất > fallback_delay, chuyển sang WiFi.
        """
        self.verify_pbr_rule()
        self.ensure_system_wifi_route()

        wwan_ip = self.get_iface_ip("wwan0")
        wlan_ip = self.get_iface_ip("wlan0")
        
        # Check 4G health
        wwan_ping_ok = bool(wwan_ip) and self.ping_via("wwan0", count=1)
        wwan_tcp_ok = False
        if bool(wwan_ip) and not wwan_ping_ok:
            wwan_tcp_ok = self.tcp_probe_via_wwan(wwan_ip)
        wwan_ok = wwan_ping_ok or wwan_tcp_ok
        
        self._wwan_probe_mode = "PING_OK" if wwan_ping_ok else ("TCP_OK" if wwan_tcp_ok else "FAIL")
        wlan_ok = bool(wlan_ip) and self.ping_via("wlan0", count=1)

        # Routing Logic
        target_iface = None
        target_gw = None
        
        if NETWORK_MODE == "wifi_only":
            if wlan_ok:
                target_iface = "wlan0"
                target_gw = self.get_wifi_gateway()
            else:
                target_iface = "wlan0" # Still target it even if down
        elif NETWORK_MODE == "4g_only":
            target_iface = "wwan0"
            target_gw = self.get_wwan_gateway()
        else: # prefer_4g
            if wwan_ok:
                self._4g_down_since = 0.0
                target_iface = "wwan0"
                target_gw = self.get_wwan_gateway()
            else:
                if self._4g_down_since == 0.0:
                    self._4g_down_since = time.time()
                
                elapsed_down = time.time() - self._4g_down_since
                if wlan_ok and elapsed_down > FALLBACK_DELAY_S:
                    target_iface = "wlan0"
                    target_gw = self.get_wifi_gateway()
                    if int(elapsed_down) % 180 < MONITOR_INTERVAL_S:
                        log.warning(f"⚠️ 4G down > {FALLBACK_DELAY_S}s ({elapsed_down:.0f}s). Fallback to WiFi.")
                else:
                    target_iface = "wwan0"
                    target_gw = self.get_wwan_gateway()
                    if int(elapsed_down) % 180 < MONITOR_INTERVAL_S:
                        log.info(f"⏳ 4G down ({elapsed_down:.0f}s). Waiting {FALLBACK_DELAY_S}s before WiFi fallback.")

        # Apply route
        if target_iface:
            if self.get_pbr_active_iface() != target_iface:
                self.set_pbr_route(target_iface, gateway=target_gw)
            self.verify_pbr_route(target_iface, gateway=target_gw)
        
        # Trigger 4G Reinit if down (regardless of target_iface, except in wifi_only)
        if not wwan_ok and NETWORK_MODE != "wifi_only":
            self._4g_failures += 1
            if self._4g_failures >= FAIL_THRESHOLD:
                if self.trigger_4g_reinit(wlan_ok=wlan_ok):
                    self._4g_failures = 0
        else:
            self._4g_failures = 0
            self._reinit_count = 0

        self.log_link_event(wwan_ip, wlan_ip, wwan_ok, wlan_ok)
        self.log_udp_wwan0_counter()
        self._save_status(wwan_ip, wlan_ip, wwan_ok, wlan_ok)

    def _save_status(self, wwan_ip, wlan_ip, wwan_ok, wlan_ok):
        """Ghi trạng thái ra file để dronebridge app và web có thể đọc."""
        status = {
            "timestamp":        int(time.time()),
            "mode":             NETWORK_MODE,
            "fallback_delay":   FALLBACK_DELAY_S,
            "active_interface": self._active_iface,
            "pbr_table":        PBR_TABLE,
            "4g":  {"ip": wwan_ip, "online": wwan_ok},
            "wifi": {"ip": wlan_ip, "online": wlan_ok},
            "ssh_route": "wlan0 (system default — unchanged)",
        }
        try:
            with open(STATUS_FILE, "w") as f:
                json.dump(status, f, indent=2)
        except Exception as e:
            log.warning(f"Không ghi được status file: {e}")

    # ── Main loop ────────────────────────────────────────────────────────────
    def run(self):
        log.info("=" * 60)
        log.info(" DroneBridge Network Monitor v2.0 — PBR Mode")
        log.info(f" PBR table: {PBR_TABLE} ({PBR_TABLE_NAME})")
        log.info(f" Mark: {FWMARK} | Interval: {MONITOR_INTERVAL_S}s")
        log.info(f" Mode: {'4G-only' if FORCE_4G_ONLY else 'legacy-fallback'}")
        log.info(f" Fail threshold: {FAIL_THRESHOLD} | Reinit timeout: {REINIT_TIMEOUT_S}s")
        log.info("=" * 60)

        while True:
            try:
                self.apply_routing_policy()
            except Exception as e:
                log.exception(f"Unexpected error in routing policy: {e}")
            time.sleep(MONITOR_INTERVAL_S)


# ─── CLI ──────────────────────────────────────────────────────────────────────
def main():
    if len(sys.argv) >= 2 and sys.argv[1] == "status":
        # Quick status check
        m = NetworkMonitor()
        wwan = m.get_iface_ip("wwan0")
        wlan = m.get_iface_ip("wlan0")
        wwan_ok = bool(wwan) and m.ping_via("wwan0")
        wlan_ok = bool(wlan) and m.ping_via("wlan0")
        _, pbr_routes, _ = run(["ip", "route", "show", "table", str(PBR_TABLE)])
        _, sys_route, _ = run(["ip", "route", "show", "default"])
        print(f"Mode: {NETWORK_MODE} (fallback: {FALLBACK_DELAY_S}s)")
        print(f"4G:   {'✅' if wwan_ok else '❌'} {wwan or 'no IP'}")
        print(f"WiFi: {'✅' if wlan_ok else '❌'} {wlan or 'no IP'}")
        print(f"PBR table {PBR_TABLE}: {pbr_routes or 'empty'}")
        print(f"System default: {sys_route or 'none'}")
        return

    NetworkMonitor().run()


if __name__ == "__main__":
    main()
