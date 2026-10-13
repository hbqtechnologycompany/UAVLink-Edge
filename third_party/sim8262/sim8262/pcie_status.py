from __future__ import annotations

import glob
import subprocess


def _mbim(device: str, *args: str) -> str:
    cmd = ["mbimcli", "-d", device, *args]
    try:
        return subprocess.check_output(cmd, stderr=subprocess.STDOUT, text=True, timeout=20)
    except subprocess.CalledProcessError as exc:
        return exc.output or str(exc)
    except subprocess.TimeoutExpired:
        return "timeout"


def pcie_status(device: str | None = None) -> dict:
    """Read modem status over PCIe/MBIM (no AT port required)."""
    if not device:
        ports = sorted(glob.glob("/dev/wwan*mbim*"))
        if not ports:
            raise RuntimeError("No /dev/wwan*mbim* port — is MHI loaded?")
        device = ports[0]

    return {
        "device": device,
        "caps": _mbim(device, "--query-device-caps"),
        "sim": _mbim(device, "--query-subscriber-ready-status"),
        "registration": _mbim(device, "--query-registration-state"),
        "signal": _mbim(device, "--query-signal-state"),
        "radio": _mbim(device, "--query-radio-state"),
        "pin": _mbim(device, "--query-pin-state"),
    }
