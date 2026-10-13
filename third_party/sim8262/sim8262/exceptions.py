class Sim8262Error(Exception):
    """Base error for SIM8262 driver."""


class TransportError(Sim8262Error):
    """Serial or control-port I/O failed."""


class AtCommandError(Sim8262Error):
    """Modem returned ERROR or unexpected response."""

    def __init__(self, command: str, response: str):
        self.command = command
        self.response = response
        super().__init__(f"AT failed: {command!r} -> {response!r}")


class TimeoutError(Sim8262Error):
    """Operation timed out waiting for modem response."""
