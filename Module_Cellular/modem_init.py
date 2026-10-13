#!/usr/bin/env python3
"""
Cellular modem init entry point — dispatches to configured driver.

Replaces direct calls to Module_4G/enable_4g_auto.py while keeping that file
as the SIM7600 implementation.
"""

from __future__ import annotations

import os
import sys


def _repo_root() -> str:
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def main() -> int:
    if os.geteuid() != 0:
        print("  ! Cần chạy với sudo/root để init modem")
        print("  → sudo python3 Module_Cellular/modem_init.py")
        return 1

    root = _repo_root()
    if root not in sys.path:
        sys.path.insert(0, root)

    from Module_Cellular.config import load_cellular_config
    from Module_Cellular.registry import get_driver_for_config

    config = load_cellular_config()
    if not config.enabled:
        print("cellular.enabled=false — bỏ qua modem init")
        return 0

    driver = get_driver_for_config(config)
    print("=" * 50)
    print(f"  CELLULAR INIT — {driver.label}")
    print(f"  driver={driver.name} iface={config.iface} apn={config.resolved_apn()}")
    print("=" * 50)

    return int(driver.connect(config) or 0)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("\n  ! Người dùng đã dừng script (Ctrl+C)")
        raise SystemExit(130)
