#!/usr/bin/env python3
"""Smoke test ArUco — fail build/startup if cv2.aruco.detectMarkers crashes or missing."""

from __future__ import annotations

import sys


def main() -> int:
    try:
        import cv2
        import numpy as np
    except ImportError as exc:
        print(f"FAIL: cannot import cv2/numpy: {exc}")
        return 1

    version = cv2.__version__
    print(f"opencv {version}")

    if not hasattr(cv2, "aruco"):
        print("FAIL: cv2.aruco missing — install opencv-contrib-python-headless")
        return 1

    dictionary = cv2.aruco.getPredefinedDictionary(cv2.aruco.DICT_4X4_50)
    gray = np.zeros((240, 320), dtype=np.uint8)

    if hasattr(cv2.aruco, "ArucoDetector"):
        detector = cv2.aruco.ArucoDetector(dictionary, cv2.aruco.DetectorParameters())
        corners, ids, _rejected = detector.detectMarkers(gray)
        api = "ArucoDetector"
    else:
        corners, ids, _rejected = cv2.aruco.detectMarkers(
            gray, dictionary, parameters=cv2.aruco.DetectorParameters()
        )
        api = "detectMarkers"

    n = len(corners) if corners is not None else 0
    print(f"OK aruco via {api} (markers={n}, ids={ids})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
