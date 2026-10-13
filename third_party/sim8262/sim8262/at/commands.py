from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable


USB_MODES = {
    "9001": "QMI (factory default, AT on ttyUSB2)",
    "9003": "MBIM with AT ports",
    "9011": "RNDIS USB network sharing",
    "901e": "MBIM without default AT ports",
}


@dataclass
class ModemIdentity:
    manufacturer: str | None = None
    model: str | None = None
    revision: str | None = None
    imei: str | None = None


@dataclass
class SignalQuality:
    rssi: int | None = None
    ber: int | None = None
    raw: str = ""


@dataclass
class NetworkRegistration:
    technology: str
    stat: int | None
    raw: str


class Sim8262Commands:
    """High-level AT wrappers for SIM8262 / SIM83xx compatible commands."""

    def __init__(self, at_client):
        self.at = at_client

    def identity(self) -> ModemIdentity:
        info = ModemIdentity()
        try:
            info.manufacturer = self._extract(self.at.query("AT+CGMI"), default="SIMCom")
        except Exception:
            pass
        try:
            info.model = self._extract(self.at.query("AT+CGMM"))
        except Exception:
            pass
        try:
            info.revision = self._extract(self.at.query("AT+CGMR"))
        except Exception:
            pass
        try:
            info.imei = self._extract(self.at.query("AT+CGSN"))
        except Exception:
            pass
        return info

    def sim_status(self) -> str:
        return self.at.query("AT+CPIN?")

    def signal(self) -> SignalQuality:
        raw = self.at.query("AT+CSQ")
        parts = raw.replace("+CSQ:", "").strip().split(",")
        rssi = int(parts[0]) if parts and parts[0].isdigit() else None
        ber = int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else None
        return SignalQuality(rssi=rssi, ber=ber, raw=raw)

    def registration(self) -> list[NetworkRegistration]:
        results: list[NetworkRegistration] = []
        for tech, cmd in (
            ("LTE", "AT+CEREG?"),
            ("5G", "AT+C5GREG?"),
            ("GSM", "AT+CREG?"),
        ):
            try:
                raw = self.at.query(cmd)
                stat = self._parse_reg_stat(raw)
                results.append(NetworkRegistration(technology=tech, stat=stat, raw=raw))
            except Exception:
                continue
        return results

    def set_apn(self, apn: str, cid: int = 1) -> str:
        return self.at.query(f'AT+CGDCONT={cid},"IPV4V6","{apn}"')

    def set_usb_mode(self, pid: str, vid: str = "1E0E") -> str:
        return self.at.query(f"AT+CUSBCFG=USBID,{vid},{pid}")

    def set_pcie_ep_mode(self) -> Iterable[str]:
        """Switch module to PCIe endpoint mode (requires AT access)."""
        for cmd in ("AT+CCUART=1", "AT+CPCIEMODE=EP"):
            yield self.at.query(cmd)

    def radio_on(self) -> str:
        return self.at.query("AT+CFUN=1")

    def radio_off(self) -> str:
        return self.at.query("AT+CFUN=0")

    def reboot_modem(self) -> str:
        return self.at.query("AT+CFUN=1,1")

    @staticmethod
    def _extract(text: str, default: str | None = None) -> str | None:
        lines = [line.strip() for line in text.splitlines() if line.strip()]
        for line in lines:
            upper = line.upper()
            if upper in {"OK", "ERROR"}:
                continue
            if upper.startswith("+CME ERROR") or upper.startswith("+CMS ERROR"):
                continue
            return line
        return default

    @staticmethod
    def _parse_reg_stat(raw: str) -> int | None:
        if ":" not in raw:
            return None
        payload = raw.split(":", 1)[1].strip()
        fields = [field.strip() for field in payload.split(",")]
        if len(fields) >= 2 and fields[1].isdigit():
            return int(fields[1])
        if fields and fields[0].isdigit():
            return int(fields[0])
        return None
