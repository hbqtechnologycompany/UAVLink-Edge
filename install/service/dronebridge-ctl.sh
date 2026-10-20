#!/bin/bash
# Shared stop/start helpers for DroneBridge (used by install.sh & deploy-restart.sh).
#
# Pi CM5 có thể chạy một trong hai mode:
#   - Docker: container "dronebridge" (network_mode: host) — bind-mount /opt/dronebridge
#   - Native: systemd dronebridge.service
#
# UI (dashboard/settings) nằm TRONG binary (go:embed) → phải restart process/container sau go build.

INSTALL_DIR="${INSTALL_DIR:-/opt/dronebridge}"
BINARY_DST="${INSTALL_DIR}/dronebridge"
WEB_PORT="${WEB_PORT:-8080}"
MAVLINK_PORT="${MAVLINK_PORT:-14550}"

dronebridge_run_user() {
    local u=""
    if [ -f /etc/systemd/system/dronebridge.service ]; then
        u="$(grep -m1 '^User=' /etc/systemd/system/dronebridge.service 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
    fi
    if [ -n "$u" ] && id "$u" >/dev/null 2>&1; then
        printf '%s\n' "$u"
        return 0
    fi
    if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ] && id "$SUDO_USER" >/dev/null 2>&1; then
        printf '%s\n' "$SUDO_USER"
        return 0
    fi
    local owner=""
    if [ -d "$INSTALL_DIR" ]; then
        owner="$(stat -c '%U' "$INSTALL_DIR" 2>/dev/null || true)"
    fi
    if [ -n "$owner" ] && [ "$owner" != "root" ] && id "$owner" >/dev/null 2>&1; then
        printf '%s\n' "$owner"
        return 0
    fi
    echo "Không tìm được user chạy dronebridge (User=${u:-trống} không tồn tại)" >&2
    return 1
}

# Unit mẫu trong repo để User=pi. Máy này không có user pi — ghi lại user thật trước khi start.
ensure_service_user() {
    local user="$1"
    local unit="/etc/systemd/system/dronebridge.service"
    [ -f "$unit" ] || return 0
    local group cur_user cur_group
    group="$(id -gn "$user")"
    cur_user="$(grep -m1 '^User=' "$unit" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
    cur_group="$(grep -m1 '^Group=' "$unit" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
    if [ "$cur_user" = "$user" ] && [ "$cur_group" = "$group" ]; then
        return 0
    fi
    echo "▶ Unit User=${cur_user:-?}/Group=${cur_group:-?} không dùng được → ${user}/${group}"
    sed -i "s/^User=.*/User=${user}/" "$unit"
    sed -i "s/^Group=.*/Group=${group}/" "$unit"
    systemctl daemon-reload
}

docker_dronebridge_exists() {
    command -v docker >/dev/null 2>&1 \
        && docker ps -a --format '{{.Names}}' 2>/dev/null | grep -qx 'dronebridge'
}

docker_dronebridge_running() {
    command -v docker >/dev/null 2>&1 \
        && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx 'dronebridge'
}

use_docker_mode() {
    docker_dronebridge_exists
}

port_in_use() {
    local port="$1"
    ss -tuln 2>/dev/null | grep -qE ":${port}\b"
}

stop_dronebridge_native() {
    echo "▶ Dừng dronebridge (systemd)..."
    systemctl stop dronebridge 2>/dev/null || true
    systemctl reset-failed dronebridge 2>/dev/null || true

    local pid
    for pid in $(pgrep -f '^/opt/dronebridge/dronebridge ' 2>/dev/null || true); do
        kill -9 "$pid" 2>/dev/null || true
    done
    pkill -9 -x dronebridge 2>/dev/null || true

    local i
    for i in $(seq 1 20); do
        if ! pgrep -f '^/opt/dronebridge/dronebridge ' >/dev/null 2>&1 \
            && ! pgrep -x dronebridge >/dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    for i in $(seq 1 15); do
        if ! port_in_use "$MAVLINK_PORT" && ! port_in_use "$WEB_PORT"; then
            return 0
        fi
        sleep 1
    done

    echo "✗ Port ${MAVLINK_PORT}/${WEB_PORT} vẫn bận (native)" >&2
    ss -tulnp 2>/dev/null | grep -E ":${MAVLINK_PORT}|:${WEB_PORT}" >&2 || true
    pgrep -af dronebridge >&2 || true
    return 1
}

