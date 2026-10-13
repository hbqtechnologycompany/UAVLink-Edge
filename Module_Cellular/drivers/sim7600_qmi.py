from __future__ import annotations

import importlib
import os
import sys
from typing import Any

from Module_Cellular.base import CellularDriver
from Module_Cellular.config import CellularConfig


class Sim7600QmiDriver(CellularDriver):
    name = "sim7600_qmi"
    label = "SIMCom SIM7600 (USB QMI)"
    generation = 4

    def connect(self, config: CellularConfig) -> int:
        os.environ.setdefault("DRONEBRIDGE_WWAN_IFACE", config.iface)
        if config.apn:
            os.environ["DRONEBRIDGE_APN"] = config.apn
        if config.qmi_device:
            os.environ["DRONEBRIDGE_QMI_DEV"] = config.qmi_device

        repo_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        if repo_root not in sys.path:
            sys.path.insert(0, repo_root)

        module = importlib.import_module("Module_4G.enable_4g_auto")
        return int(module.main() or 0)

    def set_rat_mode(self, config: CellularConfig, mode: str) -> dict[str, Any]:
        import subprocess

        script = os.path.join(
            os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
            "Module_4G",
            "set_4g_mode.py",
        )
        proc = subprocess.run(
            [sys.executable, script, "set", mode],
            capture_output=True,
            text=True,
            timeout=30,
        )
        return {
            "success": proc.returncode == 0,
            "stdout": proc.stdout.strip(),
            "stderr": proc.stderr.strip(),
        }

    def get_rat_mode(self, config: CellularConfig) -> dict[str, Any]:
        import subprocess

        script = os.path.join(
            os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
            "Module_4G",
            "set_4g_mode.py",
        )
        proc = subprocess.run(
            [sys.executable, script, "get"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        return {
            "success": proc.returncode == 0,
            "stdout": proc.stdout.strip(),
            "stderr": proc.stderr.strip(),
        }
