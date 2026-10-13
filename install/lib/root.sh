# Thư mục gốc ứng dụng: nơi có config.yaml, Find_landing, binary.
# Cây này nằm tại <gốc>/install/lib/root.sh — cả trong repo lẫn /opt/dronebridge.
dronebridge_root() {
    local lib_dir app
    lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    app="$(cd "$lib_dir/../.." && pwd)"
    if [ -f "$app/config.yaml" ] || [ -d "$app/Find_landing" ] || [ -f "$app/go.mod" ]; then
        printf '%s\n' "$app"
        return 0
    fi
    printf '%s\n' "/opt/dronebridge"
}
