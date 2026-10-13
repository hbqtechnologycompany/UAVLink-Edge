#!/usr/bin/env bash
# Complete CM5 PCIe bring-up after MHI kernel is installed and rebooted.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KVER="$(uname -r)"

echo "=== CM5 SIM8262 PCIe bring-up ==="
echo "Kernel: ${KVER}"

if ! grep -q '^dtoverlay=pcie-32bit-dma-pi5' /boot/firmware/config.txt; then
  echo "ERROR: Add dtoverlay=pcie-32bit-dma-pi5 to /boot/firmware/config.txt" >&2
  exit 1
fi

if ! grep -qE '^CONFIG_MHI_BUS=(y|m)' "/boot/config-${KVER}" 2>/dev/null; then
  echo "ERROR: Running kernel lacks MHI. Reboot into kernel_2712.img with MHI first." >&2
  exit 1
fi

echo "[1/5] Load MHI modules..."
for mod in mhi mhi_pci_generic wwan mhi_wwan_ctrl mhi_wwan_mbim mhi_net rmnet; do
  sudo modprobe "${mod}" 2>/dev/null || echo "  warn: modprobe ${mod} failed"
done

if ! ls /dev/wwan0qmi0 &>/dev/null; then
  echo "[1b/5] WWAN missing — running recover_mhi.sh..."
  sudo bash "${ROOT}/scripts/recover_mhi.sh"
fi

echo "[3/5] Check devices..."
ls -la /dev/wwan* 2>/dev/null || echo "  (no /dev/wwan* yet)"
ip -br link show type wwan 2>/dev/null || true

echo "[4/5] Kernel log..."
dmesg | grep -iE 'mhi|wwan|0308|sdx' | tail -20

echo "[5/5] Test AT (if port ready)..."
if ls /dev/wwan*at* >/dev/null 2>&1; then
  AT_PORT="$(ls /dev/wwan*at* | head -1)"
  echo "  AT port: ${AT_PORT}"
  if [[ -x "${ROOT}/.venv/bin/sim8262" ]]; then
    "${ROOT}/.venv/bin/sim8262" at "AT" --port "${AT_PORT}" || true
  fi
else
  echo "  AT port not ready — check dmesg above."
fi

echo
echo "Done. Next: sim8262 status && sim8262 connect"
