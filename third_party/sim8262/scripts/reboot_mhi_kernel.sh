#!/usr/bin/env bash
# Finalize CM5 PCIe setup: ensure boot config, then reboot into MHI kernel.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_TXT="/boot/firmware/config.txt"
KVER="6.12.93-mhi-pcie+"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run with sudo" >&2
  exit 1
fi

echo "=== Finalize CM5 PCIe (SIM8262) ==="

# Pi 5 / CM5 uses kernel_2712.img by default on recent firmware
if ! grep -q '^kernel=' "${CONFIG_TXT}"; then
  echo "kernel=kernel_2712.img" >> "${CONFIG_TXT}"
  echo "Added kernel=kernel_2712.img"
fi

if ! grep -q '^dtoverlay=pcie-32bit-dma-pi5' "${CONFIG_TXT}"; then
  echo "dtoverlay=pcie-32bit-dma-pi5" >> "${CONFIG_TXT}"
  echo "Added pcie-32bit-dma-pi5 overlay"
fi

# Copy kernel config for reference
if [[ -f "${ROOT}/build/linux/.config" ]]; then
  cp "${ROOT}/build/linux/.config" "/boot/config-${KVER}"
fi

depmod -a "${KVER}"

echo
echo "Installed:"
echo "  Kernel: /boot/firmware/kernel_2712.img (backup: kernel_2712.backup.img)"
echo "  Modules: /lib/modules/${KVER}/"
echo "  MHI modules: mhi, mhi_pci_generic, mhi_wwan_ctrl, mhi_wwan_mbim"
echo
echo "Rebooting in 5 seconds to activate PCIe/MHI stack..."
sleep 5
reboot
