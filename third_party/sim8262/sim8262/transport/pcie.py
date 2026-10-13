from __future__ import annotations

import glob

from sim8262.hardware import scan_hardware
from sim8262.transport.serial import SerialTransport, probe_at_port


def discover_pcie_at_ports() -> list[str]:
    """Return MHI/WWAN AT control ports exposed over PCIe."""
    ports = sorted(glob.glob("/dev/wwan*at*"))
    if not ports:
        ports = sorted(glob.glob("/dev/wwan*ctrl*"))
    return ports


def open_pcie_at_transport(
    port: str | None = None,
    baudrate: int = 115200,
    timeout: float = 5.0,
) -> tuple[SerialTransport, str]:
    """
    Open the PCIe/MHI AT control port.

    After MHI driver loads, the modem exposes `/dev/wwan0at0`.
    The device behaves like a serial port for AT command I/O.
    """
    status = scan_hardware()
    if not status.pcie_detected:
        raise RuntimeError("Qualcomm PCIe modem (17cb:0308) not detected")

    candidates = [port] if port else discover_pcie_at_ports()
    for candidate in candidates:
        if not candidate:
            continue
        if probe_at_port(candidate, baudrate=baudrate, timeout=timeout):
            transport = SerialTransport(candidate, baudrate=baudrate, default_timeout=timeout)
            transport.open()
            return transport, candidate

    raise RuntimeError(
        "No PCIe AT port found. Expected /dev/wwan0at0 after MHI driver loads. "
        "Run scripts/build_mhi_kernel.sh and reboot."
    )
