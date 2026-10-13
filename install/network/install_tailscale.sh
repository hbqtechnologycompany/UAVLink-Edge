#!/bin/bash
# =============================================================================
# install_tailscale.sh — Tailscale admin plane (SSH + dashboard qua 100.x)
#
# Gọi từ: install.sh, docker/setup.sh, hoặc thủ công:
#   sudo bash install/network/install_tailscale.sh
#
# Auth key (tuỳ chọn, cho fleet unattended):
#   export TAILSCALE_AUTHKEY=tskey-auth-...
#   hoặc ghi vào /opt/dronebridge/data/tailscale_authkey (chmod 600)
#
# Hostname trên tailnet: device.name trong config.yaml (vd. Drone_sim_1455)
# =============================================================================
set -euo pipefail

INSTALL_DIR="${DRONEBRIDGE_DIR:-/opt/dronebridge}"
AUTHKEY="${TAILSCALE_AUTHKEY:-}"
AUTHKEY_FILE="${TAILSCALE_AUTHKEY_FILE:-$INSTALL_DIR/data/tailscale_authkey}"
TS_DEFAULT="/etc/default/tailscaled"
UNIT_UP="/etc/systemd/system/dronebridge-tailscale-up.service"

log() { echo "[tailscale] $*"; }

if [ "$EUID" -ne 0 ]; then
    echo "❌ Chạy với sudo"
    exit 1
fi

# ── Auth key từ file (nếu chưa set env) ───────────────────────────────────────
if [ -z "$AUTHKEY" ] && [ -f "$AUTHKEY_FILE" ]; then
    AUTHKEY="$(tr -d ' \n\r\t' < "$AUTHKEY_FILE")"
    [ -n "$AUTHKEY" ] && log "Dùng auth key từ $AUTHKEY_FILE"
fi

# ── Hostname cho tailnet ──────────────────────────────────────────────────────
read_drone_hostname() {
    local cfg="$INSTALL_DIR/config.yaml"
    local profile="$INSTALL_DIR/data/device_profile.json"
    local name=""

    if [ -f "$profile" ] && command -v python3 >/dev/null 2>&1; then
        name="$(python3 -c 'import json,sys
try:
    with open(sys.argv[1]) as f: d=json.load(f)
    print((d.get("name") or "").strip())
except Exception: pass' "$profile" 2>/dev/null || true)"
    fi

    if [ -z "$name" ] && [ -f "$cfg" ]; then
        name="$(grep -E '^\s+name:' "$cfg" 2>/dev/null | head -1 | awk '{print $2}' | tr -d '"')"
    fi

    # Tailscale hostname: DNS label (không underscore)
    name="$(echo "$name" | tr ' _' '-' | tr -cd 'a-zA-Z0-9.-' | head -c 63)"
    echo "$name"
}

# ── Cài package ───────────────────────────────────────────────────────────────
install_package() {
    if command -v tailscale >/dev/null 2>&1; then
        log "Đã cài: $(tailscale version 2>/dev/null | head -1 || echo tailscale)"
        return 0
    fi
    log "Cài Tailscale (tailscale.com/install.sh)..."
    if ! curl -fsSL https://tailscale.com/install.sh | sh; then
        log "❌ Cài Tailscale thất bại"
        exit 1
    fi
}

# ── Daemon — /etc/default/tailscaled (PORT bắt buộc, không được rỗng) ─────────
configure_daemon() {
    mkdir -p "$(dirname "$TS_DEFAULT")"
    if [ -f "$TS_DEFAULT" ] && grep -qE '^PORT=41641' "$TS_DEFAULT" 2>/dev/null; then
        log "Giữ $TS_DEFAULT"
        return 0
    fi
    cat > "$TS_DEFAULT" << 'EOF'
# DroneBridge — tailscale up flags: scripts/install_tailscale.sh
# PORT=41641: tránh systemd truyền --port="" (tailscaled exit 2)
PORT=41641
FLAGS=""
EOF
    log "✅ $TS_DEFAULT"
}

# ── Oneshot join lúc boot (chỉ khi có auth key file) ─────────────────────────
install_up_unit() {
    cat > "$UNIT_UP" << EOF
[Unit]
Description=DroneBridge Tailscale join (auth key file)
After=tailscaled.service network-online.target
Wants=tailscaled.service network-online.target
ConditionPathExists=$AUTHKEY_FILE

[Service]
Type=oneshot
RemainAfterExit=yes
Environment=DRONEBRIDGE_DIR=$INSTALL_DIR
Environment=TAILSCALE_AUTHKEY_FILE=$AUTHKEY_FILE
ExecStart=/bin/bash $INSTALL_DIR/install/network/install_tailscale.sh --join-only

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    if [ -f "$AUTHKEY_FILE" ]; then
        systemctl enable dronebridge-tailscale-up.service 2>/dev/null || true
        log "✅ dronebridge-tailscale-up.service (auth key file)"
    else
        systemctl disable dronebridge-tailscale-up.service 2>/dev/null || true
    fi
}

# ── Join tailnet ──────────────────────────────────────────────────────────────
join_tailnet() {
    local hostname
    hostname="$(read_drone_hostname)"

    if tailscale status --json 2>/dev/null | grep -q '"BackendState":"Running"'; then
        if tailscale ip -4 2>/dev/null | grep -qE '^100\.'; then
            log "Đã join tailnet: $(tailscale ip -4 2>/dev/null | head -1)"
            [ -n "$hostname" ] && tailscale set --hostname="$hostname" 2>/dev/null || true
            return 0
        fi
    fi

    local args=(up --accept-routes=false --accept-dns=true --ssh=false)
    [ -n "$hostname" ] && args+=(--hostname="$hostname")

    if [ -n "$AUTHKEY" ]; then
        args+=(--authkey="$AUTHKEY")
        log "Join tailnet (auth key) hostname=${hostname:-auto}..."
        tailscale "${args[@]}"
        log "✅ Tailscale IP: $(tailscale ip -4 2>/dev/null || echo '?')"
        return 0
    fi

    log "Chưa có auth key — bỏ qua tailscale up tự động."
    log "Đăng nhập thủ công (copy URL từ output):"
    echo ""
    echo "  sudo tailscale up --accept-routes=false --accept-dns=true --ssh=false \\"
    if [ -n "$hostname" ]; then
        echo "    --hostname=$hostname"
    fi
    echo ""
    log "Hoặc fleet: ghi tskey-auth vào $AUTHKEY_FILE (chmod 600) rồi chạy lại script."
}

# ── Main ──────────────────────────────────────────────────────────────────────
JOIN_ONLY=0
[ "${1:-}" = "--join-only" ] && JOIN_ONLY=1

if [ "$JOIN_ONLY" -eq 1 ]; then
    join_tailnet
    exit 0
fi

install_package
configure_daemon
systemctl enable tailscaled.service 2>/dev/null || true
systemctl restart tailscaled.service 2>/dev/null || systemctl start tailscaled.service

mkdir -p "$INSTALL_DIR/data"
install_up_unit
join_tailnet

log "Trạng thái:"
tailscale status 2>/dev/null | head -5 || true
