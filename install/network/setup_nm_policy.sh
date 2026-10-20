#!/bin/bash
# NetworkManager: không bao giờ bỏ cuộc tự kết nối WiFi/4G và tắt WiFi power save.
# Mặc định NM chỉ thử autoconnect 4 lần rồi chặn profile; drone phải tự lên lại mạng.
set -euo pipefail

CONF=/etc/NetworkManager/conf.d/90-dronebridge.conf
WANT='[connection]
connection.autoconnect-retries=0
wifi.powersave=2'

if [ "$(cat "$CONF" 2>/dev/null)" != "$WANT" ]; then
    mkdir -p "$(dirname "$CONF")"
    printf '%s\n' "$WANT" > "$CONF"
    nmcli general reload conf 2>/dev/null || systemctl reload NetworkManager || true
    echo "   ghi $CONF"
fi

while IFS=: read -r name type; do
    case "$type" in
        802-11-wireless|gsm) ;;
        *) continue ;;
    esac
    cur="$(nmcli -g connection.autoconnect-retries connection show "$name" 2>/dev/null || true)"
    if [ "$cur" != "0" ]; then
        nmcli connection modify "$name" connection.autoconnect-retries 0
        echo "   $name: autoconnect-retries=0"
    fi
done < <(nmcli -t -f NAME,TYPE connection show)
