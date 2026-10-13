#!/usr/bin/env bash
# Fetch Qualcomm SDX65M firmware required by mhi_pci_generic for 17cb:0308.
set -euo pipefail

DEST="/lib/firmware/qcom/sdx65m"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

if [[ -f "${DEST}/xbl.elf" ]]; then
  echo "Firmware already present: ${DEST}/xbl.elf"
  exit 0
fi

echo "Downloading SDX65M firmware from linux-firmware..."
git clone --depth=1 --filter=blob:none --sparse https://gitlab.com/kernel-firmware/linux-firmware.git "${TMP}/lf"
(
  cd "${TMP}/lf"
  git sparse-checkout init --cone
  git sparse-checkout set qcom
)

SRC=""
for candidate in \
  "${TMP}/lf/qcom/sdx65m" \
  "${TMP}/lf/qcom/sdx62m" \
  "${TMP}/lf/qcom/sdx65"; do
  if [[ -d "${candidate}" ]]; then
    SRC="${candidate}"
    break
  fi
done

if [[ -z "${SRC}" ]]; then
  echo "No sdx65m/sdx62m firmware directory found in linux-firmware." >&2
  echo "Modem may still work if firmware is loaded from module flash." >&2
  exit 0
fi

sudo mkdir -p "${DEST}"
sudo cp -a "${SRC}/." "${DEST}/"
echo "Installed firmware to ${DEST}:"
ls -la "${DEST}"
