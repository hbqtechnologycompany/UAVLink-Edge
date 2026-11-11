#!/bin/bash
# Giới hạn log hệ thống để thẻ nhớ / eMMC không đầy và không bị ghi mòn.
#   - journald: lưu bền (/var/log/journal), tối đa JOURNAL_MAX, luôn chừa JOURNAL_KEEP_FREE trống
#   - w2b-dis-eth-led (WalnutPi): vòng lặp 1 s in 6 dòng/s (~500k dòng/ngày) — tắt stdout, giữ chức năng LED
# File lỗi riêng của DroneBridge (data/logs/dronebridge-errors.log) tự xoay vòng trong binary.
# Gọi: setup_journald.sh            cài / sửa (root); đúng rồi thì không đổi gì
#      setup_journald.sh --check    chỉ kiểm tra
set -euo pipefail

JOURNAL_MAX=300M
JOURNAL_KEEP_FREE=1G
JOURNAL_FILE_MAX=30M
DROPIN=/etc/systemd/journald.conf.d/50-dronebridge.conf
LED_UNIT=w2b-dis-eth-led.service
LED_DROPIN=/etc/systemd/system/${LED_UNIT}.d/50-dronebridge-quiet.conf

JOURNAL_CONF="# DroneBridge: giới hạn journal (install/service/setup_journald.sh)
[Journal]
Storage=persistent
Compress=yes
SystemMaxUse=${JOURNAL_MAX}
SystemKeepFree=${JOURNAL_KEEP_FREE}
SystemMaxFileSize=${JOURNAL_FILE_MAX}
RuntimeMaxUse=48M"

LED_CONF="# DroneBridge: script LED in 6 dòng/giây vào journal — bỏ stdout, giữ chức năng
[Service]
StandardOutput=null"

led_unit_present() { systemctl cat "$LED_UNIT" >/dev/null 2>&1; }

if [ "${1:-}" = "--check" ]; then
    miss=()
    [ "$(cat "$DROPIN" 2>/dev/null)" = "$JOURNAL_CONF" ] || miss+=("$DROPIN")
    if led_unit_present && [ "$(cat "$LED_DROPIN" 2>/dev/null)" != "$LED_CONF" ]; then
        miss+=("$LED_DROPIN")
    fi
    if [ ${#miss[@]} -eq 0 ]; then
        echo "✅ journald ≤ ${JOURNAL_MAX} ($(journalctl --disk-usage 2>/dev/null | grep -oE '[0-9.]+[KMG]' | head -1) đang dùng)"
        exit 0
    fi
    echo "⚠️  Chưa giới hạn log: ${miss[*]}"
    exit 1
fi

[ "$EUID" -eq 0 ] || { echo "Cần sudo"; exit 1; }

if [ "$(cat "$DROPIN" 2>/dev/null)" = "$JOURNAL_CONF" ]; then
    echo "   = $DROPIN"
else
    mkdir -p "$(dirname "$DROPIN")"
    printf '%s\n' "$JOURNAL_CONF" > "$DROPIN"
    systemctl restart systemd-journald
    echo "   + $DROPIN (tối đa ${JOURNAL_MAX}, chừa ${JOURNAL_KEEP_FREE})"
fi
journalctl --vacuum-size="$JOURNAL_MAX" >/dev/null 2>&1 || true

if led_unit_present; then
    if [ "$(cat "$LED_DROPIN" 2>/dev/null)" = "$LED_CONF" ]; then
        echo "   = $LED_DROPIN"
    else
        mkdir -p "$(dirname "$LED_DROPIN")"
        printf '%s\n' "$LED_CONF" > "$LED_DROPIN"
        systemctl daemon-reload
        systemctl try-restart "$LED_UNIT" || true
        echo "   + $LED_DROPIN"
    fi
fi

echo "✅ journald ≤ ${JOURNAL_MAX}: $(journalctl --disk-usage 2>/dev/null | sed 's/.*take up //')"
