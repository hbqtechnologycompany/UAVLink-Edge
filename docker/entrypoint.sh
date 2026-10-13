#!/bin/bash
# Bootstrap container entrypoint (profile: bootstrap) — git pull + host install loop.
set -euo pipefail

HOST_REPO="${HOST_REPO_PATH:-/opt/Pi_CM5_DroneBridgeService}"
REPO_URL="${REPO_URL:-}"
BRANCH="${REPO_BRANCH:-main}"
PULL_INTERVAL="${PULL_INTERVAL:-300}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-60}"
MAX_RETRIES="${MAX_INSTALL_RETRIES:-3}"
RUN_USER="${DRONE_RUN_USER:-cm5drone6425}"

log() { echo "[bootstrap] $(date -Iseconds) $*"; }

run_install() {
    local dir="$1"
    if [ -f "$dir/install/install.sh" ]; then
        log "Running install/install.sh in $dir"
        (cd "$dir" && bash install/install.sh) && return 0
    fi
    if [ -f "$dir/docker/setup.sh" ]; then
        log "Running docker/setup.sh"
        bash "$dir/docker/setup.sh" && return 0
    fi
    log "No install.sh or docker/setup.sh found in $dir"
    return 1
}

if [ -n "$REPO_URL" ]; then
    if [ ! -d "$HOST_REPO/.git" ]; then
        log "Cloning $REPO_URL -> $HOST_REPO"
        mkdir -p "$(dirname "$HOST_REPO")"
        git clone --branch "$BRANCH" "$REPO_URL" "$HOST_REPO"
    fi
fi

if [ ! -d "$HOST_REPO" ]; then
    log "ERROR: HOST_REPO_PATH not found: $HOST_REPO"
    exit 1
fi

attempt=0
while [ "$attempt" -lt "$MAX_RETRIES" ]; do
    if [ -n "$REPO_URL" ] && [ -d "$HOST_REPO/.git" ]; then
        log "git pull ($BRANCH)"
        git -C "$HOST_REPO" fetch origin "$BRANCH" || true
        git -C "$HOST_REPO" reset --hard "origin/$BRANCH" || true
    fi
    if run_install "$HOST_REPO"; then
        log "Install OK"
        break
    fi
    attempt=$((attempt + 1))
    log "Install failed ($attempt/$MAX_RETRIES), retry in ${HEALTH_INTERVAL}s"
    sleep "$HEALTH_INTERVAL"
done

log "Bootstrap idle — pull every ${PULL_INTERVAL}s"
while true; do
    sleep "$PULL_INTERVAL"
    if [ -n "$REPO_URL" ] && [ -d "$HOST_REPO/.git" ]; then
        if git -C "$HOST_REPO" pull --ff-only origin "$BRANCH" 2>/dev/null; then
            log "Updated from git — re-run install"
            run_install "$HOST_REPO" || log "Re-install failed"
        fi
    fi
done
