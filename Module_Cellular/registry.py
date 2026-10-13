from __future__ import annotations

from Module_Cellular.base import CellularDriver
from Module_Cellular.config import CellularConfig
from Module_Cellular.drivers.sim7600_qmi import Sim7600QmiDriver
from Module_Cellular.drivers.sim8262_pcie import Sim8262PcieDriver

_DRIVERS: dict[str, CellularDriver] = {
    Sim7600QmiDriver.name: Sim7600QmiDriver(),
    Sim8262PcieDriver.name: Sim8262PcieDriver(),
}


def register_driver(driver: CellularDriver) -> None:
    _DRIVERS[driver.name] = driver


def list_drivers() -> list[str]:
    return sorted(_DRIVERS.keys())


def get_driver(name: str) -> CellularDriver:
    driver = _DRIVERS.get(name)
    if driver is None:
        known = ", ".join(list_drivers())
        raise KeyError(f"Unknown cellular driver '{name}'. Known: {known}")
    return driver


def get_driver_for_config(config: CellularConfig) -> CellularDriver:
    return get_driver(config.driver)
