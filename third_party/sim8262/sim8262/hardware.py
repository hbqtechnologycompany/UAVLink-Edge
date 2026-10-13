from __future__ import annotations

import glob
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path


PCI_MODEM_VENDOR = "17cb"
PCI_MODEM_DEVICE = "0308"
SIMCOM_USB_VID = "1e0e"


@dataclass
class HardwareStatus:
    interface: str
    pcie_detected: bool
    usb_detected: bool
    mhi_enabled: bool
    at_ports: list[str]
    wwan_ports: list[str]
    wdm_ports: list[str]
    kernel_modules: list[str]
    issues: list[str]
    recommendations: list[str]


def _run(cmd: list[str]) -> str:
    try:
        return subprocess.check_output(cmd, stderr=subprocess.STDOUT, text=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        return ""


def _kernel_has_mhi() -> bool:
    config_paths = [
        Path("/proc/config.gz"),
        Path(f"/boot/config-{subprocess.check_output(['uname', '-r'], text=True).strip()}"),
        Path("/boot/firmware/config.txt"),
    ]
    for path in config_paths:
        if not path.exists():
            continue
        try:
            if path.suffix == ".gz":
                import gzip

                text = gzip.open(path, "rt").read()
            else:
                text = path.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        if "CONFIG_MHI_BUS=y" in text or "CONFIG_MHI_BUS=m" in text:
            return True
        if "CONFIG_MHI_BUS is not set" in text:
            return False
    return bool(glob.glob("/lib/modules/*/kernel/drivers/bus/mhi/*.ko*"))


def _list_loaded_modules(prefixes: tuple[str, ...]) -> list[str]:
    text = _run(["lsmod"])
    modules: list[str] = []
    for line in text.splitlines():
        name = line.split()[0] if line.split() else ""
        if any(name.startswith(prefix) for prefix in prefixes):
            modules.append(name)
    return modules


def scan_hardware() -> HardwareStatus:
    issues: list[str] = []
    recommendations: list[str] = []

    lspci = _run(["lspci", "-nn"])
    pcie_detected = bool(re.search(rf"{PCI_MODEM_VENDOR}:{PCI_MODEM_DEVICE}", lspci, re.I))

    lsusb = _run(["lsusb"])
    usb_detected = SIMCOM_USB_VID.lower() in lsusb.lower()

    mhi_enabled = _kernel_has_mhi()
    if pcie_detected and not mhi_enabled:
        issues.append(
            "Module visible on PCIe (Qualcomm 17cb:0308) but CONFIG_MHI_BUS is disabled in kernel."
        )
        recommendations.append(
            "Run: sudo ./scripts/setup_pcie.sh && ./scripts/build_mhi_kernel.sh, then reboot."
        )
        recommendations.append(
            "After reboot expect: /dev/wwan0at0 (AT), /dev/wwan0mbim0 (data), interface wwan0."
        )

    at_ports = sorted(
        set(
            glob.glob("/dev/ttyUSB*")
            + glob.glob("/dev/wwan*at*")
            + glob.glob("/dev/ttyAMA*")
        )
    )
    wwan_ports = sorted(glob.glob("/sys/class/net/wwan*") + glob.glob("/sys/class/net/mhi_*"))
    wdm_ports = sorted(
        glob.glob("/dev/cdc-wdm*")
        + glob.glob("/dev/wwan*mbim*")
        + glob.glob("/dev/wwan*qmi*")
        + glob.glob("/dev/wwan*ctrl*")
    )

    if pcie_detected and mhi_enabled and wdm_ports and not glob.glob("/dev/wwan*at*"):
        recommendations.append(
            "PCIe mode uses MBIM/QMI control (/dev/wwan0mbim0). Use: sim8262 pcie-status"
        )

    if not at_ports and not wdm_ports:
        issues.append("No AT control port found (/dev/ttyUSB* or /dev/wwan*at*).")
        if pcie_detected:
            recommendations.append(
                "After MHI driver loads, expect /dev/wwan0at0 and /dev/wwan0mbim0."
            )
        elif not usb_detected:
            recommendations.append(
                "Check power, SIM card, and whether module is in USB or PCIe mode."
            )

    kernel_modules = _list_loaded_modules(("qmi_wwan", "cdc_mbim", "option", "mhi", "wwan"))

    if pcie_detected:
        interface = "pcie"
    elif usb_detected:
        interface = "usb"
    else:
        interface = "unknown"
        issues.append("SIM8262 not detected on USB or PCIe.")

    return HardwareStatus(
        interface=interface,
        pcie_detected=pcie_detected,
        usb_detected=usb_detected,
        mhi_enabled=mhi_enabled,
        at_ports=at_ports,
        wwan_ports=wwan_ports,
        wdm_ports=wdm_ports,
        kernel_modules=kernel_modules,
        issues=issues,
        recommendations=recommendations,
    )
