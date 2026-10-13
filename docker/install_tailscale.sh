#!/bin/bash
# Wrapper — gọi install/network/install_tailscale.sh
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$DIR")"
exec bash "$ROOT/install/network/install_tailscale.sh" "$@"
