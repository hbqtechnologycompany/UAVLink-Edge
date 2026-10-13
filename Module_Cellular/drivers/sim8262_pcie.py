from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

from Module_Cellular.base import CellularDriver, SignalMetrics
from Module_Cellular.config import CellularConfig
from Module_Cellular.modem_state import write_modem_state


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def _third_party_root() -> Path:
    return _repo_root() / "third_party" / "sim8262"


def _ensure_sim8262_importable() -> None:
    root = _third_party_root()
    if root.is_dir() and str(root) not in sys.path:
        sys.path.insert(0, str(root))


def _run(cmd: list[str], timeout: int = 60) -> tuple[int, str]:
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        output = (proc.stdout or "") + (proc.stderr or "")
        return proc.returncode, output.strip()
    except (OSError, subprocess.TimeoutExpired) as exc:
        return 1, str(exc)


def _get_iface_ipv4(iface: str) -> str | None:
    rc, out = _run(["ip", "-4", "addr", "show", iface], timeout=8)
    if rc != 0 or not out:
        return None
    match = re.search(r"inet (\d+\.\d+\.\d+\.\d+)", out)
    return match.group(1) if match else None


def _normalize_iface(src: str, target: str) -> bool:
    if src == target:
        return True
    if not os.path.exists(f"/sys/class/net/{src}"):
        return False
    if os.path.exists(f"/sys/class/net/{target}"):
        return True
    _run(["ip", "link", "set", src, "down"], timeout=5)
    rc, _ = _run(["ip", "link", "set", src, "name", target], timeout=5)
    return rc == 0 and os.path.exists(f"/sys/class/net/{target}")


def _protect_ssh_default_route() -> None:
    """Remove cellular default from main table — SSH must stay on wlan0."""
    _run(["ip", "route", "del", "default", "dev", "wwan0"], timeout=3)
    for iface in ("rmnet0",):
        _run(["ip", "route", "del", "default", "dev", iface], timeout=3)


def _bring_up_iface(iface: str, parent: str = "") -> bool:
    if parent:
        _run(["ip", "link", "set", parent, "up"], timeout=5)
    rc, _ = _run(["ip", "link", "set", iface, "up"], timeout=5)
    return rc == 0


def _reuse_existing_session(config: CellularConfig) -> int | None:
    """Reuse wwan0/rmnet left from prior boot when IP is still assigned."""
    for iface in (config.iface, str(config.sim8262.get("data_iface", "rmnet0"))):
        ip_addr = _get_iface_ipv4(iface)
        if not ip_addr:
            continue
        parent = str(config.sim8262.get("parent_iface", "mhi_hwip0"))
        _bring_up_iface(iface, parent)
        write_modem_state(
            ready=True,
            iface=iface if iface == config.iface or not config.normalize_iface else config.iface,
            driver="sim8262_pcie",
            ip=ip_addr,
            phase="active_reuse",
            qmi_dev=config.resolved_qmi_device(),
            generation=5,
            radio_access="NR",
            extra={"method": "reuse", "parent_iface": parent},
        )
        print(f"SIM8262 reuse existing {iface} ({ip_addr})")
        _protect_ssh_default_route()
        return 0
    return None


class Sim8262PcieDriver(CellularDriver):
    name = "sim8262_pcie"
    label = "SIMCom SIM8262-E (PCIe MHI + QMI)"
    generation = 5

    def _scripts_dir(self, config: CellularConfig) -> Path:
        custom = str(config.sim8262.get("scripts_dir", "") or "").strip()
        if custom:
            return Path(custom)
        installed = Path("/opt/dronebridge/third_party/sim8262/scripts")
        if installed.is_dir():
            return installed
        return _third_party_root() / "scripts"

    def connect(self, config: CellularConfig) -> int:
        _ensure_sim8262_importable()
        from sim8262.config import Sim8262Config
        from sim8262.manager import ConnectionManager

        data_iface = str(config.sim8262.get("data_iface", "rmnet0"))
        parent_iface = str(config.sim8262.get("parent_iface", "mhi_hwip0"))
        rat_profile = str(config.sim8262.get("rat_profile", "stability"))
        qmi_dev = config.resolved_qmi_device()
        apn = config.resolved_apn()

        sim_cfg = Sim8262Config(
            apn=apn,
            prefer_interface="pcie",
            rat_profile=rat_profile,
            extra={
                "qmi_dev": qmi_dev,
                "parent_iface": parent_iface,
                "data_iface": data_iface,
                "scripts_dir": str(self._scripts_dir(config)),
            },
        )

        write_modem_state(
            ready=False,
            iface=config.iface,
            driver=self.name,
            phase="connecting",
            qmi_dev=qmi_dev,
            generation=self.generation,
        )

        reused = _reuse_existing_session(config)
        if reused is not None:
            return reused

        result = ConnectionManager(sim_cfg).connect(apn=apn)
        if not result.success:
            write_modem_state(
                ready=False,
                iface=config.iface,
                driver=self.name,
                phase="connect_failed",
                qmi_dev=qmi_dev,
                generation=self.generation,
                extra={"detail": result.detail, "method": result.method},
            )
            print(result.detail)
            return 1

        active_iface = data_iface
        if config.normalize_iface and config.iface != data_iface:
            if _normalize_iface(data_iface, config.iface):
                active_iface = config.iface
            else:
                print(f"WARN: cannot rename {data_iface} -> {config.iface}; using {data_iface}")
                active_iface = data_iface

        ip_addr = _get_iface_ipv4(active_iface)
        if not ip_addr:
            write_modem_state(
                ready=False,
                iface=active_iface,
                driver=self.name,
                phase="no_ipv4",
                qmi_dev=qmi_dev,
                generation=self.generation,
            )
            return 1

        write_modem_state(
            ready=True,
            iface=active_iface,
            driver=self.name,
            ip=ip_addr,
            phase="active",
            qmi_dev=qmi_dev,
            generation=self.generation,
            radio_access="NR",
            extra={"method": result.method, "parent_iface": parent_iface},
        )
        print(f"SIM8262 active on {active_iface} ({ip_addr}) via {result.method}")
        _protect_ssh_default_route()
        return 0

    def get_signal_metrics(self, config: CellularConfig) -> SignalMetrics:
        metrics = super().get_signal_metrics(config)
        if metrics.signal_dbm is None:
            _ensure_sim8262_importable()
            try:
                from sim8262.modem import Sim8262Modem

                with Sim8262Modem() as modem:
                    status = modem.status()
                    signal = status.get("signal", {})
                    dbm = signal.get("rssi_dbm")
                    if dbm is not None:
                        metrics.signal_dbm = int(dbm)
                        metrics.rssi = f"{dbm}dBm"
                        metrics.quality = "GOOD" if dbm >= -85 else "FAIR"
            except Exception:
                pass
        return metrics

    def set_rat_mode(self, config: CellularConfig, mode: str) -> dict[str, Any]:
        script = self._scripts_dir(config) / "set_radio_preference.sh"
        if not script.is_file():
            return {"success": False, "error": f"missing script: {script}"}
        rc, out = _run(["bash", str(script), config.resolved_qmi_device(), mode], timeout=30)
        return {"success": rc == 0, "stdout": out}

    def get_rat_mode(self, config: CellularConfig) -> dict[str, Any]:
        return {
            "success": True,
            "profile": str(config.sim8262.get("rat_profile", "stability")),
            "driver": self.name,
        }
