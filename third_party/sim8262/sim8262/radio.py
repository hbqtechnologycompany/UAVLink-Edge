"""Radio access technology (RAT) profiles for QMI modems."""

from __future__ import annotations

# Acquisition order strings for qmicli --nas-set-system-selection-preference
RAT_PROFILES: dict[str, str] = {
    # Drone / mobile robot: stable LTE first, 3G fallback, 5G optional last.
    "stability": "lte|umts|5gnr",
    "drone": "lte|umts|5gnr",
    "balanced": "lte|5gnr|umts",
    "max-speed": "5gnr|lte|umts",
    "legacy": "umts|lte",
}

DEFAULT_RAT_PROFILE = "stability"


def acquisition_order(profile: str | None = None) -> str:
    key = (profile or DEFAULT_RAT_PROFILE).lower()
    return RAT_PROFILES.get(key, RAT_PROFILES[DEFAULT_RAT_PROFILE])
