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
    if [ -f /etc/systemd/system/dronebridge.service ]; then
        local u
        u="$(grep -m1 '^User=' /etc/systemd/system/dronebridge.service | cut -d= -f2)"
        if [ -n "$u" ]; then
            echo "$u"
            return
        fi
    fi
    echo "${SUDO_USER:-pi}"
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

    echo "▶ Cài binary → $BINARY_DST (user=$user)"
    install -m 755 "$src" "$BINARY_DST"
    chown "${user}:${user}" "$BINARY_DST"
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
        echo "▶ Khởi động dronebridge.service..."
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
    local marker="${1:-stream-cam0-enabled}"
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
    verify_embedded_ui "stream-cam0-enabled" || return 1
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
