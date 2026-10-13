from __future__ import annotations

import re
import subprocess

from Module_Cellular.base import SignalMetrics


def _run_qmicli(qmi_dev: str) -> str:
    try:
        result = subprocess.run(
            ["qmicli", "-d", qmi_dev, "--nas-get-signal-strength"],
            capture_output=True,
            text=True,
            timeout=8,
        )
        if result.returncode == 0:
            return result.stdout or ""
    except (OSError, subprocess.TimeoutExpired):
        pass
    return ""


def _quality_from_rsrp(rsrp: int | None, rssi: int | None) -> str:
    if rsrp is not None:
        if rsrp >= -90:
            return "STRONG"
        if rsrp >= -100:
            return "GOOD"
        if rsrp >= -110:
            return "FAIR"
        return "WEAK"
    if rssi is not None:
        if rssi >= -70:
            return "STRONG"
        if rssi >= -85:
            return "GOOD"
        if rssi >= -100:
            return "FAIR"
        return "WEAK"
    return "UNKNOWN"


def read_qmi_signal(qmi_dev: str) -> SignalMetrics:
    out = _run_qmicli(qmi_dev)
    if not out:
        return SignalMetrics()

    rssi_match = re.search(
        r"RSSI:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dBm'",
        out,
    )
    rsrp_match = re.search(
        r"RSRP:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dBm'",
        out,
    )
    snr_match = re.search(
        r"SNR:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dB'",
        out,
    )
    nr_rsrp_match = re.search(
        r"NR5G RSRP:\s*(?:\n\s*Network\s+'[^']+':\s*)?'(-?\d+(?:\.\d+)?)\s*dBm'",
        out,
    )

    rssi = int(float(rssi_match.group(1))) if rssi_match else None
    rsrp = int(float(rsrp_match.group(1))) if rsrp_match else None
    snr = float(snr_match.group(1)) if snr_match else None
    nr_rsrp = int(float(nr_rsrp_match.group(1))) if nr_rsrp_match else None

    radio_access = "LTE"
    generation = 4
    signal_dbm = rsrp if rsrp is not None else rssi
    if nr_rsrp is not None:
        radio_access = "NR"
        generation = 5
        signal_dbm = nr_rsrp
        rsrp = nr_rsrp
    elif rsrp is not None and rssi is not None and rsrp != rssi:
        radio_access = "LTE"

    quality = _quality_from_rsrp(rsrp, rssi)
    return SignalMetrics(
        quality=quality,
        rssi=f"{rssi}dBm" if rssi is not None else "N/A",
        rsrp=f"{rsrp}dBm" if rsrp is not None else "N/A",
        snr=f"{snr}dB" if snr is not None else "N/A",
        signal_dbm=signal_dbm,
        radio_access=radio_access,
        generation=generation,
    )
