from __future__ import annotations

from abc import ABC, abstractmethod


class Transport(ABC):
    """Low-level byte transport to the modem AT/control port."""

    @abstractmethod
    def open(self) -> None:
        raise NotImplementedError

    @abstractmethod
    def close(self) -> None:
        raise NotImplementedError

    @abstractmethod
    def write(self, data: bytes) -> None:
        raise NotImplementedError

    @abstractmethod
    def read_until(self, marker: bytes, timeout: float | None = None) -> bytes:
        raise NotImplementedError

    @abstractmethod
    def flush_input(self) -> None:
        raise NotImplementedError

    @property
    @abstractmethod
    def name(self) -> str:
        raise NotImplementedError

    def __enter__(self) -> "Transport":
        self.open()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()
