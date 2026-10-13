#!/usr/bin/env bash
# Hardware and driver diagnostics for SIM8262-E on Raspberry Pi.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "=== SIM8262 Hardware Scan ==="
python3 - <<'PY'
import json
from sim8262.hardware import scan_hardware

status = scan_hardware()
print(json.dumps(status, indent=2, default=lambda o: o.__dict__))
PY

echo
echo "=== PCIe modem ==="
lspci -nn | grep -i '17cb:0308' || echo "Qualcomm 17cb:0308 not found on PCIe"

echo
echo "=== USB modem ==="
lsusb | grep -i '1e0e' || echo "SIMCom USB VID 1e0e not found"

echo
echo "=== Kernel MHI support ==="
if zgrep -q 'CONFIG_MHI_BUS=y' /proc/config.gz 2>/dev/null; then
  echo "CONFIG_MHI_BUS=y"
elif zgrep -q 'CONFIG_MHI_BUS=m' /proc/config.gz 2>/dev/null; then
  echo "CONFIG_MHI_BUS=m"
elif zgrep -q 'CONFIG_MHI_BUS is not set' /proc/config.gz 2>/dev/null; then
  echo "CONFIG_MHI_BUS is NOT enabled (PCIe data path blocked)"
else
  echo "Could not read kernel config"
fi

echo
echo "=== Control/data ports ==="
ls -l /dev/ttyUSB* /dev/cdc-wdm* /dev/wwan* 2>/dev/null || true
ip -br link show type wwan 2>/dev/null || true

echo
echo "Project root: ${ROOT}"
