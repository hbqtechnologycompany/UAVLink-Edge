#!/bin/bash
# [BUILD] Dựng lại imx219.ko (VIN, 2-lane RAW10) và overlay từ mã nguồn trong thư mục này.
# Không đụng hệ thống: chỉ ghi imx219.ko và sun55i-t527-imx219.dtbo cạnh script.
# Cần: linux-headers khớp `uname -r`, git, dtc, mạng (lấy header BSP của walnutpi/linux).
# Gọi: bash build.sh [thư-mục-làm-việc]
set -euo pipefail
HERE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
KVER="$(uname -r)"
WORK="${1:-$HOME/.cache/dronebridge-imx219}"
KDIR="/lib/modules/$KVER/build"

[ -d "$KDIR" ] || { echo "Thiếu kernel headers: $KDIR"; exit 1; }

# Driver phải khớp ABI với vin_io.ko của image WalnutPi → dùng đúng nhánh kernel của hãng.
if [ ! -d "$WORK/linux/bsp/drivers/vin" ]; then
    mkdir -p "$WORK"
    git clone --depth 1 --filter=blob:none --sparse -b "$KVER" \
        https://github.com/walnutpi/linux.git "$WORK/linux"
    git -C "$WORK/linux" sparse-checkout set --no-cone '/bsp/drivers/vin/' '/bsp/include/'
fi

SENSOR="$WORK/linux/bsp/drivers/vin/modules/sensor"
BUILD="$WORK/build"
rm -rf "$BUILD"
mkdir -p "$BUILD"
cp "$HERE/imx219.c" "$BUILD/"
cat > "$BUILD/Kbuild" <<EOF
obj-m := imx219.o
ccflags-y += -I$SENSOR -I$SENSOR/../.. -I$WORK/linux/bsp/include
EOF
make -C "$KDIR" M="$BUILD" modules
strip --strip-debug "$BUILD/imx219.ko"
cp "$BUILD/imx219.ko" "$HERE/imx219.ko"
dtc -@ -I dts -O dtb -o "$HERE/sun55i-t527-imx219.dtbo" "$HERE/sun55i-t527-imx219.dts"
/sbin/modinfo "$HERE/imx219.ko" | grep -E '^(vermagic|srcversion)'
echo "OK  $HERE/imx219.ko, $HERE/sun55i-t527-imx219.dtbo"
