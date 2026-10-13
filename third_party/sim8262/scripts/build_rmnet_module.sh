#!/usr/bin/env bash
# Build and install only the RMNET module for the running MHI kernel.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT}/build/linux"
FRAGMENT="${ROOT}/kernel/mhi.fragment"

[[ -d "${BUILD_DIR}/.git" ]] || { echo "Kernel source missing. Run scripts/build_mhi_kernel.sh first."; exit 1; }

cd "${BUILD_DIR}"
make ARCH=arm64 CROSS_COMPILE= bcm2712_defconfig >/dev/null

while IFS= read -r line; do
  [[ -z "${line}" || "${line}" =~ ^# ]] && continue
  key="${line%%=*}"
  val="${line#*=}"
  case "${val}" in
    y) ./scripts/config --file .config --enable "${key}" ;;
    m) ./scripts/config --file .config --module "${key}" ;;
    n) ./scripts/config --file .config --disable "${key}" ;;
    *) ./scripts/config --file .config --set-val "${key}" "${val}" ;;
  esac
done < "${FRAGMENT}"

./scripts/config --file .config --set-str LOCALVERSION "-mhi-pcie"
make ARCH=arm64 CROSS_COMPILE= olddefconfig

make ARCH=arm64 CROSS_COMPILE= modules_prepare

echo "Building rmnet module..."
make ARCH=arm64 CROSS_COMPILE= -j"$(nproc)" M=drivers/net/ethernet/qualcomm/rmnet modules

KO="${BUILD_DIR}/drivers/net/ethernet/qualcomm/rmnet/rmnet.ko"
[[ -f "$KO" ]] || { echo "rmnet.ko not found"; exit 1; }

echo "Installing rmnet.ko..."
sudo cp "$KO" "/lib/modules/$(uname -r)/kernel/drivers/net/ethernet/qualcomm/rmnet/"
sudo depmod -a
echo "Done. Load with: sudo modprobe rmnet"
