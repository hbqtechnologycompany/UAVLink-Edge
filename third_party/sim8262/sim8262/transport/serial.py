from __future__ import annotations

import glob
import time

import serial

from sim8262.exceptions import TransportError
from sim8262.transport.base import Transport


class SerialTransport(Transport):
    """Serial transport for /dev/ttyUSB* or /dev/ttyAMA* AT ports."""

    def __init__(self, port: str, baudrate: int = 115200, default_timeout: float = 5.0):
        self.port = port
        self.baudrate = baudrate
        self.default_timeout = default_timeout
        self._ser: serial.Serial | None = None

    @property
    def name(self) -> str:
        return self.port

    def open(self) -> None:
        try:
            self._ser = serial.Serial(
                port=self.port,
                baudrate=self.baudrate,
                timeout=self.default_timeout,
                write_timeout=self.default_timeout,
            )
        except serial.SerialException as exc:
            raise TransportError(f"Cannot open serial port {self.port}: {exc}") from exc

    def close(self) -> None:
        if self._ser and self._ser.is_open:
            self._ser.close()
        self._ser = None

    def write(self, data: bytes) -> None:
        if not self._ser or not self._ser.is_open:
            raise TransportError("Serial port is not open")
        try:
            self._ser.write(data)
            self._ser.flush()
        except serial.SerialException as exc:
            raise TransportError(f"Write failed on {self.port}: {exc}") from exc

    def read_until(self, marker: bytes, timeout: float | None = None) -> bytes:
        if not self._ser or not self._ser.is_open:
            raise TransportError("Serial port is not open")

        deadline = time.monotonic() + (timeout if timeout is not None else self.default_timeout)
        buf = bytearray()
        while time.monotonic() < deadline:
            chunk = self._ser.read(max(1, self._ser.in_waiting or 1))
            if chunk:
                buf.extend(chunk)
                if marker in buf:
                    return bytes(buf)
            else:
                time.sleep(0.01)
        return bytes(buf)

    def flush_input(self) -> None:
        if self._ser and self._ser.is_open:
            self._ser.reset_input_buffer()


def probe_at_port(port: str, baudrate: int = 115200, timeout: float = 1.0) -> bool:
    """Return True if port responds OK to a basic AT probe."""
    transport = SerialTransport(port, baudrate=baudrate, default_timeout=timeout)
    try:
        transport.open()
        transport.flush_input()
        transport.write(b"AT\r\n")
        response = transport.read_until(b"OK", timeout=timeout)
        return b"OK" in response
    except TransportError:
        return False
    finally:
        transport.close()


def discover_at_ports(prefer_pcie: bool = True) -> list[str]:
    """Find candidate AT ports in priority order."""
    pcie_ports = sorted(glob.glob("/dev/wwan*at*")) + sorted(glob.glob("/dev/wwan*ctrl*"))
    usb_ports = [
        "/dev/ttyUSB2",
        "/dev/ttyUSB3",
    ]
    patterns = (
        (pcie_ports + usb_ports + ["/dev/ttyUSB*", "/dev/ttyAMA*", "/dev/serial*"])
        if prefer_pcie
        else (usb_ports + pcie_ports + ["/dev/ttyUSB*", "/dev/ttyAMA*", "/dev/serial*"])
    )
    seen: set[str] = set()
    candidates: list[str] = []
    for pattern in patterns:
        paths = [pattern] if not pattern.endswith("*") else sorted(glob.glob(pattern))
        for path in paths:
            if path not in seen:
                seen.add(path)
                candidates.append(path)
    return candidates
