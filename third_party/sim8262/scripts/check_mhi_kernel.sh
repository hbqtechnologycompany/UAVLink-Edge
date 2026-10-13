#!/usr/bin/env bash
# Check whether the running kernel can drive SIM8262 over PCIe (MHI).
set -euo pipefail

KVER="$(uname -r)"
echo "Kernel: ${KVER}"

check_config() {
  local key="$1"
  local config_text=""
  if [[ -r /proc/config.gz ]]; then
    config_text="$(zgrep -E "^(${key}=y|${key}=m|# ${key} is not set)" /proc/config.gz 2>/dev/null || true)"
  elif [[ -r "/boot/config-${KVER}" ]]; then
    config_text="$(grep -E "^(${key}=y|${key}=m|# ${key} is not set)" "/boot/config-${KVER}" 2>/dev/null || true)"
  fi
  if [[ -z "${config_text}" ]]; then
    echo "  ${key} unknown"
    return 2
  fi
  echo "  ${config_text}"
  if grep -q "is not set" <<<"${config_text}"; then
    return 1
  fi
  return 0
}

echo "Required kernel options for SIM8262 PCIe:"
missing=0
for opt in CONFIG_MHI_BUS CONFIG_WWAN CONFIG_MHI_WWAN_CTRL CONFIG_MHI_NET; do
  check_config "${opt}" || missing=1
done

echo
echo "PCIe modem:"
lspci -nn | grep '17cb:0308' || echo "  not found"

echo
if [[ "${missing}" -ne 0 ]]; then
  cat <<'EOF'

PCIe path is blocked on this kernel.

Options:
A) Rebuild Raspberry Pi kernel with MHI enabled
   - Enable: CONFIG_MHI_BUS, CONFIG_WWAN, CONFIG_MHI_WWAN_CTRL, CONFIG_MHI_NET
   - Install firmware package: linux-firmware (qcom/sdx65m/*)
   - Expected devices after success: /dev/wwan0at0, /dev/wwan0mbim0, wwan0 netdev

B) Switch module to USB mode (if carrier exposes USB from M.2)
   - Use Windows + SIM8200 driver or UART AT port first
   - AT+CUSBCFG=USBID,1E0E,9003  (MBIM + AT ports)
   - Then use SIMCom Linux USB driver or mainline option/qmi_wwan/cdc_mbim

Reference: scripts/fetch_simcom_driver.sh
EOF
  exit 1
fi

echo "Kernel looks ready for PCIe/MHI."
