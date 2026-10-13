#!/usr/bin/env bash
# Prepare Raspberry Pi 5 for SIM8262-E PCIe communication.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_TXT="/boot/firmware/config.txt"
OVERLAY="dtoverlay=pcie-32bit-dma-pi5"

echo "=== SIM8262-E PCIe setup ==="

if [[ "${EUID}" -ne 0 ]]; then
  echo "Re-run with sudo for system changes." >&2
  exit 1
fi

echo "[1/4] Installing build dependencies and firmware..."
apt-get update -qq
apt-get install -y --no-install-recommends \
  git bc bison flex libssl-dev libncurses-dev \
  firmware-qcom-soc \
  libmbim-utils libqmi-utils modemmanager

echo "[2/4] Enabling PCIe 32-bit DMA overlay for Pi 5..."
if grep -q '^dtoverlay=pcie-32bit-dma-pi5' "${CONFIG_TXT}"; then
  echo "  already enabled"
else
  echo "" >> "${CONFIG_TXT}"
  echo "# SIM8262-E M.2 PCIe modem (Qualcomm X62 / MHI)" >> "${CONFIG_TXT}"
  echo "${OVERLAY}" >> "${CONFIG_TXT}"
  echo "  added ${OVERLAY}"
fi

echo "[3/4] Configuring module autoload..."
cat >/etc/modules-load.d/sim8262-mhi.conf <<'EOF'
# SIM8262-E over PCIe (MHI stack)
mhi
mhi_pci_generic
wwan
mhi_wwan_ctrl
mhi_wwan_mbim
mhi_net
rmnet
EOF

echo "[4/4] Checking kernel MHI support..."
if zgrep -q '^CONFIG_MHI_BUS=y' /proc/config.gz 2>/dev/null || \
   zgrep -q '^CONFIG_MHI_BUS=m' /proc/config.gz 2>/dev/null || \
   grep -q '^CONFIG_MHI_BUS=y' "/boot/config-$(uname -r)" 2>/dev/null || \
   grep -q '^CONFIG_MHI_BUS=m' "/boot/config-$(uname -r)" 2>/dev/null; then
  echo "  MHI already enabled in running kernel."
else
  echo "  MHI NOT enabled. Build custom kernel:"
  echo "    ${ROOT}/scripts/build_mhi_kernel.sh"
fi

echo
echo "Setup done. If kernel was rebuilt, reboot now: reboot"
