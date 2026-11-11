"""WalnutPi (Allwinner T527) CSI camera — vendor flow: sunxi-vin V4L2 NV12 + AWIspApi 3A.

Same sequence as WalnutPi's walnutpi_isp package: open /dev/videoN through OpenCV
as NV12, then start the Allwinner ISP thread (AE/AWB) in this process. Without the
ISP thread the VIN ISP runs untuned and frames come out green.
"""

import os
import threading
import time

import cv2
import numpy as np

_isp_lock = threading.Lock()
_isp_started = set()


def vin_video_name(index: int) -> str:
    try:
        with open(f"/sys/class/video4linux/video{index}/name") as f:
            return f.read().strip()
    except OSError:
        return ""


def is_vin_device(index: int) -> bool:
    return vin_video_name(index).startswith("vin_video")


def has_vin_devices() -> bool:
    base = "/sys/class/video4linux"
    try:
        names = os.listdir(base)
    except OSError:
        return False
    return any(n.startswith("video") and is_vin_device(int(n[5:])) for n in names if n[5:].isdigit())


def _start_isp(index: int) -> bool:
    """AWIspApi thread lives for the whole process; start it once per video node."""
    with _isp_lock:
        if index in _isp_started:
            return True
        try:
            import _aw_isp
        except ImportError:
            try:
                import walnutpi_isp  # noqa: F401  (puts _aw_isp on sys.path)
                import _aw_isp
            except ImportError as e:
                print(f"✗ WalnutPi ISP module missing (walnutpi.isp): {e}")
                return False
        if not _aw_isp.start(index):
            print(f"✗ AWIspApi start failed for video{index}")
            return False
        _isp_started.add(index)
        return True


class GrayWorldWB:
    """Residual white balance on top of the vendor AWB (no IMX219 tuning in libisp)."""

    def __init__(self, every: int = 10, alpha: float = 0.2, max_gain: float = 2.5):
        self.every = every
        self.alpha = alpha
        self.max_gain = max_gain
        self.gains = np.ones(3, dtype=np.float32)
        self.count = 0
        self.lut = self._build_lut()

    def _build_lut(self):
        x = np.arange(256, dtype=np.float32)
        return np.clip(x[None, :, None] * self.gains[None, None, :], 0, 255).astype(np.uint8)

    def apply(self, frame):
        if self.count % self.every == 0:
            means = frame[::8, ::8].reshape(-1, 3).mean(axis=0).astype(np.float32) + 1e-3
            target = np.clip(means.mean() / means, 1.0 / self.max_gain, self.max_gain)
            self.gains = (1 - self.alpha) * self.gains + self.alpha * target
            self.lut = self._build_lut()
        self.count += 1
        return cv2.LUT(frame, self.lut)


def open_vin_camera(index: int, width: int, height: int, fps: int = 30):
    """Returns (cap, wb) or (None, None). Frames from cap.read() are BGR."""
    if not is_vin_device(index):
        return None, None
    cap = cv2.VideoCapture(index, cv2.CAP_V4L2)
    if not cap.isOpened():
        print(f"✗ Cannot open /dev/video{index}")
        return None, None
    cap.set(cv2.CAP_PROP_FOURCC, cv2.VideoWriter.fourcc(*"NV12"))
    cap.set(cv2.CAP_PROP_FRAME_WIDTH, width)
    cap.set(cv2.CAP_PROP_FRAME_HEIGHT, height)
    cap.set(cv2.CAP_PROP_FPS, fps)
    if not _start_isp(index):
        cap.release()
        return None, None

    deadline = time.time() + 5
    while time.time() < deadline:
        ok, frame = cap.read()
        if ok and frame is not None:
            h, w = frame.shape[:2]
            print(f"✓ WalnutPi CSI camera on /dev/video{index} (vin + AWIspApi, {w}x{h} NV12→BGR)")
            wb = None if os.environ.get("DRONEBRIDGE_VIN_WB", "1") == "0" else GrayWorldWB()
            return cap, wb
        time.sleep(0.05)
    print(f"✗ /dev/video{index}: no frames from sensor")
    cap.release()
    return None, None
