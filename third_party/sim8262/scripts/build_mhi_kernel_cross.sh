#!/usr/bin/env bash
# Cross-compile Raspberry Pi 5 / CM5 kernel with MHI on x64 Linux or WSL.
# Typical build time: 15-30 minutes (vs 60-90 min native on CM5).
#
# Run this on a x64 machine, then copy results to CM5:
#   scp arch/arm64/boot/Image.gz cm5:/boot/firmware/kernel_2712.img
#   rsync -av lib/modules/ cm5:/lib/modules/
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT}/build/linux"
FRAGMENT="${ROOT}/kernel/mhi.fragment"
KERNEL="${KERNEL:-kernel_2712}"
BRANCH="${BRANCH:-rpi-6.12.y}"
JOBS="${JOBS:-$(nproc)}"
CROSS="${CROSS_COMPILE:-aarch64-linux-gnu-}"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "Missing: $1" >&2; exit 1; }
}

for bin in git make bc bison flex "${CROSS}gcc"; do
  need "$bin"
done

if [[ ! -d "${BUILD_DIR}/.git" ]]; then
  echo "Cloning kernel ${BRANCH}..."
  git clone --depth=1 --branch "${BRANCH}" https://github.com/raspberrypi/linux "${BUILD_DIR}"
fi

cd "${BUILD_DIR}"

make ARCH=arm64 CROSS_COMPILE="${CROSS}" KCONFIG_CONFIG=.config bcm2712_defconfig

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
make ARCH=arm64 CROSS_COMPILE="${CROSS}" olddefconfig

echo "Cross-compiling with ${JOBS} jobs..."
make ARCH=arm64 CROSS_COMPILE="${CROSS}" -j"${JOBS}" Image.gz modules dtbs

STAGE="${ROOT}/build/staged"
rm -rf "${STAGE}"
mkdir -p "${STAGE}/boot" "${STAGE}/modules"

make ARCH=arm64 CROSS_COMPILE="${CROSS}" INSTALL_MOD_PATH="${STAGE}/modules" modules_install
cp arch/arm64/boot/Image.gz "${STAGE}/boot/${KERNEL}.img"
cp arch/arm64/boot/dts/broadcom/*.dtb "${STAGE}/boot/"
mkdir -p "${STAGE}/boot/overlays"
cp arch/arm64/boot/dts/overlays/*.dtb* "${STAGE}/boot/overlays/"

cat <<EOF

Cross-build done. Artifacts: ${STAGE}/

Copy to CM5 (adjust user@host):
  scp ${STAGE}/boot/${KERNEL}.img user@cm5:/tmp/
  rsync -av ${STAGE}/modules/lib/modules/ user@cm5:/tmp/new-modules/

On CM5:
  sudo cp /boot/firmware/${KERNEL}.img /boot/firmware/${KERNEL}.backup.img
  sudo cp /tmp/${KERNEL}.img /boot/firmware/${KERNEL}.img
  sudo rsync -av /tmp/new-modules/ /lib/modules/
  sudo depmod -a
  # ensure config.txt has: dtoverlay=pcie-32bit-dma-pi5
  sudo reboot

EOF
