from __future__ import annotations

import json
import os

MODEM_STATE_FILE = "/run/dronebridge/modem_net.json"


def write_modem_state(
    *,
    ready: bool,
    iface: str,
    driver: str,
    ip: str | None = None,
    phase: str = "",
    qmi_dev: str | None = None,
    generation: int = 4,
    radio_access: str = "UNKNOWN",
    extra: dict | None = None,
) -> None:
    os.makedirs(os.path.dirname(MODEM_STATE_FILE), exist_ok=True)
    payload = {
        "iface": iface,
        "ip": ip,
        "ready": ready,
        "phase": phase,
        "driver": driver,
        "generation": generation,
        "radio_access": radio_access,
        "qmi_dev": qmi_dev if qmi_dev and os.path.exists(qmi_dev) else None,
    }
    if extra:
        payload.update(extra)
    with open(MODEM_STATE_FILE, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2)


def read_modem_state() -> dict:
    try:
        with open(MODEM_STATE_FILE, encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError):
        return {}
