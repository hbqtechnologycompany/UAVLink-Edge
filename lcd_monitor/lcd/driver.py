"""SH1107 128×128 OLED qua I2C (smbus2).

  dev = open_display(bus=3, address=0x3C)
  display_image(dev, img)          # PIL Image bất kỳ, tự chuyển 1-bit 128×128
  dev.cleanup()

Bus: CM5 /dev/i2c-3 (GPIO22/23), WalnutPi CM2 /dev/i2c-4 (J23 chân 6/8).
"""

from __future__ import annotations

from pathlib import Path

from PIL import Image
from smbus2 import SMBus, i2c_msg

DRIVER_ID = "sh1107-i2c-smbus2"

WIDTH = HEIGHT = 128
PAGES = HEIGHT // 8

_CMD = 0x00
_DATA = 0x40


class SH1107:
    """Page addressing: mỗi page là 8 dòng, mỗi byte là một cột 8 pixel (bit 0 ở trên)."""

    DISPLAY_OFFSET = 0x00
    # Nhiều adapter I2C không nhận một message dài; chia page thành từng khúc.
    CHUNK = 32

    def __init__(self, bus: int, address: int = 0x3C, contrast: int = 0x6F, page_mode: bool = False):
        self.address = address
        self.page_mode = page_mode
        self._bus = SMBus(bus)
        self._last: list[bytes] | None = None
        self._init(contrast)

    def _cmd(self, *codes: int) -> None:
        self._bus.i2c_rdwr(i2c_msg.write(self.address, bytes([_CMD, *codes])))

    def _data(self, payload: bytes) -> None:
        step = self.CHUNK if self.page_mode else len(payload)
        for i in range(0, len(payload), step):
            self._bus.i2c_rdwr(i2c_msg.write(self.address, bytes([_DATA]) + payload[i : i + step]))

    def _init(self, contrast: int) -> None:
        self._cmd(0xAE)                       # display off
        self._cmd(0xDC, 0x00)                 # start line 0
        self._cmd(0x81, contrast & 0xFF)      # contrast
        self._cmd(0x20)                       # page addressing
        self._cmd(0xA0)                       # segment remap normal
        self._cmd(0xC0)                       # COM scan normal
        self._cmd(0xA8, 0x7F)                 # multiplex 128
        self._cmd(0xD3, self.DISPLAY_OFFSET)  # display offset
        self._cmd(0xD5, 0x51)                 # clock divide / oscillator
        self._cmd(0xD9, 0x22)                 # pre-charge
        self._cmd(0xDB, 0x35)                 # VCOMH
        self._cmd(0xAD, 0x8A)                 # DC-DC on
        self._cmd(0xA4, 0xA6)                 # RAM content, normal (not inverted)
        self.clear()
        self._cmd(0xAF)                       # display on

    def clear(self) -> None:
        self.show_pages([bytes(WIDTH)] * PAGES)

    def show_pages(self, pages: list[bytes]) -> None:
        for p, data in enumerate(pages):
            if self._last is not None and self._last[p] == data:
                continue
            self._cmd(0xB0 | p, 0x00, 0x10)   # page p, column 0
            self._data(data)
        self._last = list(pages)

    def display(self, img: Image.Image) -> None:
        self.show_pages(_to_pages(img))

    def cleanup(self) -> None:
        try:
            self._cmd(0xAE)
        except OSError:
            pass
        self._bus.close()


class AdafruitSH1107(SH1107):
    """Module kiểu Adafruit: RAM lệch 0x60 dòng so với panel."""

    DISPLAY_OFFSET = 0x60


def prepare_image(img: Image.Image, rotate: int = 0) -> Image.Image:
    if img.size != (WIDTH, HEIGHT):
        img = img.resize((WIDTH, HEIGHT))
    if rotate:
        img = img.rotate(-90 * (rotate % 4), expand=False)
    return img.convert("1")


def _to_pages(img: Image.Image) -> list[bytes]:
    px = img.load()
    pages = []
    for p in range(PAGES):
        row = bytearray(WIDTH)
        y0 = p * 8
        for x in range(WIDTH):
            b = 0
            for bit in range(8):
                if px[x, y0 + bit]:
                    b |= 1 << bit
            row[x] = b
        pages.append(bytes(row))
    return pages


_BOARD_BUS = {"cm5": 3, "walnutpi": 4}


def board_name(forced: str | None = None) -> str:
    """Khớp config/board.go DetectBoard: khóa `board:` trong config thắng, rồi device-tree."""
    forced = (forced or "").strip().lower()
    if forced in _BOARD_BUS:
        return forced
    try:
        model = Path("/proc/device-tree/model").read_bytes().decode(errors="ignore").lower()
    except OSError:
        model = ""
    return "walnutpi" if "walnutpi" in model else "cm5"


def default_bus(board: str | None = None) -> int:
    """CM5 I2C3 (GPIO22/23), WalnutPi CM2 TWI4 (J23 chân 6/8)."""
    return _BOARD_BUS[board_name(board)]


def resolve_bus(raw, board: str | None = None) -> int:
    """Như applyBoardProfile: rỗng/0 hoặc bus mặc định của board kia → bus board này."""
    name = board_name(board)
    try:
        bus = int(raw or 0)
    except (TypeError, ValueError):
        bus = 0
    others = {b for n, b in _BOARD_BUS.items() if n != name}
    if bus <= 0 or bus in others:
        return _BOARD_BUS[name]
    return bus


def open_display(bus: int, address: int = 0x3C, contrast: int = 0x6F, page_mode: bool = False) -> SH1107:
    return SH1107(bus, address=address, contrast=contrast, page_mode=page_mode)


def display_image(dev: SH1107, img: Image.Image, rotate: int = 0) -> None:
    dev.display(prepare_image(img, rotate))
