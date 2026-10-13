#!/usr/bin/env bash
# Guide for downloading official SIMCom resources for SIM8262-E.
set -euo pipefail

cat <<'EOF'
SIM8262-E official resources (SIMCom)

1) Product page
   https://www.simcom.com/product/SIM8262X-M2.html

2) Technical files portal
   https://www.simcom.com/technical_files-p60.html
   Filter:
   - Product line: 5G
   - Product name: SIM8262X-M2 Series

Important downloads:
- SIM82XX_SIM83XX Series AT Command Manual
- SIM82XX_SIM83XX Series Linux USB Application Note V1.01
- SIMCom 5G Series Module_PCIE USE NOTE V1.00
- SIM8262E-M2 Hardware Design / SPEC

3) Waveshare bundled USB driver (SIM8200 family, compatible with SIM8262 USB mode)
   https://www.waveshare.com/wiki/SIM8262E-M2_5G_HAT
   Resource -> Software -> SIM8200 Driver

4) Linux USB mode summary (SIMCom official note)
   VID/PID: 1e0e:9001 (QMI default)
   Other PIDs: 9003 (MBIM+AT), 9011 (RNDIS), 901e (MBIM no AT)
   Requires kernel modules: option, qmi_wwan or cdc_mbim, usbserial

5) Linux PCIe mode summary
   Device: Qualcomm 17cb:0308 (Snapdragon X62 / SDX62)
   Requires mainline MHI stack:
     CONFIG_MHI_BUS
     CONFIG_WWAN
     CONFIG_MHI_WWAN_CTRL
     CONFIG_MHI_NET
   Firmware path in mainline kernel: /lib/firmware/qcom/sdx65m/

6) SIMCom support
   support@simcom.com
   Ask for qmi_wwan_simcom.c if you need the vendor QMI patch for USB mode.

After downloading USB driver package:
  unzip SIM8200_OS_Driver.zip
  cd SIM8200_OS_Driver/linux
  sudo ./install.sh

Note: Raspberry Pi OS 6.12 currently ships without MHI modules.
For your current PCIe attachment, kernel rebuild or USB fallback is required.
EOF
