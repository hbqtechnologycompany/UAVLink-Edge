#!/usr/bin/env python3
"""Demo dashboard 2 khung — CM5 ↔ Drone."""

from __future__ import annotations

import argparse
import signal
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lcd import (
    CM5SystemScreen,
    DroneScreen,
    collect_cm5,
    collect_live,
    demo_state,
    display_image,
    open_display,
)

_running = True


def _stop(*_args) -> None:
    global _running
    _running = False


def main() -> int:
    parser = argparse.ArgumentParser(description="SH1107 monitor demo")
    parser.add_argument("--bus", type=int, default=3)
    parser.add_argument("--addr", type=lambda x: int(x, 0), default=0x3C)
    parser.add_argument("--interval", type=float, default=1.0)
    parser.add_argument("--hold", type=float, default=5.0)
    parser.add_argument("--screen", choices=("alt", "cm5", "drone"), default="alt")
    parser.add_argument("--rotate", type=int, default=0, choices=(0, 1, 2, 3))
    parser.add_argument("--page-mode", action="store_true")
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--preview", nargs="?", const=".", metavar="DIR")
    parser.add_argument("--live", action="store_true")
    args = parser.parse_args()

    cm5 = CM5SystemScreen()
    drone = DroneScreen()
    boot = time.time() - max(1, __import__("lcd.collector", fromlist=["uptime_seconds"]).uptime_seconds())

    def state():
        if args.live:
            return collect_live(boot_epoch=boot)
        return demo_state(tick, boot_epoch=boot)

    if args.preview is not None:
        s = state()
        out = Path.cwd() if str(args.preview) == "." else Path(args.preview)
        out.mkdir(parents=True, exist_ok=True)
        p1, p2 = out / "monitor_cm5.png", out / "monitor_drone.png"
        cm5.render(s).save(p1)
        drone.render(s).save(p2)
        print(f"CM5:   {p1.resolve()}")
        print(f"Drone: {p2.resolve()}")
        return 0

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    try:
        dev = open_display(bus=args.bus, address=args.addr, contrast=255, page_mode=args.page_mode)
    except OSError as exc:
        print(f"Không mở được LCD: {exc}", file=sys.stderr)
        return 1

    show_cm5 = args.screen != "drone"
    last_switch = time.time()
    tick = 0

    try:
        while _running:
            s = state()
            if args.screen == "alt":
                if time.time() - last_switch >= args.hold:
                    show_cm5 = not show_cm5
                    last_switch = time.time()
                frame = cm5.render(s) if show_cm5 else drone.render(s)
            elif args.screen == "cm5":
                frame = cm5.render(s)
            else:
                frame = drone.render(s)
            display_image(dev, frame, rotate=args.rotate)
            tick += 1
            if args.once:
                break
            time.sleep(args.interval)
    finally:
        dev.cleanup()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
