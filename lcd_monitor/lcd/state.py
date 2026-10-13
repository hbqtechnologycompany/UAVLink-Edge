"""Biến hiển thị — cập nhật cục bộ rồi truyền vào render()."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime


@dataclass
class MonitorState:
    """Toàn bộ dữ liệu 2 khung CM5 + Drone — hệ thống khác chỉ sửa object này."""

    # CM5 / mạng — mặc định trung tính (không dùng số demo)
    system_time: str = ""
    uptime_seconds: int = 0
    wifi_ip: str = ""
    lte_ip: str = ""
    vpn_ip: str = ""
    vpn_via: str = ""
    signal_valid: bool = False
    signal_source: str = ""  # "4g" | "wifi" — cloud uplink từ netmon
    signal_dbm: int = 0
    signal_bars: int = 0
    cpu_percent: int = 0
    ram_used_gb: float = 0.0
    ram_total_gb: float = 0.0
    core_temp_c: int = 0

    # Drone / PX4 — chỉ hiển thị khi px4_telemetry_valid=True
    drone_name: str = "DRONEBRIDGE"
    altitude_m: float = 0.0
    speed_ms: float = 0.0
    battery_pct: float = 0.0
    satellites: int = 0
    vertical_speed_ms: float = 0.0
    flight_mode: str = ""
    drone_link_dbm: int = 0
    distance_home_m: float = 0.0
    gps_lock: str = ""
    pixhawk_connected: bool = False
    px4_telemetry_valid: bool = False

    # Server (API sau — không hiển thị trên LCD hiện tại)
    server_connected: bool = False
    server_id: str = ""
    server_host: str = ""
    server_latency_ms: int = 0
    link_quality_pct: float = 0.0
    tls_active: bool = False

    hardware_rev: str = ""
    boot_epoch: float = field(default_factory=lambda: datetime.now().timestamp())

    def __post_init__(self) -> None:
        if not self.system_time:
            self.system_time = datetime.now().strftime("%H:%M:%S")

    def refresh_clock(self) -> None:
        self.system_time = datetime.now().strftime("%H:%M:%S")
        if self.boot_epoch > 0:
            self.uptime_seconds = max(0, int(datetime.now().timestamp() - self.boot_epoch))

    @property
    def ram_percent(self) -> int:
        if self.ram_total_gb <= 0:
            return 0
        return min(100, int(self.ram_used_gb / self.ram_total_gb * 100))


def demo_state(tick: int, *, boot_epoch: float | None = None) -> MonitorState:
    """Dữ liệu mẫu cho demo."""
    return MonitorState(
        boot_epoch=boot_epoch if boot_epoch is not None else datetime.now().timestamp() - 95455,
        wifi_ip="192.168.1.104",
        lte_ip="10.42.0.18",
        signal_dbm=-64 - (tick % 4),
        signal_bars=4 + (tick % 2),
        cpu_percent=33 + (tick % 18),
        ram_used_gb=1.4 + (tick % 5) * 0.1,
        ram_total_gb=4.0,
        core_temp_c=47 + (tick % 8),
        altitude_m=24.2 + (tick % 10) * 0.2,
        speed_ms=0.1 + (tick % 6) * 0.3,
        battery_pct=77.0 - (tick % 20) * 0.5,
        satellites=12 + (tick % 5),
        vertical_speed_ms=0.1 + (tick % 4) * 0.15,
        flight_mode=("LOITER", "AUTO", "STABILIZE", "RTL")[tick % 4],
        drone_link_dbm=-68 - (tick % 6),
        distance_home_m=120.0 + (tick % 8) * 3.5,
        drone_name="CM5-Quad-V2",
        gps_lock="3D 12",
        pixhawk_connected=True,
        px4_telemetry_valid=True,
        server_connected=True,
    )
