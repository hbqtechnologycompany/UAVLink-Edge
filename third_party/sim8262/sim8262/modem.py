from __future__ import annotations

from sim8262.at.client import AtClient
from sim8262.at.commands import Sim8262Commands
from sim8262.config import Sim8262Config
from sim8262.exceptions import Sim8262Error, TransportError
from sim8262.hardware import scan_hardware
from sim8262.transport.serial import SerialTransport, discover_at_ports, probe_at_port


class Sim8262Modem:
    """High-level modem API built on AT commands."""

    def __init__(self, config: Sim8262Config | None = None):
        self.config = config or Sim8262Config()
        self._transport: SerialTransport | None = None
        self._at: AtClient | None = None
        self._commands: Sim8262Commands | None = None

    @property
    def commands(self) -> Sim8262Commands:
        if not self._commands:
            raise Sim8262Error("Modem is not connected")
        return self._commands

    def connect(self, port: str | None = None) -> str:
        selected = port or self.config.at_port or self._auto_port()
        if not selected:
            status = scan_hardware()
            hint = "; ".join(status.recommendations) if status.recommendations else "No AT port found"
            raise TransportError(f"Cannot find AT port. {hint}")

        self._transport = SerialTransport(
            selected,
            baudrate=self.config.baudrate,
            default_timeout=self.config.timeout,
        )
        self._transport.open()
        self._at = AtClient(self._transport, default_timeout=self.config.timeout)
        if not self._at.test():
            self.close()
            raise TransportError(f"Port {selected} did not respond to AT")
        self._commands = Sim8262Commands(self._at)
        self.config.at_port = selected
        return selected

    def close(self) -> None:
        if self._transport:
            self._transport.close()
        self._transport = None
        self._at = None
        self._commands = None

    def status(self) -> dict:
        self.commands.radio_on()
        identity = self.commands.identity()
        signal = self.commands.signal()
        registration = self.commands.registration()
        return {
            "port": self.config.at_port,
            "identity": identity,
            "sim": self.commands.sim_status(),
            "signal": signal,
            "registration": registration,
        }

    def configure_usb_mode(self, pid: str | None = None) -> str:
        mode = pid or self.config.usb_mode
        return self.commands.set_usb_mode(mode)

    def configure_apn(self, apn: str | None = None) -> str:
        value = apn or self.config.apn
        return self.commands.set_apn(value)

    def _auto_port(self) -> str | None:
        prefer_pcie = self.config.prefer_interface != "usb"
        for candidate in discover_at_ports(prefer_pcie=prefer_pcie):
            if probe_at_port(candidate, baudrate=self.config.baudrate, timeout=1.0):
                return candidate
        return None

    def __enter__(self) -> "Sim8262Modem":
        self.connect()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()
