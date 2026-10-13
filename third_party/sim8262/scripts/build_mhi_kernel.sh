#!/usr/bin/env bash
# Build Raspberry Pi 5 kernel with MHI/WWAN support for SIM8262-E PCIe.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT}/build/linux"
FRAGMENT="${ROOT}/kernel/mhi.fragment"
KERNEL="${KERNEL:-kernel_2712}"
BRANCH="${BRANCH:-rpi-6.12.y}"
JOBS="${JOBS:-$(nproc)}"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "Missing dependency: $1" >&2; exit 1; }
}

for bin in git make bc bison flex gcc; do
  need "$bin"
done

if [[ ! -d "${BUILD_DIR}/.git" ]]; then
  echo "Cloning Raspberry Pi kernel (${BRANCH})..."
  git clone --depth=1 --branch "${BRANCH}" https://github.com/raspberrypi/linux "${BUILD_DIR}"
fi

cd "${BUILD_DIR}"

echo "Applying bcm2712_defconfig..."
make ARCH=arm64 CROSS_COMPILE= KCONFIG_CONFIG=.config bcm2712_defconfig

echo "Merging MHI config fragment..."
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

# Avoid overwriting stock modules; mark this build clearly.
./scripts/config --file .config --set-str LOCALVERSION "-mhi-pcie"

make ARCH=arm64 CROSS_COMPILE= olddefconfig

echo "Building kernel (this may take 30-90 minutes on Pi 5)..."
make ARCH=arm64 CROSS_COMPILE= -j"${JOBS}" Image.gz modules dtbs

echo "Installing modules..."
sudo make ARCH=arm64 CROSS_COMPILE= modules_install

echo "Backing up current kernel..."
if [[ -f "/boot/firmware/${KERNEL}.img" && ! -f "/boot/firmware/${KERNEL}.backup.img" ]]; then
  sudo cp "/boot/firmware/${KERNEL}.img" "/boot/firmware/${KERNEL}.backup.img"
fi

echo "Installing kernel image and device trees..."
sudo cp arch/arm64/boot/Image.gz "/boot/firmware/${KERNEL}.img"
sudo cp arch/arm64/boot/dts/broadcom/*.dtb /boot/firmware/
sudo cp arch/arm64/boot/dts/overlays/*.dtb* /boot/firmware/overlays/
sudo cp arch/arm64/boot/dts/overlays/README /boot/firmware/overlays/

cat <<EOF

Build complete.

Next steps:
  1. Ensure /boot/firmware/config.txt contains:
       dtoverlay=pcie-32bit-dma-pi5
  2. Reboot: sudo reboot
  3. Verify:  lsmod | grep mhi
             ls /dev/wwan*
             sim8262 scan

EOF
