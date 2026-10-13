"""Đọc dữ liệu CM5 + DroneBridge thật → MonitorState."""

from __future__ import annotations

import json
import re
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

from lcd.state import MonitorState

DEFAULT_INSTALL_DIR = "/opt/dronebridge"
NETWORK_STATUS = "/run/dronebridge/network_status.json"


def _read_text(path: str | Path, default: str = "") -> str:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").strip()
    except OSError:
        return default


def _load_json(path: str | Path) -> dict | None:
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, TypeError):
        return None


def _http_json(url: str, timeout: float = 1.5) -> dict | None:
    try:
        req = urllib.request.Request(url, headers={"Accept": "application/json"})
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8", errors="replace"))
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError, OSError):
        return None


def _load_dronebridge_config(install_dir: str = DEFAULT_INSTALL_DIR) -> dict:
    cfg_path = Path(install_dir) / "config.yaml"
    if not cfg_path.is_file():
        return {}
    try:
        import yaml  # type: ignore

        with cfg_path.open(encoding="utf-8") as f:
            return yaml.safe_load(f) or {}
    except Exception:
        return {}


def core_temp_c() -> int:
    """Đọc nhiệt độ CPU thật — lấy max các thermal zone + vcgencmd."""
    temps: list[int] = []
    try:
        for path in Path("/sys/class/thermal").glob("thermal_zone*/temp"):
            raw = path.read_text(encoding="utf-8", errors="replace").strip()
            if raw.isdigit():
                temps.append(int(int(raw) / 1000))
    except OSError:
        pass
    try:
        out = subprocess.check_output(
            ["vcgencmd", "measure_temp"],
            text=True,
            stderr=subprocess.DEVNULL,
            timeout=2,
        )
        m = re.search(r"([\d.]+)", out)
        if m:
            temps.append(int(float(m.group(1))))
    except (subprocess.SubprocessError, FileNotFoundError, ValueError):
        pass
    if temps:
        return max(temps)
    raw = _read_text("/sys/class/thermal/thermal_zone0/temp", "0")
    try:
        return int(int(raw) / 1000)
    except ValueError:
        return 0


def cpu_percent(sample_delay: float = 0.15) -> int:
    def snap() -> tuple[int, int]:
        line = _read_text("/proc/stat").splitlines()[0]
        parts = [int(x) for x in line.split()[1:]]
        idle = parts[3] + (parts[4] if len(parts) > 4 else 0)
        return idle, sum(parts)

    i0, t0 = snap()
    time.sleep(sample_delay)
    i1, t1 = snap()
    dt, di = t1 - t0, i1 - i0
    if dt <= 0:
        return 0
    return max(0, min(100, int(100 * (1 - di / dt))))


def ram_info() -> tuple[float, float]:
    info: dict[str, int] = {}
    for line in _read_text("/proc/meminfo").splitlines():
        m = re.match(r"(\w+):\s+(\d+)", line)
        if m:
            info[m.group(1)] = int(m.group(2))
    total_kb = info.get("MemTotal", 0)
    avail = info.get("MemAvailable", info.get("MemFree", 0))
    used_kb = max(0, total_kb - avail)
    return round(used_kb / 1024 / 1024, 1), round(total_kb / 1024 / 1024, 1)


def uptime_seconds() -> int:
    try:
        return int(float(_read_text("/proc/uptime", "0").split()[0]))
    except (IndexError, ValueError):
        return 0


def signal_bars_from_dbm(dbm: int) -> int:
    if dbm >= -50:
        return 5
    if dbm >= -60:
        return 4
    if dbm >= -70:
        return 3
    if dbm >= -80:
        return 2
    if dbm >= -90:
        return 1
    return 0


def _vpn_via_label(active_iface: str | None) -> str:
    if not active_iface:
        return ""
    iface = str(active_iface).lower()
    if iface in ("wwan0", "usb0", "ppp0", "eth1", "rmnet0", "rmnet_data0"):
        return "4G"
    if iface.startswith("wlan") or iface.startswith("wl"):
        return "WiFi"
    return iface[:6]


def _load_vpn_ip(install_dir: str) -> str:
    for rel in ("vpn_config.json", "data/vpn_config.json"):
        data = _load_json(Path(install_dir) / rel)
        if not data:
            continue
        ip = str(data.get("assigned_ip") or "").strip()
        if ip:
            return ip.split("/")[0]
    return ""


def collect_cm5(*, boot_epoch: float | None = None) -> MonitorState:
    """Gom metrics phần cứng CM5 (CPU/RAM/temp). Mạng/sóng → network_status.json."""
    used, total = ram_info()
    up = uptime_seconds()
    return MonitorState(
        uptime_seconds=up,
        boot_epoch=boot_epoch if boot_epoch is not None else time.time() - up,
        cpu_percent=cpu_percent(),
        ram_used_gb=used,
        ram_total_gb=total,
        core_temp_c=core_temp_c(),
    )


def _apply_cloud_signal(state: MonitorState, net: dict) -> None:
    """Sóng hiển thị = cloud uplink (netmon đo). LCD không gọi qmicli/iw."""
    dbm = net.get("cloud_signal_dbm")
    source = net.get("cloud_signal_source")
    if dbm is not None:
        try:
            state.signal_dbm = int(dbm)
            state.signal_valid = True
            state.signal_bars = signal_bars_from_dbm(state.signal_dbm)
        except (TypeError, ValueError):
            state.signal_valid = False
            state.signal_bars = 0
    else:
        state.signal_valid = False
        state.signal_bars = 0
    if source in ("4g", "wifi"):
        state.signal_source = str(source)


