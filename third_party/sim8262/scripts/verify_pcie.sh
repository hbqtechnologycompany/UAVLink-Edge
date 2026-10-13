#!/usr/bin/env bash
# Verify SIM8262-E PCIe/MHI stack after reboot.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0

check() {
  local name="$1"
  shift
  if "$@"; then
    echo "OK   ${name}"
  else
    echo "FAIL ${name}"
    FAIL=1
  fi
}

echo "=== SIM8262 PCIe post-reboot check ==="
echo "Kernel: $(uname -r)"
echo

check "PCIe modem present" lspci -nn | grep -q '17cb:0308'
check "MHI in kernel config" grep -qE '^CONFIG_MHI_BUS=(y|m)' "/boot/config-$(uname -r)" 2>/dev/null || \
  zgrep -qE '^CONFIG_MHI_BUS=(y|m)' /proc/config.gz 2>/dev/null
check "mhi module loaded" lsmod | grep -q '^mhi '
check "mhi_pci_generic loaded" lsmod | grep -q 'mhi_pci_generic'
check "wwan AT port exists" test -e /dev/wwan0at0 || ls /dev/wwan*at* >/dev/null 2>&1
check "wwan MBIM port exists" test -e /dev/wwan0mbim0 || ls /dev/wwan*mbim* >/dev/null 2>&1

echo
echo "--- lspci ---"
lspci -nn -s 0001:01:00.0 2>/dev/null || lspci -nn | grep 0308 || true

echo
echo "--- modules ---"
lsmod | grep -E 'mhi|wwan' || true

echo
echo "--- devices ---"
ls -la /dev/wwan* 2>/dev/null || echo "(no /dev/wwan*)"

echo
echo "--- dmesg (mhi/wwan) ---"
dmesg | grep -iE 'mhi|wwan|0308|sdx' | tail -20 || true

echo
if [[ -x "${ROOT}/.venv/bin/sim8262" ]]; then
  echo "--- sim8262 scan ---"
  "${ROOT}/.venv/bin/sim8262" scan || true
fi

echo
if [[ "${FAIL}" -eq 0 ]]; then
  echo "All checks passed. Try: sim8262 at \"AT\""
else
  echo "Some checks failed. Review dmesg above."
fi
exit "${FAIL}"
