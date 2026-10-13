#!/usr/bin/env python3
"""LCD monitor SH1107 — entrypoint production (systemd /opt/dronebridge/lcd_monitor)."""

from __future__ import annotations

import argparse
import fcntl
import os
import signal
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lcd import CM5SystemScreen, DroneScreen, collect_live, display_image, open_display

_running = True
_LOCK_FP = None


def _acquire_singleton_lock(install_dir: str) -> None:
    """Chỉ cho phép một tiến trình LCD — tránh nhảy màn khi chạy trùng."""
    global _LOCK_FP
    lock_candidates = [
        Path(install_dir) / ".lcd_monitor.lock",
        Path("/tmp") / "dronebridge-lcd_monitor.lock",
    ]
    last_err: Exception | None = None
    for lock_path in lock_candidates:
        try:
            lock_path.parent.mkdir(parents=True, exist_ok=True)
            _LOCK_FP = open(lock_path, "w", encoding="utf-8")
            break
        except OSError as exc:
            last_err = exc
            _LOCK_FP = None
    if _LOCK_FP is None:
        print(f"Không tạo được lock file LCD: {last_err}", file=sys.stderr)
        raise SystemExit(1)
    try:
        fcntl.flock(_LOCK_FP.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        print(
            "LCD monitor đã chạy. Dừng bản trùng: pkill -f run_lcd_monitor.py",
            file=sys.stderr,
        )
        raise SystemExit(1)
    _LOCK_FP.write(str(os.getpid()))
    _LOCK_FP.flush()


def _stop(*_args) -> None:
    global _running
    _running = False


def _load_lcd_cfg(install_dir: str) -> dict:
    cfg_path = Path(install_dir) / "config.yaml"
    if not cfg_path.is_file():
        return {}
    try:
        import yaml  # type: ignore

        with cfg_path.open(encoding="utf-8") as f:
            full = yaml.safe_load(f) or {}
        return full.get("lcd") or {}
    except Exception:
        return {}


def main() -> int:
    install_dir = os.environ.get("DRONEBRIDGE_INSTALL_DIR", "/opt/dronebridge")
    lcd_cfg = _load_lcd_cfg(install_dir)
    web_port = 8080
    try:
        import yaml  # type: ignore

        with (Path(install_dir) / "config.yaml").open(encoding="utf-8") as f:
            web_port = int((yaml.safe_load(f) or {}).get("web", {}).get("port", 8080))
    except Exception:
        pass

    parser = argparse.ArgumentParser(description="SH1107 live dashboard")
    parser.add_argument("--install-dir", default=install_dir)
    parser.add_argument("--bus", type=int, default=int(lcd_cfg.get("bus", os.environ.get("LCD_I2C_BUS", 3))))
    parser.add_argument("--addr", type=lambda x: int(x, 0), default=int(lcd_cfg.get("address", 0x3C)))
    parser.add_argument("--interval", type=float, default=float(lcd_cfg.get("interval", 1.0)))
    parser.add_argument("--hold", type=float, default=float(lcd_cfg.get("screen_hold", 5.0)))
    parser.add_argument("--screen", choices=("alt", "cm5", "drone"), default=lcd_cfg.get("screen", "alt"))
    parser.add_argument("--rotate", type=int, default=0, choices=(0, 1, 2, 3))
    parser.add_argument("--page-mode", action="store_true")
    args = parser.parse_args()

    _acquire_singleton_lock(args.install_dir)

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    try:
        dev = open_display(bus=args.bus, address=args.addr, contrast=255, page_mode=args.page_mode)
    except OSError as exc:
        print(f"Không mở được LCD: {exc}", file=sys.stderr)
        return 1

    cm5 = CM5SystemScreen()
    drone = DroneScreen()
    show_cm5 = args.screen != "drone"
    last_switch = time.time()
    boot_epoch: float | None = None

    try:
        while _running:
            state = collect_live(
                install_dir=args.install_dir,
                web_port=web_port,
                boot_epoch=boot_epoch,
            )
            boot_epoch = state.boot_epoch

            if args.screen == "alt":
                if time.time() - last_switch >= args.hold:
                    show_cm5 = not show_cm5
                    last_switch = time.time()
                frame = cm5.render(state) if show_cm5 else drone.render(state)
            elif args.screen == "cm5":
                frame = cm5.render(state)
            else:
                frame = drone.render(state)

            display_image(dev, frame, rotate=args.rotate)
            time.sleep(args.interval)
    finally:
        dev.cleanup()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
