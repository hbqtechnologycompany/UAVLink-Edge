#!/usr/bin/env bash
# Compare PCIe vs USB paths for SIM8262 on CM5.
set -euo pipefail

cat <<'EOF'
SIM8262 trên CM5 — so sánh các cách

┌─────────────────────┬──────────────┬────────────────────────────────────────┐
│ Cách                │ Thời gian     │ Ghi chú                                │
├─────────────────────┼──────────────┼────────────────────────────────────────┤
│ 1. USB mode         │ ~10 phút     │ NHANH NHẤT nếu carrier có USB từ M.2   │
│ 2. Cross-compile    │ ~20 phút     │ Trên PC x64/WSL, copy kernel sang CM5  │
│ 3. Build native CM5 │ ~60-90 phút  │ Đang chạy — không cần PC thứ hai       │
│ 4. apt upgrade      │ KHÔNG ĐƯỢC   │ Kernel 6.18.34 vẫn tắt CONFIG_MHI     │
│ 5. Chỉ build module │ KHÔNG ĐƯỢC   │ Kernel gốc thiếu MHI → phải boot kernel mới │
└─────────────────────┴──────────────┴────────────────────────────────────────┘

Tại sao PCIe bắt buộc rebuild kernel?
  - SIM8262 qua M.2 PCIe dùng Qualcomm MHI (17cb:0308)
  - Raspberry Pi OS cố ý KHÔNG bật MHI (cả 6.12 lẫn 6.18)
  - Không có gói apt, DKMS, hay driver SIMCom thay thế được

CÁCH 1 — USB (khuyến nghị nếu base board hỗ trợ)
  Nhiều carrier CM5 (Waveshare, TOFU5+, v.v.) có M.2 Key B với USB + PCIe.
  - Chuyển DIP switch sang USB (hoặc cắm USB từ slot M.2)
  - Cài driver SIM8200 USB (5 phút): scripts/fetch_simcom_driver.sh
  - AT port: /dev/ttyUSB2 — driver Python hoạt động ngay
  - Không cần rebuild kernel

CÁCH 2 — Cross-compile (khuyến nghị nếu BẮT BUỘC PCIe)
  Trên PC Linux/WSL x64:
    sudo apt install crossbuild-essential-arm64 bc bison flex libssl-dev libncurses-dev
    ./scripts/build_mhi_kernel_cross.sh
  Copy kernel sang CM5 và reboot.

CÁCH 3 — Tiếp tục build native trên CM5
  Nếu không có PC x64 và bắt buộc PCIe → chờ build hiện tại xong.
  Theo dõi: tail -f build/linux/../terminals/260067.txt

Sau reboot (PCIe path):
  ./scripts/verify_pcie.sh
  sim8262 at "AT"
  sim8262 connect

EOF

# Live status
echo "=== Trạng thái hiện tại ==="
if lspci -nn | grep -q '17cb:0308'; then
  echo "Modem PCIe: detected"
else
  echo "Modem PCIe: not found"
fi
if lsusb | grep -qi '1e0e'; then
  echo "Modem USB:  detected"
else
  echo "Modem USB:  not found"
fi
if pgrep -f build_mhi_kernel.sh >/dev/null; then
  echo "Native build: RUNNING ($(ps -o etime= -p "$(pgrep -f 'make.*Image.gz' | head -1)" 2>/dev/null || echo '?'))"
else
  echo "Native build: not running"
fi
if [[ -f /home/rpi5drone/SIM8262_E_M2/build/linux/arch/arm64/boot/Image.gz ]]; then
  echo "Image.gz: READY"
else
  echo "Image.gz: not ready yet"
fi