stop_dronebridge_docker() {
    echo "▶ Dừng container dronebridge..."
    docker stop dronebridge 2>/dev/null || true
    systemctl stop dronebridge 2>/dev/null || true

    local i
    for i in $(seq 1 25); do
        if ! docker ps --filter name=dronebridge --filter status=running -q | grep -q . \
            && ! port_in_use "$MAVLINK_PORT" && ! port_in_use "$WEB_PORT"; then
            return 0
        fi
        sleep 1
    done

    echo "✗ Docker/port vẫn bận" >&2
    docker ps -a --filter name=dronebridge 2>&1 || true
    ss -tulnp 2>/dev/null | grep -E ":${MAVLINK_PORT}|:${WEB_PORT}" >&2 || true
    return 1
}

stop_dronebridge() {
    if use_docker_mode; then
        stop_dronebridge_docker
    else
        stop_dronebridge_native
    fi
}

install_dronebridge_binary() {
    local src="$1"
    local user
    user="$(dronebridge_run_user)"

    if [ ! -f "$src" ]; then
        echo "✗ Không tìm thấy binary: $src" >&2
        return 1
    fi

    ensure_service_user "$user"
    local group
    group="$(id -gn "$user")"
    echo "▶ Cài binary → $BINARY_DST (user=${user}:${group})"
    install -m 755 "$src" "$BINARY_DST"
    chown "${user}:${group}" "$BINARY_DST"
}

wait_for_http() {
    local i
    for i in $(seq 1 30); do
        if curl -sf --max-time 2 "http://127.0.0.1:${WEB_PORT}/dashboard.html" >/dev/null 2>&1; then
            echo "✓ Web UI phản hồi trên :${WEB_PORT}"
            return 0
        fi
        sleep 1
    done
    echo "✗ Web UI chưa phản hồi trên :${WEB_PORT}" >&2
    return 1
}

start_dronebridge() {
    if use_docker_mode; then
        echo "▶ Khởi động container dronebridge..."
        docker start dronebridge >/dev/null
    else
        local user
        user="$(dronebridge_run_user)"
        ensure_service_user "$user"
        echo "▶ Khởi động dronebridge.service (User=${user})..."
        systemctl start dronebridge
        local i
        for i in $(seq 1 30); do
            systemctl is-active --quiet dronebridge && break
            sleep 1
        done
        if ! systemctl is-active --quiet dronebridge; then
            echo "✗ dronebridge.service không active" >&2
            journalctl -u dronebridge -n 20 --no-pager >&2 || true
            return 1
        fi
    fi
    wait_for_http
}

verify_embedded_ui() {
    local marker="${1:-port-cam0-enabled}"
    if ! grep -aq "$marker" "$BINARY_DST" 2>/dev/null; then
        echo "✗ Binary trên disk chưa chứa marker UI: $marker" >&2
        return 1
    fi
    local n
    n="$(curl -s --max-time 5 "http://127.0.0.1:${WEB_PORT}/settings.html" 2>/dev/null | grep -c "$marker" || true)"
    if [ "${n:-0}" -lt 1 ]; then
        echo "✗ HTTP settings.html chưa phục vụ UI mới ($marker)" >&2
        return 1
    fi
    echo "✓ UI mới đang phục vụ ($marker)"
}

restart_dronebridge() {
    local binary_src="${1:-}"

    if use_docker_mode; then
        echo "▶ Mode: Docker (container dronebridge, mount $INSTALL_DIR)"
    else
        echo "▶ Mode: systemd native"
    fi

    stop_dronebridge || return 1
    if [ -n "$binary_src" ]; then
        install_dronebridge_binary "$binary_src" || return 1
    fi
    start_dronebridge || return 1
    verify_embedded_ui "port-cam0-enabled" || return 1
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    set -euo pipefail
    if [ "$(id -u)" -ne 0 ]; then
        echo "Chạy với sudo" >&2
        exit 1
    fi
    case "${1:-restart}" in
        stop) stop_dronebridge ;;
        start) start_dronebridge ;;
        restart) restart_dronebridge "${2:-}" ;;
        *) echo "Usage: sudo bash $0 {stop|start|restart} [binary_path]" >&2; exit 1 ;;
    esac
fi
