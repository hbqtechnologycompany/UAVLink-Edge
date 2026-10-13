from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any

from Module_Cellular.config import CellularConfig


@dataclass
class SignalMetrics:
    quality: str = "UNKNOWN"
    rssi: str = "N/A"
    rsrp: str = "N/A"
    snr: str = "N/A"
    signal_dbm: int | None = None
    radio_access: str = "UNKNOWN"  # LTE | NR | NR-NSA | GSM | WCDMA
    generation: int = 4

    def to_dict(self) -> dict[str, Any]:
        return {
            "quality": self.quality,
            "rssi": self.rssi,
            "rsrp": self.rsrp,
            "snr": self.snr,
            "signal_dbm": self.signal_dbm,
            "radio_access": self.radio_access,
            "generation": self.generation,
        }


class CellularDriver(ABC):
    """Hardware-specific modem init — one implementation per module family."""

    name: str = "base"
    label: str = "Base Cellular Driver"
    generation: int = 4

    @abstractmethod
    def connect(self, config: CellularConfig) -> int:
        """Bring up packet data. Return 0 on success."""

    def get_signal_metrics(self, config: CellularConfig) -> SignalMetrics:
        from Module_Cellular.cellular_signal import read_qmi_signal

        return read_qmi_signal(config.qmi_device)

    def set_rat_mode(self, config: CellularConfig, mode: str) -> dict[str, Any]:
        raise NotImplementedError(f"RAT control not implemented for {self.name}")

    def get_rat_mode(self, config: CellularConfig) -> dict[str, Any]:
        raise NotImplementedError(f"RAT query not implemented for {self.name}")
