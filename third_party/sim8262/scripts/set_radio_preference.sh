#!/usr/bin/env bash
# Set RAT acquisition order for drone stability: LTE first, 3G fallback, 5G last.
set -euo pipefail

DEVICE="${1:-/dev/wwan0qmi0}"
PROFILE="${2:-stability}"

case "$PROFILE" in
  stability|drone)
    # LTE: best balance of speed + mobility for airborne use.
    # UMTS: fallback when LTE weak.
    # 5GNR: allowed but lowest priority (can be less stable when moving).
    ORDER="lte|umts|5gnr"
    ;;
  balanced)
    ORDER="lte|5gnr|umts"
    ;;
  max-speed)
    ORDER="5gnr|lte|umts"
    ;;
  legacy)
    ORDER="umts|lte"
    ;;
  *)
    echo "Unknown profile: $PROFILE (use: stability, balanced, max-speed, legacy)"
    exit 1
    ;;
esac

if [[ ! -e "$DEVICE" ]]; then
  echo "ERROR: $DEVICE not found — run scripts/recover_mhi.sh first"
  exit 1
fi

echo "RAT profile: $PROFILE → acquisition order: $ORDER"
OUT=$(sudo qmicli -d "$DEVICE" --nas-set-system-selection-preference="$ORDER" 2>&1)
echo "$OUT"

echo "--- current preference ---"
sudo qmicli -d "$DEVICE" --nas-get-system-selection-preference 2>&1 | \
  grep -iE 'Mode preference|Acquisition order|Usage preference|Network selection' || true
