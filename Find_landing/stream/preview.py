"""Ảnh xem thử lấy từ khung hình camera đang đẩy, không mở thêm luồng RTSP."""

from __future__ import annotations

import os
import time

_last = {}


def write_preview(camera_id: int, frame, ui_format: str = "BGR888") -> None:
    now = time.monotonic()
    if now - _last.get(camera_id, 0.0) < 0.1:
        return
    if frame is None:
        return
    try:
        import cv2
        import numpy as np

        img = np.ascontiguousarray(frame)
        if img.ndim == 3 and img.shape[2] == 4:
            img = np.ascontiguousarray(img[:, :, :3])
        if str(ui_format).upper().startswith("RGB") and img.ndim == 3 and img.shape[2] >= 3:
            img = np.ascontiguousarray(img[:, :, :3][:, :, ::-1])
        ok, encoded = cv2.imencode(".jpg", img, [int(cv2.IMWRITE_JPEG_QUALITY), 60])
        if not ok:
            _last[camera_id] = now
            return
        path = f"/tmp/dronebridge_preview_{int(camera_id)}.jpg"
        tmp = path + ".part"
        with open(tmp, "wb") as fh:
            fh.write(encoded.tobytes())
        os.replace(tmp, path)
        _last[camera_id] = now
    except Exception as exc:
        _last[camera_id] = now
        print(f" preview write failed cam{camera_id}: {exc}", flush=True)
