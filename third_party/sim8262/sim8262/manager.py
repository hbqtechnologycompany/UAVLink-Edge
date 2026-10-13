from __future__ import annotations

import os
import re
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

from sim8262.carriers import DEFAULT_APN, apn_for_plmn
from sim8262.config import Sim8262Config
from sim8262.hardware import scan_hardware


@dataclass
class ConnectionResult:
    method: str
    success: bool
    detail: str


class ConnectionManager:
    """Bring up packet data using system tools when kernel data path exists."""

    def __init__(self, config: Sim8262Config | None = None):
        self.config = config or Sim8262Config()

    def detect_method(self) -> str:
        status = scan_hardware()
        # PCIe SDX65: QMI + mhi_hwip0 is the working data path (not MBIM alone).
        if status.interface == "pcie" and status.wdm_ports and shutil.which("qmicli"):
            if any("qmi" in path for path in status.wdm_ports):
                return "qmi"
        if status.wwan_ports and shutil.which("mmcli"):
            return "modemmanager"
        if status.wdm_ports:
            if any("mbim" in path for path in status.wdm_ports) and shutil.which("mbimcli"):
                return "mbim"
            if shutil.which("qmicli"):
                return "qmi"
        return "none"

    def connect(self, apn: str | None = None) -> ConnectionResult:
        method = self.detect_method()
        apn_value = apn or self._resolve_apn()

        if method == "modemmanager":
            return self._connect_modemmanager(apn_value)
        if method == "qmi":
            return self._connect_qmi(apn_value)
        if method == "mbim":
            return self._connect_mbim(apn_value)

        status = scan_hardware()
        detail = "; ".join(status.recommendations) or "No WWAN/MBIM/QMI interface available"
        return ConnectionResult(method="none", success=False, detail=detail)

    def _connect_modemmanager(self, apn: str) -> ConnectionResult:
        listing = self._run(["mmcli", "-L"])
        if "No modems were found" in listing:
            return ConnectionResult("modemmanager", False, listing.strip() or "No modems found")

        modem_path = ""
        for line in listing.splitlines():
            if "/Modem/" in line:
                modem_path = line.split("/Modem/")[-1].split()[0]
                modem_path = f"/org/freedesktop/ModemManager1/Modem/{modem_path}"
                break
        if not modem_path:
            return ConnectionResult("modemmanager", False, "Cannot parse modem path from mmcli -L")

        self._run(["mmcli", "-m", modem_path, f"--simple-connect=apn={apn}"])
        status = self._run(["mmcli", "-m", modem_path])
        success = "connected: yes" in status.lower() or "state: connected" in status.lower()
        return ConnectionResult("modemmanager", success, status.strip())

    def _connect_mbim(self, apn: str) -> ConnectionResult:
        script = Path(__file__).resolve().parents[1] / "scripts" / "mbim_connect.sh"
        if script.is_file():
            output = self._run(["bash", str(script), "/dev/wwan0mbim0", apn, "ipv4"])
            success = "Successfully connected" in output and "Activation state: 'activated'" in output
            return ConnectionResult("mbim", success, output.strip())

        ports = [p for p in scan_hardware().wdm_ports if "mbim" in p]
        device = ports[0] if ports else scan_hardware().wdm_ports[0]
        self._run(["mbimcli", "-d", device, "--set-radio-state=on"])
        self._run(["mbimcli", "-d", device, "--attach-packet-service"])
        output = self._run(
            ["mbimcli", "-d", device, f"--connect=access-string={apn},ip-type=ipv4"]
        )
        success = "ActivationState: activated" in output or "activated" in output.lower()
        return ConnectionResult("mbim", success, output.strip())

    def _resolve_apn(self) -> str:
        if self.config.apn and self.config.apn != "internet":
            return self.config.apn
        plmn = self._detect_plmn()
        return apn_for_plmn(plmn) or self.config.apn or DEFAULT_APN

    def _detect_plmn(self) -> str | None:
        for dev in ("/dev/wwan0mbim0", "/dev/wwan0qmi0"):
            if not Path(dev).exists():
                continue
            if "mbim" in dev and shutil.which("mbimcli"):
                out = self._run(["mbimcli", "-d", dev, "--query-registration-state"])
                match = re.search(r"Provider ID:\s*'(\d+)'", out)
                if match:
                    return match.group(1)
        return None

    def _connect_qmi(self, apn: str) -> ConnectionResult:
        script = Path(__file__).resolve().parents[1] / "scripts" / "qmi_connect.sh"
        if script.is_file():
            env = {**os.environ, "RAT_PROFILE": self.config.rat_profile}
            output = self._run_env(
                ["bash", str(script), "/dev/wwan0qmi0", apn, "mhi_hwip0"], env
            )
            success = "Internet OK" in output or (
                "0% packet loss" in output and "8.8.8.8" in output
            )
            return ConnectionResult("qmi", success, output.strip())

        device = next((p for p in scan_hardware().wdm_ports if "qmi" in p), "")
        if not device:
            return ConnectionResult("qmi", False, "No QMI device found")
        output = self._run(
            [
                "qmicli",
                "-d",
                device,
                "--device-open-proxy",
                "--wds-start-network",
                f"apn={apn}",
                "--client-no-release-cid",
            ]
        )
        success = "Network started" in output
        return ConnectionResult("qmi", success, output.strip())

    @staticmethod
    def _run(cmd: list[str]) -> str:
        return ConnectionManager._run_env(cmd, None)

    @staticmethod
    def _run_env(cmd: list[str], env: dict[str, str] | None) -> str:
        run_env = {**os.environ, **env} if env else None
        try:
            return subprocess.check_output(
                cmd, stderr=subprocess.STDOUT, text=True, env=run_env
            )
        except subprocess.CalledProcessError as exc:
            return exc.output or str(exc)
        except FileNotFoundError:
            return f"Command not found: {cmd[0]}"
