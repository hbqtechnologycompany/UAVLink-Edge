#!/usr/bin/env bash
# Recover MHI/wwan devices when PCIe modem is visible but channels did not enumerate.
set -euo pipefail

PCI_ADDR="${PCI_ADDR:-0001:01:00.0}"

if ls /dev/wwan0qmi0 &>/dev/null; then
  echo "WWAN devices OK"
  exit 0
fi

if ! lspci -nn | grep -q '17cb:0308'; then
  echo "ERROR: Qualcomm 17cb:0308 not on PCI bus — check hardware / reboot"
  exit 1
fi

echo "Modem on PCI but no /dev/wwan* — reloading MHI stack..."
sudo systemctl stop ModemManager 2>/dev/null || true

for mod in mhi_net mhi_wwan_mbim mhi_wwan_ctrl rmnet; do
  sudo modprobe -r "$mod" 2>/dev/null || true
done
sudo modprobe -r mhi_pci_generic mhi 2>/dev/null || true
sleep 2

if [[ -e "/sys/bus/pci/devices/${PCI_ADDR}" ]]; then
  echo 1 | sudo tee "/sys/bus/pci/devices/${PCI_ADDR}/remove" >/dev/null
  sleep 3
fi
echo 1 | sudo tee /sys/bus/pci/rescan >/dev/null
sleep 8

for mod in mhi mhi_pci_generic wwan mhi_wwan_ctrl mhi_wwan_mbim mhi_net rmnet; do
  sudo modprobe "$mod" 2>/dev/null || true
done
sleep 5

if ls /dev/wwan0qmi0 &>/dev/null; then
  echo "Recovery OK: $(ls /dev/wwan*)"
  ip -br link | grep mhi || true
  exit 0
fi

echo "ERROR: recovery failed — try reboot"
sudo dmesg | grep -iE 'mhi|wwan|died|fail' | tail -15
exit 1