def _merge_network_status(state: MonitorState) -> None:
    """Đọc network_status.json — single source of truth (INTERFACE_POLICY.md)."""
    net = _load_json(NETWORK_STATUS)
    if not net:
        return

    g4 = net.get("4g") or {}
    wifi = net.get("wifi") or {}
    if g4.get("ip"):
        state.lte_ip = str(g4["ip"])
    if wifi.get("ip"):
        state.wifi_ip = str(wifi["ip"])

    active = net.get("active_interface")
    if active:
        state.vpn_via = _vpn_via_label(str(active))
    elif g4.get("online"):
        state.vpn_via = "4G"
    elif wifi.get("online"):
        state.vpn_via = "WiFi"

    _apply_cloud_signal(state, net)


def _merge_vpn_info(
    state: MonitorState,
    *,
    install_dir: str = DEFAULT_INSTALL_DIR,
    base: str | None = None,
) -> None:
    """VPN IP từ WireGuard config; nhãn 4G/WiFi từ network_status.json."""
    vpn_ip = _load_vpn_ip(install_dir)
    if not vpn_ip and base:
        status = _http_json(f"{base}/api/status")
        if status:
            ip = str(status.get("current_ip") or "").strip().split("/")[0]
            if ip.startswith("10.8."):
                vpn_ip = ip
    state.vpn_ip = vpn_ip[:15] if vpn_ip else ""
    if state.vpn_ip:
        state.server_id = state.vpn_ip

    if state.vpn_via:
        return

    net = _load_json(NETWORK_STATUS)
    if not net and base:
        net = _http_json(f"{base}/api/network/status")
    if not net:
        return

    active = net.get("active_interface")
    if active:
        state.vpn_via = _vpn_via_label(str(active))
        return

    g4 = net.get("4g") or {}
    wifi = net.get("wifi") or {}
    if g4.get("online"):
        state.vpn_via = "4G"
    elif wifi.get("online"):
        state.vpn_via = "WiFi"


def _merge_dronebridge_api(
    state: MonitorState,
    *,
    install_dir: str = DEFAULT_INSTALL_DIR,
    web_port: int = 8080,
) -> None:
    cfg = _load_dronebridge_config(install_dir)
    auth_cfg = cfg.get("auth") or {}
    web_cfg = cfg.get("web") or {}
    port = int(web_cfg.get("port") or web_port)
    base = f"http://127.0.0.1:{port}"

    uuid = str(auth_cfg.get("uuid") or "").strip()
    if uuid:
        state.drone_name = uuid.split("-")[0][:12].upper()

    host = str(auth_cfg.get("host") or "")
    auth_port = auth_cfg.get("port")
    if host and auth_port:
        state.server_host = f"{host}:{auth_port}"

    status = _http_json(f"{base}/api/status")
    if status:
        auth_st = str(status.get("auth_status") or "")
        state.server_connected = bool(status.get("server_reachable")) or auth_st == "Authenticated"

    _merge_vpn_info(state, install_dir=install_dir, base=base)

    conn = _http_json(f"{base}/api/connection")
    if not conn:
        state.flight_mode = "NO API"
        state.gps_lock = "NO DATA"
        state.pixhawk_connected = False
        state.px4_telemetry_valid = False
        return

    connected = bool(conn.get("Connected") or conn.get("connected"))
    state.pixhawk_connected = connected

    telem = _http_json(f"{base}/api/telemetry")
    if telem and telem.get("success"):
        valid = bool(telem.get("valid"))
        state.px4_telemetry_valid = connected and valid
        if state.px4_telemetry_valid:
            if telem.get("altitude_m") is not None:
                state.altitude_m = float(telem.get("altitude_m") or 0)
            if telem.get("speed_ms") is not None:
                state.speed_ms = float(telem.get("speed_ms") or 0)
            if telem.get("battery_pct") is not None:
                state.battery_pct = float(telem.get("battery_pct") or 0)
            if telem.get("climb_ms") is not None:
                state.vertical_speed_ms = float(telem.get("climb_ms") or 0)
            if telem.get("satellites") is not None:
                state.satellites = int(telem.get("satellites") or 0)
            if telem.get("flight_mode"):
                state.flight_mode = str(telem.get("flight_mode"))[:8]
            if telem.get("gps_lock"):
                state.gps_lock = str(telem.get("gps_lock"))[:10]
        else:
            state.altitude_m = 0.0
            state.speed_ms = 0.0
            state.battery_pct = 0.0
            state.vertical_speed_ms = 0.0
            state.satellites = 0
            state.flight_mode = "WAIT" if connected else "NO PX4"
            state.gps_lock = "WAIT" if connected else "NO PX4"
    else:
        state.px4_telemetry_valid = False
        state.altitude_m = 0.0
        state.speed_ms = 0.0
        state.battery_pct = 0.0
        state.vertical_speed_ms = 0.0
        state.satellites = 0
        state.flight_mode = "WAIT" if connected else "NO PX4"
        state.gps_lock = "WAIT" if connected else "NO PX4"


def collect_live(
    *,
    install_dir: str = DEFAULT_INSTALL_DIR,
    web_port: int = 8080,
    boot_epoch: float | None = None,
) -> MonitorState:
    """CM5 + mạng DroneBridge + trạng thái PX4/cloud (dữ liệu thật, không demo)."""
    up = uptime_seconds()
    boot = boot_epoch if boot_epoch is not None else time.time() - up
    state = collect_cm5(boot_epoch=boot)
    _merge_network_status(state)
    _merge_dronebridge_api(state, install_dir=install_dir, web_port=web_port)
    return state
