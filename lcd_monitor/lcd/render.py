"""Vẽ 2 khung dashboard 128×128 từ MonitorState."""

from __future__ import annotations

from PIL import Image, ImageDraw, ImageFont

from lcd.state import MonitorState


def _font():
    try:
        return ImageFont.load_default()
    except Exception:
        return None


def _uptime_short(seconds: int) -> str:
    seconds = max(0, int(seconds))
    days, rem = divmod(seconds, 86400)
    hours, rem = divmod(rem, 3600)
    minutes, _ = divmod(rem, 60)
    if days:
        return f"{days}d {hours:02d}h"
    if hours:
        return f"{hours}h {minutes:02d}m"
    return f"{minutes}m"


class _Canvas:
    LINE = 11

    def __init__(self, draw: ImageDraw.ImageDraw, font) -> None:
        self._d = draw
        self._font = font

    def _w(self, msg: str) -> int:
        if not msg:
            return 0
        b = self._d.textbbox((0, 0), msg, font=self._font)
        return b[2] - b[0]

    def _fit(self, msg: str, max_w: int) -> str:
        s = msg
        while len(s) > 1 and self._w(s) > max_w:
            s = s[:-1]
        return s

    def text(self, x: int, y: int, msg: str, *, fill: int = 255) -> None:
        self._d.text((x, y), msg, fill=fill, font=self._font)

    def text_right(self, x: int, y: int, msg: str, *, fill: int = 255) -> None:
        self.text(x - self._w(msg), y, msg, fill=fill)

    def text_center(self, x: int, y: int, w: int, msg: str, *, fill: int = 255) -> None:
        self.text(x + max(0, (w - self._w(self._fit(msg, w))) // 2), y, self._fit(msg, w), fill=fill)

    def hline(self, x0: int, y: int, x1: int) -> None:
        self._d.line([(x0, y), (x1, y)], fill=255)

    def dashed(self, x0: int, y: int, x1: int) -> None:
        x = x0
        while x <= x1:
            self._d.line([(x, y), (min(x + 4, x1), y)], fill=255)
            x += 6

    def rect(self, x0: int, y0: int, x1: int, y1: int) -> None:
        self._d.rectangle([x0, y0, x1, y1], outline=255)

    def section(self, y: int, title: str, w: int = 128) -> int:
        self.text(2, y, title)
        self.hline(0, y + 11, w - 1)
        return y + 13

    def grid_cells(
        self,
        x: int,
        y: int,
        cells: list[tuple[str, str]],
        *,
        cols: int = 2,
        cw: int = 61,
        ch: int = 18,
        gap: int = 4,
    ) -> int:
        compact = ch <= 19
        for i, (label, value) in enumerate(cells):
            col = i % cols
            row = i // cols
            cx = x + col * (cw + gap)
            cy = y + row * (ch + gap)
            self.metric_box(cx, cy, cw, ch, label, value, compact=compact)
        rows = (len(cells) + cols - 1) // cols
        return y + rows * ch + (rows - 1) * gap

    def metric_box(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        label: str,
        value: str,
        *,
        compact: bool = False,
    ) -> None:
        self.rect(x, y, x + w - 1, y + h - 1)
        if compact or h <= 19:
            self.text_center(x, y + 1, w, label)
            self.text_center(x, y + 9, w, value)
        else:
            self.text_center(x, y + 3, w, label)
            self.text_center(x, y + 3 + self.LINE, w, value)

    def signal_bars(self, x: int, y: int, level: int) -> None:
        level = max(0, min(5, level))
        bx = x
        for i in range(5):
            bh = 3 + i
            top = y + 8 - bh
            if i < level:
                self._d.rectangle([bx, top, bx + 2, y + 8], fill=255)
            else:
                self.rect(bx, top, bx + 2, y + 8)
            bx += 4


class CM5SystemScreen:
    W = H = 128
    NET_LINE = 10

    def render(self, state: MonitorState) -> Image.Image:
        img = Image.new("1", (self.W, self.H), 0)
        c = _Canvas(ImageDraw.Draw(img), _font())
        state.refresh_clock()
        W = self.W
        nl = self.NET_LINE

        c.text(2, 1, state.system_time[:8])
        c.text_right(W - 2, 1, f"Up {_uptime_short(state.uptime_seconds)}")
        c.hline(0, 13, W - 1)

        y = c.section(16, "Network")
        wifi = state.wifi_ip if state.wifi_ip and state.wifi_ip != "0.0.0.0" else "---"
        lte = state.lte_ip if state.lte_ip else "---"
        vpn = state.vpn_ip if state.vpn_ip else "---"
        via = f"({state.vpn_via})" if state.vpn_via else ""
        c.text(4, y, f"WiFi {wifi}"[:18])
        c.text(4, y + nl, f"4G   {lte}"[:18])
        c.text(4, y + nl * 2, f"VPN  {vpn}"[:14])
        if via:
            c.text_right(W - 2, y + nl * 2, via)
        sig_label = state.signal_source.upper() if state.signal_source else "?"
        sig_txt = f"{state.signal_dbm} dBm" if state.signal_valid else "N/A"
        c.text(4, y + nl * 3, f"{sig_label} {sig_txt}"[:18])
        c.signal_bars(72, y + nl * 3, state.signal_bars)

        c.dashed(0, y + nl * 4 + 2, W - 1)

        y2 = c.section(y + nl * 4 + 4, "Performance")
        bw, bh, g = 39, 27, 4
        ram_label = f"{state.ram_used_gb:.1f}G"
        if state.ram_total_gb > 0:
            ram_label = f"{state.ram_percent}%"
        c.metric_box(2, y2, bw, bh, "CPU", f"{state.cpu_percent}%")
        c.metric_box(2 + bw + g, y2, bw, bh, "RAM", ram_label)
        temp = f"{state.core_temp_c} C" if state.core_temp_c > 0 else "---"
        c.metric_box(2 + (bw + g) * 2, y2, bw, bh, "Temp", temp)
        return img


class DroneScreen:
    W = H = 128

    def render(self, state: MonitorState) -> Image.Image:
        img = Image.new("1", (self.W, self.H), 0)
        c = _Canvas(ImageDraw.Draw(img), _font())
        state.refresh_clock()

        y = c.section(2, state.drone_name[:18] or "DRONEBRIDGE")
        live = state.pixhawk_connected and state.px4_telemetry_valid

        def _num(val: float, fmt: str, empty: str = "---") -> str:
            if not live:
                return empty
            return fmt.format(val)

        mode = state.flight_mode[:8] if live and state.flight_mode else ("WAIT" if state.pixhawk_connected else "NO PX4")
        gps = state.gps_lock[:8] if live and state.gps_lock else ("WAIT" if state.pixhawk_connected else "NO PX4")
        auth = "OK" if state.server_connected else "OFF"
        cells = [
            ("Altitude", _num(state.altitude_m, "{:.1f} m")),
            ("Speed", _num(state.speed_ms, "{:.1f} m/s")),
            ("Battery", _num(state.battery_pct, "{:.0f} %")),
            ("Climb", _num(state.vertical_speed_ms, "{:+.1f} m/s")),
            ("Mode", mode),
            ("GPS", gps),
            ("Cloud", auth),
            ("4G", (state.lte_ip or "---")[:8]),
        ]
        c.grid_cells(2, y + 2, cells, ch=26, gap=3)
        return img


# Alias tương thích
DroneServerScreen = DroneScreen
CM5MonitorScreen = CM5SystemScreen
