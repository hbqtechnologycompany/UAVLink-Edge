from __future__ import annotations

import argparse
import json
import subprocess
import sys

from sim8262.config import Sim8262Config
from sim8262.hardware import scan_hardware
from sim8262.manager import ConnectionManager
from sim8262.modem import Sim8262Modem
from sim8262.pcie_status import pcie_status


def _print_json(data) -> None:
    def default(obj):
        if hasattr(obj, "__dict__"):
            return obj.__dict__
        return str(obj)

    print(json.dumps(data, indent=2, default=default))


def cmd_scan(_: argparse.Namespace) -> int:
    status = scan_hardware()
    _print_json(status)
    return 1 if status.issues else 0


def cmd_at(args: argparse.Namespace) -> int:
    config = Sim8262Config.from_file(args.config) if args.config else Sim8262Config()
    modem = Sim8262Modem(config)
    try:
        port = modem.connect(args.port)
        print(f"Connected on {port}")
        response = modem.commands.at.query(args.command)
        print(response)
        return 0
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    finally:
        modem.close()


def cmd_status(args: argparse.Namespace) -> int:
    config = Sim8262Config.from_file(args.config) if args.config else Sim8262Config()
    modem = Sim8262Modem(config)
    try:
        modem.connect(args.port)
        _print_json(modem.status())
        return 0
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    finally:
        modem.close()


def cmd_connect(args: argparse.Namespace) -> int:
    config = Sim8262Config.from_file(args.config) if args.config else Sim8262Config()
    manager = ConnectionManager(config)
    result = manager.connect(args.apn)
    _print_json(result)
    return 0 if result.success else 1


def cmd_pcie_status(_: argparse.Namespace) -> int:
    try:
        _print_json(pcie_status())
        return 0
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1


def cmd_radio(args: argparse.Namespace) -> int:
    from pathlib import Path

    config = Sim8262Config.from_file(args.config) if args.config else Sim8262Config()
    profile = args.profile or config.rat_profile
    root = Path(__file__).resolve().parents[1]
    radio_script = root / "scripts" / "set_radio_preference.sh"
    device = args.device or "/dev/wwan0qmi0"
    result = subprocess.run(
        ["bash", str(radio_script), device, profile],
        capture_output=True,
        text=True,
    )
    print(result.stdout)
    if result.stderr:
        print(result.stderr, file=sys.stderr)
    return result.returncode


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="SIM8262-E communication driver")
    sub = parser.add_subparsers(dest="command", required=True)

    scan = sub.add_parser("scan", help="Scan USB/PCIe/modem interfaces")
    scan.set_defaults(func=cmd_scan)

    at = sub.add_parser("at", help="Send a raw AT command")
    at.add_argument("command")
    at.add_argument("--port")
    at.add_argument("--config")
    at.set_defaults(func=cmd_at)

    status = sub.add_parser("status", help="Read modem identity, SIM, signal, registration")
    status.add_argument("--port")
    status.add_argument("--config")
    status.set_defaults(func=cmd_status)

    connect = sub.add_parser("connect", help="Start packet data via MBIM/QMI/ModemManager")
    connect.add_argument("--apn")
    connect.add_argument("--config")
    connect.set_defaults(func=cmd_connect)

    pcie = sub.add_parser("pcie-status", help="Modem status via PCIe/MBIM (no AT port)")
    pcie.set_defaults(func=cmd_pcie_status)

    radio = sub.add_parser("radio", help="Set RAT acquisition order (LTE/3G/5G priority)")
    radio.add_argument("--profile", choices=["stability", "drone", "balanced", "max-speed", "legacy"])
    radio.add_argument("--device", default="/dev/wwan0qmi0")
    radio.add_argument("--config")
    radio.set_defaults(func=cmd_radio)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
