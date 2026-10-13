from __future__ import annotations

import time

from sim8262.at.parser import AtParser, AtResponse
from sim8262.exceptions import AtCommandError, TimeoutError, TransportError
from sim8262.transport.base import Transport


class AtClient:
    """Send AT commands and parse responses."""

    def __init__(self, transport: Transport, default_timeout: float = 5.0):
        self.transport = transport
        self.default_timeout = default_timeout

    def send(self, command: str, timeout: float | None = None, wait: float = 0.05) -> AtResponse:
        if not command.endswith("\r"):
            command = command.rstrip() + "\r"

        self.transport.flush_input()
        try:
            self.transport.write(command.encode("ascii", errors="ignore"))
        except TransportError:
            raise

        time.sleep(wait)
        raw_bytes = self.transport.read_until(b"OK", timeout=timeout or self.default_timeout)
        raw = raw_bytes.decode("ascii", errors="replace")

        if b"OK" not in raw_bytes and b"ERROR" not in raw_bytes:
            extra = self.transport.read_until(b"ERROR", timeout=0.5)
            raw += extra.decode("ascii", errors="replace")

        if not raw.strip():
            raise TimeoutError(f"No response for command: {command.strip()!r}")

        response = AtParser.parse(command.strip(), raw)
        if not response.success:
            raise AtCommandError(command.strip(), response.raw)
        return response

    def query(self, command: str, timeout: float | None = None) -> str:
        response = self.send(command, timeout=timeout)
        return response.text

    def test(self) -> bool:
        try:
            self.send("AT")
            return True
        except (AtCommandError, TimeoutError, TransportError):
            return False
