#!/bin/bash
# Wrapper: docker compose (plugin) hoặc docker-compose (Debian/Raspberry Pi OS)
set -euo pipefail
DOCKER_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DOCKER_DIR"

if docker compose version >/dev/null 2>&1; then
    exec docker compose "$@"
fi
if command -v docker-compose >/dev/null 2>&1; then
    exec docker-compose "$@"
fi

echo "❌ Không tìm thấy docker compose — cài: sudo apt install docker.io docker-compose" >&2
exit 1
