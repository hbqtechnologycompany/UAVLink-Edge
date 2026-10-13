from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from typing import Any


def _config_paths() -> list[str]:
    seen: set[str] = set()
    paths: list[str] = []
    for path in (
        os.getenv("DRONEBRIDGE_CONFIG", ""),
        "/opt/dronebridge/config.yaml",
        os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "config.yaml"),
        "config.yaml",
    ):
        if path and path not in seen and os.path.isfile(path):
            seen.add(path)
            paths.append(path)
    return paths


def _parse_yaml_section(section: str) -> dict[str, Any]:
    """Parse a top-level YAML section without requiring PyYAML."""
    data: dict[str, Any] = {}
    in_section = False
    section_indent = 0

    for path in _config_paths():
        try:
            with open(path, encoding="utf-8") as handle:
                for raw in handle:
                    line = raw.rstrip("\n")
                    if re.match(rf"^{section}:\s*$", line):
                        in_section = True
                        section_indent = len(line) - len(line.lstrip())
                        data = {}
                        continue
                    if not in_section:
                        continue
                    if line and not line[0].isspace():
                        if data:
                            return data
                        in_section = False
                        continue
                    indent = len(line) - len(line.lstrip())
                    if indent <= section_indent:
                        if data:
                            return data
                        in_section = False
                        continue
                    key_match = re.match(r"^\s+(\w+):\s*(.*)$", line)
                    if key_match:
                        key, value = key_match.group(1), key_match.group(2).strip()
                        if value in ("", "|", ">"):
                            continue
                        value = value.split("#", 1)[0].strip()
                        value = value.strip('"').strip("'")
                        if value.lower() in ("true", "yes", "on"):
                            data[key] = True
                        elif value.lower() in ("false", "no", "off"):
                            data[key] = False
                        elif re.fullmatch(r"-?\d+", value):
                            data[key] = int(value)
                        else:
                            data[key] = value
        except OSError:
            continue
    return data


def _load_yaml_section(section: str) -> dict[str, Any]:
    for path in _config_paths():
        try:
            import yaml  # type: ignore

            with open(path, encoding="utf-8") as handle:
                doc = yaml.safe_load(handle) or {}
            block = doc.get(section)
            if isinstance(block, dict):
                return block
        except Exception:
            break
    return _parse_yaml_section(section)


@dataclass
class CellularConfig:
    enabled: bool = True
    driver: str = "sim7600_qmi"
    iface: str = "wwan0"
    apn: str = ""
    qmi_device: str = ""
    normalize_iface: bool = True
    sim7600: dict[str, Any] = field(default_factory=lambda: {"gpio_power": True})
    sim8262: dict[str, Any] = field(
        default_factory=lambda: {
            "parent_iface": "mhi_hwip0",
            "data_iface": "rmnet0",
            "rat_profile": "stability",
            "scripts_dir": "",
        }
    )

    @classmethod
    def from_dict(cls, data: dict[str, Any] | None) -> "CellularConfig":
        if not data:
            return cls()
        sim7600 = data.get("sim7600") if isinstance(data.get("sim7600"), dict) else {}
        sim8262 = data.get("sim8262") if isinstance(data.get("sim8262"), dict) else {}
        return cls(
            enabled=bool(data.get("enabled", True)),
            driver=str(data.get("driver", "sim7600_qmi")),
            iface=str(data.get("iface", "wwan0")),
            apn=str(data.get("apn", "") or ""),
            qmi_device=str(data.get("qmi_dev", "") or data.get("qmi_device", "") or ""),
            normalize_iface=bool(data.get("normalize_iface", True)),
            sim7600=sim7600 or {"gpio_power": True},
            sim8262=sim8262
            or {
                "parent_iface": "mhi_hwip0",
                "data_iface": "rmnet0",
                "rat_profile": "stability",
                "scripts_dir": "",
            },
        )

    def resolved_qmi_device(self) -> str:
        if self.qmi_device:
            return self.qmi_device
        if self.driver == "sim8262_pcie":
            return "/dev/wwan0qmi0"
        return "/dev/cdc-wdm0"

    def resolved_apn(self) -> str:
        if self.apn:
            return self.apn
        if self.driver == "sim8262_pcie":
            return "m-wap"
        return "v-internet"


def load_cellular_config() -> CellularConfig:
    env_driver = os.getenv("DRONEBRIDGE_CELLULAR_DRIVER")
    env_iface = os.getenv("DRONEBRIDGE_WWAN_IFACE")
    env_apn = os.getenv("DRONEBRIDGE_APN")
    cfg = CellularConfig.from_dict(_load_yaml_section("cellular"))
    if env_driver:
        cfg.driver = env_driver
    if env_iface:
        cfg.iface = env_iface
    if env_apn:
        cfg.apn = env_apn
    if not cfg.qmi_device:
        cfg.qmi_device = cfg.resolved_qmi_device()
    return cfg
