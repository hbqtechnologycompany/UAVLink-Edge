from __future__ import annotations

import re
from dataclasses import dataclass


@dataclass
class AtResponse:
    command: str
    lines: list[str]
    success: bool
    raw: str

    @property
    def text(self) -> str:
        return "\n".join(self.lines)


class AtParser:
    """Parse modem text responses into structured AT results."""

    _FINAL_RE = re.compile(r"^(OK|ERROR|\+CME ERROR:|\+CMS ERROR:)", re.I)

    @classmethod
    def parse(cls, command: str, raw: str) -> AtResponse:
        lines = [line.strip() for line in raw.replace("\r", "").split("\n") if line.strip()]
        success = any(line.upper() == "OK" for line in lines)
        if any(line.upper().startswith("ERROR") for line in lines):
            success = False
        if any("+CME ERROR" in line.upper() or "+CMS ERROR" in line.upper() for line in lines):
            success = False
        payload = [line for line in lines if not cls._FINAL_RE.match(line)]
        return AtResponse(command=command, lines=payload, success=success, raw=raw)
