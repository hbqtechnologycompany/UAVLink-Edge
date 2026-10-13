#!/usr/bin/env python3
"""Test Hello — lcd.driver."""

from __future__ import annotations

import argparse
import sys
import time
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lcd import DRIVER_ID, display_image, open_display

I2C_BUS = 3
DEFAULT_ADDR = 0x3C


def main() -> int:
    parser = argparse.ArgumentParser(description="SH1107 Hello")
    parser.add_argument("--bus", type=int, default=I2C_BUS)
    parser.add_argument("--addr", type=lambda x: int(x, 0), default=DEFAULT_ADDR)
    parser.add_argument("--rotate", type=int, default=0, choices=(0, 1, 2, 3))
    parser.add_argument("--page-mode", action="store_true")
    args = parser.parse_args()

    print(f"Driver: {DRIVER_ID}", file=sys.stderr)

    try:
        dev = open_display(bus=args.bus, address=args.addr, contrast=255, page_mode=args.page_mode)
    except OSError as exc:
        print(f"Không mở được LCD: {exc}", file=sys.stderr)
        return 1

    img = Image.new("1", (128, 128), 0)
    draw = ImageDraw.Draw(img)
    try:
        font = ImageFont.load_default()
    except Exception:
        font = None
    draw.text((24, 52), "Hello", fill=255, font=font)

    try:
        display_image(dev, img, rotate=args.rotate)
        print(f'OK "Hello" (page_mode={args.page_mode}, rotate={args.rotate})')
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        print("\nThoát.")
    finally:
        dev.cleanup()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
