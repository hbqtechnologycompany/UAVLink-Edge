#!/bin/bash
# Cài Docker trên Debian / Raspberry Pi OS (docker.io + docker-compose)
set -euo pipefail

if command -v docker >/dev/null 2>&1; then
    echo "Docker: $(docker --version)"
    if docker compose version >/dev/null 2>&1; then
        echo "Compose: $(docker compose version)"
    elif command -v docker-compose >/dev/null 2>&1; then
        echo "Compose: $(docker-compose --version)"
    fi
    systemctl enable docker 2>/dev/null || true
    exit 0
fi

echo "▶ Cài docker.io + docker-compose..."
apt-get update -qq
apt-get install -y -qq docker.io docker-compose git ca-certificates curl

systemctl enable --now docker
echo "✅ $(docker --version)"
if command -v docker-compose >/dev/null 2>&1; then
    echo "✅ $(docker-compose --version)"
fi
