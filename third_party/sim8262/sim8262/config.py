from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import json


@dataclass
class Sim8262Config:
    """Runtime configuration for the SIM8262 driver."""

    at_port: str | None = None
    baudrate: int = 115200
    timeout: float = 5.0
    apn: str = "m-wap"
    usb_mode: str = "9001"  # QMI default from factory
    prefer_interface: str = "auto"  # auto | usb | pcie
    rat_profile: str = "stability"  # stability | balanced | max-speed | legacy
    extra: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_file(cls, path: str | Path) -> "Sim8262Config":
        data = json.loads(Path(path).read_text(encoding="utf-8"))
        known = {f.name for f in cls.__dataclass_fields__.values()}
        core = {k: v for k, v in data.items() if k in known and k != "extra"}
        extra = {k: v for k, v in data.items() if k not in known}
        if "extra" in data and isinstance(data["extra"], dict):
            extra.update(data["extra"])
        return cls(extra=extra, **core)

    def to_dict(self) -> dict[str, Any]:
        payload = {
            "at_port": self.at_port,
            "baudrate": self.baudrate,
            "timeout": self.timeout,
            "apn": self.apn,
            "usb_mode": self.usb_mode,
            "prefer_interface": self.prefer_interface,
            "rat_profile": self.rat_profile,
        }
        payload.update(self.extra)
        return payload
