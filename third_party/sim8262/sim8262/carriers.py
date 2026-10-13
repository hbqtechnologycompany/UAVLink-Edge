"""Vietnam carrier APN defaults (MCC 452)."""

from __future__ import annotations

# PLMN (MCC+MNC) -> default internet APN
VIETNAM_APN: dict[str, str] = {
    "45201": "m-wap",        # MobiFone
    "45202": "m3-world",     # Vinaphone
    "45204": "v-internet",   # Viettel
    "45205": "internet",     # Vietnamobile
    "45207": "internet",     # Gmobile
    "45208": "internet",     # I-Telecom
    "45209": "internet",     # REDDI
}

DEFAULT_APN = "m-wap"


def apn_for_plmn(plmn: str | None) -> str | None:
    if not plmn:
        return None
    digits = "".join(c for c in plmn if c.isdigit())
    if len(digits) >= 5:
        return VIETNAM_APN.get(digits[:5])
    return None
