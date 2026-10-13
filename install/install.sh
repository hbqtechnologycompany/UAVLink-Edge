#!/bin/bash
# =============================================================================
# DroneBridge All-in-One Installer
# =============================================================================
# Cài và cấu hình:  sudo bash install/install.sh
# Kiểm tra và sửa tại chỗ (không ghi đè config, không cài lại gói):
#   sudo bash install/install.sh --check
# =============================================================================

set -e

INSTALL_DIR="/opt/dronebridge"
RUN_USER="${SUDO_USER:-pi}"
GROUP_NAME="${RUN_USER}"
TABLE_ID=100
TABLE_NAME="dronebridge"

# install.sh nằm trong <gốc>/install. Gốc là repo hoặc /tmp/dronebridge_staging.
INSTALL_TREE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
STAGING_DIR="$(cd "$INSTALL_TREE/.." && pwd)"

echo "===================================================="
echo "   DroneBridge — Installer"
echo "   Staging: $STAGING_DIR"
echo "   Target:  $INSTALL_DIR"
echo "===================================================="

# ── Kiểm tra quyền root ────────────────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    echo "❌ Chạy với sudo: sudo bash install/install.sh"
    exit 1
fi

FAILS=0
run_step() {
    local name="$1"
    shift
    echo "▶ $name"
    if "$@"; then
        echo "   OK  $name"
        return 0
    fi
    echo "   FAIL  $name"
    FAILS=$((FAILS + 1))
    return 0
}

configure_host() {
    local cfg="$INSTALL_DIR/config.yaml"
    local bin_sh="$INSTALL_DIR/install"
    run_step "PBR" bash "$bin_sh/network/setup_pbr.sh"
    run_step "Ethernet PX4" bash "$bin_sh/network/setup_eth.sh" "$cfg"
    run_step "LCD I2C" bash "$bin_sh/network/setup_lcd_i2c.sh" "$cfg"
    run_step "MAVLink UART" bash "$bin_sh/network/setup_mavlink_uart.sh"
    run_step "Camera overlay" bash "$bin_sh/camera/setup_camera.sh" "$cfg"
    run_step "UART boot unit" install_uart_boot_unit "$bin_sh"
    run_step "Camera sudoers" bash "$bin_sh/camera/install_camera_sudoers.sh" "$RUN_USER"
    if [ -f "$INSTALL_DIR/.need_camera_reboot" ] || [ -f "$INSTALL_DIR/.need_lcd_reboot" ] || \
       { [ -f "$INSTALL_DIR/.need_host_reboot" ] && [ -s "$INSTALL_DIR/.need_host_reboot" ]; }; then
        echo "   Boot config đã ghi. Reboot thì kernel mới nhận camera/LCD/UART."
    fi
}

# Giống setup_eth.sh / setup_camera.sh: symlink ở gốc /opt và oneshot lúc boot.
install_uart_boot_unit() {
    local bin_sh="$1"
    ln -sfn install/network/setup_mavlink_uart.sh "$INSTALL_DIR/setup_mavlink_uart.sh"
    local unit_src="$INSTALL_DIR/etc/systemd/dronebridge-uart-setup.service"
    if [ ! -f "$unit_src" ] && [ -f "$bin_sh/../etc/systemd/dronebridge-uart-setup.service" ]; then
        unit_src="$bin_sh/../etc/systemd/dronebridge-uart-setup.service"
    fi
    if [ ! -f "$unit_src" ]; then
        echo "   thiếu dronebridge-uart-setup.service"
        return 1
    fi
    cp "$unit_src" /etc/systemd/system/dronebridge-uart-setup.service
    local svc="/etc/systemd/system/dronebridge.service"
    if [ -f "$svc" ] && ! grep -q 'dronebridge-uart-setup.service' "$svc"; then
        sed -i \
            -e 's/^After=/After=dronebridge-uart-setup.service /' \
            -e 's/^Wants=/Wants=dronebridge-uart-setup.service /' \
            "$svc"
    fi
    systemctl daemon-reload
    systemctl enable dronebridge-uart-setup.service
}

check_and_fix() {
    echo "===================================================="
    echo "   DroneBridge — kiểm tra và sửa tại chỗ"
    echo "   Target: $INSTALL_DIR"
    echo "===================================================="
    if [ ! -x "$INSTALL_DIR/dronebridge" ]; then
        echo "FAIL  thiếu $INSTALL_DIR/dronebridge — chạy cài đầy đủ trước"
        exit 1
    fi
    if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
        echo "FAIL  thiếu $INSTALL_DIR/config.yaml"
        exit 1
    fi
    if [ ! -x "$INSTALL_DIR/install/network/setup_pbr.sh" ]; then
        echo "FAIL  thiếu cây $INSTALL_DIR/install — chạy cài đầy đủ trước"
        exit 1
    fi
    configure_host
    run_step "Bật service" bash -c '
        systemctl enable dronebridge-4g-init.service dronebridge-netmon.service dronebridge.service
        systemctl enable dronebridge-uart-setup.service dronebridge-camera-watch.service 2>/dev/null || true
    '
    run_step "Restart DroneBridge" systemctl restart dronebridge
    echo ""
    if [ "$FAILS" -eq 0 ]; then
        echo "OK  Cấu hình tại chỗ đã khớp."
        exit 0
    fi
    echo "FAIL  $FAILS bước chưa xong. Xem dòng FAIL phía trên."
    exit 1
}

if [ "${1:-}" = "--check" ]; then
    check_and_fix
fi

# ── 1. Tạo thư mục đích ───────────────────────────────────────────────────────
echo "▶ [1/5] Tạo thư mục $INSTALL_DIR..."
if ! id -u "$RUN_USER" >/dev/null 2>&1; then
    echo "   ℹ️ Tạo user hệ thống '$RUN_USER'..."
    useradd --system --create-home --shell /usr/sbin/nologin "$RUN_USER"
fi

mkdir -p "$INSTALL_DIR"
mkdir -p "$INSTALL_DIR/Module_4G"
mkdir -p "$INSTALL_DIR/Module_Cellular"
mkdir -p "$INSTALL_DIR/third_party/sim8262"
mkdir -p "$INSTALL_DIR/etc/systemd"
mkdir -p "$INSTALL_DIR/data"

# ── 2. Copy files từ staging sang /opt/dronebridge ────────────────────────────
echo "▶ [2/5] Copy files từ staging..."
if [ -f "$STAGING_DIR/go.mod" ] && command -v go >/dev/null 2>&1; then
    need_build=0
    if [ ! -x "$STAGING_DIR/dronebridge" ]; then
        need_build=1
    elif find "$STAGING_DIR" \
        \( -path "$STAGING_DIR/vendor" -o -path "$STAGING_DIR/vendor/*" \) -prune -o \
        \( -name '*.go' -o -path "$STAGING_DIR/web/static/*" \) \
        -newer "$STAGING_DIR/dronebridge" -print -quit | grep -q .; then
        need_build=1
    fi
    if [ "$need_build" = 1 ]; then
        echo "   ℹ️ Source mới hơn binary — build lại..."
        (cd "$STAGING_DIR" && CGO_ENABLED=0 go build -mod=vendor -o "$STAGING_DIR/dronebridge" .)
    fi
fi
if [ ! -f "$STAGING_DIR/dronebridge" ]; then
    echo "❌ Thiếu binary dronebridge. Chạy: go build -mod=vendor -o dronebridge ."
    exit 1
fi
cp -f  "$STAGING_DIR/dronebridge"                  "$INSTALL_DIR/dronebridge"
if [ -f "$INSTALL_DIR/config.yaml" ]; then
    echo "   ℹ️ Giữ $INSTALL_DIR/config.yaml (không ghi đè uuid/secret của máy này)"
elif [ -f "$STAGING_DIR/config.yaml" ]; then
    cp -f "$STAGING_DIR/config.yaml" "$INSTALL_DIR/config.yaml"
fi
if [ "$INSTALL_TREE" != "$INSTALL_DIR/install" ]; then
    mkdir -p "$INSTALL_DIR/install"
    rsync -a "$INSTALL_TREE/" "$INSTALL_DIR/install/"
fi
find "$INSTALL_DIR/install" -name '*.sh' -exec chmod 755 {} \;
# Camera + landing CV (Python streamer)
if [ -d "$STAGING_DIR/Find_landing" ]; then
    mkdir -p "$INSTALL_DIR/Find_landing"
    rsync -a \
        --exclude 'camera_detected.json' \
        --exclude 'camera_config.json' \
        --exclude 'camera_config_0.json' \
        --exclude 'camera_config_1.json' \
        "$STAGING_DIR/Find_landing/" "$INSTALL_DIR/Find_landing/"
fi
cp -f  "$STAGING_DIR/Module_4G/enable_4g_auto.py"  "$INSTALL_DIR/Module_4G/"
cp -f  "$STAGING_DIR/Module_4G/connection_manager.py" "$INSTALL_DIR/Module_4G/"
cp -f  "$STAGING_DIR/Module_4G/set_4g_mode.py"     "$INSTALL_DIR/Module_4G/"   2>/dev/null || true
cp -f  "$STAGING_DIR/Module_4G/vn_carriers.py"     "$INSTALL_DIR/Module_4G/"   2>/dev/null || true
if [ -d "$STAGING_DIR/Module_Cellular" ]; then
    cp -a "$STAGING_DIR/Module_Cellular/." "$INSTALL_DIR/Module_Cellular/"
fi
if [ -d "$STAGING_DIR/third_party/sim8262" ]; then
    cp -a "$STAGING_DIR/third_party/sim8262/." "$INSTALL_DIR/third_party/sim8262/"
fi
# setup_pbr.sh đã nằm trong install/network (copy cùng cây install/).
cp -f  "$STAGING_DIR/etc/systemd/"*.service         "$INSTALL_DIR/etc/systemd/"
if [ -d "$STAGING_DIR/scripts" ]; then
    mkdir -p "$INSTALL_DIR/scripts"
    if [ -f "$STAGING_DIR/scripts/dronebridge-ctl.sh" ]; then
        cp -f "$STAGING_DIR/scripts/dronebridge-ctl.sh" "$INSTALL_DIR/scripts/"
        chmod +x "$INSTALL_DIR/scripts/dronebridge-ctl.sh"
    fi
fi
if [ -f "$STAGING_DIR/deploy-restart.sh" ]; then
    cp -f "$STAGING_DIR/deploy-restart.sh" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/deploy-restart.sh"
fi

# Find_landing: Python streamer + landing CV (preserve runtime camera_detected.json)
if [ -d "$STAGING_DIR/Find_landing" ]; then
    mkdir -p "$INSTALL_DIR/Find_landing"
    DETECTED_BAK=""
    if [ -f "$INSTALL_DIR/Find_landing/camera_detected.json" ]; then
        DETECTED_BAK="$(mktemp)"
        cp -a "$INSTALL_DIR/Find_landing/camera_detected.json" "$DETECTED_BAK"
    fi
    cp -a "$STAGING_DIR/Find_landing/." "$INSTALL_DIR/Find_landing/"
    if [ -n "$DETECTED_BAK" ] && [ -f "$DETECTED_BAK" ]; then
        cp -a "$DETECTED_BAK" "$INSTALL_DIR/Find_landing/camera_detected.json"
        rm -f "$DETECTED_BAK"
    fi
fi

if [ -d "$STAGING_DIR/docker" ]; then
    cp -a "$STAGING_DIR/docker" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/docker/"*.sh 2>/dev/null || true
fi

echo "✅ Files copied."

# ── 3. Gán quyền thực thi ─────────────────────────────────────────────────────
echo "▶ [3/5] Gán quyền..."
chmod +x "$INSTALL_DIR/dronebridge"
# KHÔNG dùng setcap trên binary — systemd User= từ chối EXEC (203/EXEC).
# MAVLink eth0: iptables exempt 10.41.10.0/24 + route table 100 (setup_pbr.sh).
chmod +x "$INSTALL_DIR/Module_4G/enable_4g_auto.py"
chmod +x "$INSTALL_DIR/Module_4G/connection_manager.py"
chmod +x "$INSTALL_DIR/Module_Cellular/modem_init.py" 2>/dev/null || true
# Ownership: toàn bộ thuộc user dịch vụ, trừ script chạy bằng root / sudo NOPASSWD.
chown -R "$RUN_USER:$GROUP_NAME" "$INSTALL_DIR"
for root_script in \
    install/network/setup_pbr.sh \
    install/network/setup_eth.sh \
    install/network/setup_lcd_i2c.sh \
    install/network/setup_mavlink_uart.sh \
    install/camera/setup_camera.sh \
    install/camera/apply_camera_overlay.sh \
    install/camera/apply_host_reboot.sh \
    install/camera/post_boot_clear.sh \
    install/camera/watch_camera_overlay.sh
do
    if [ -f "$INSTALL_DIR/$root_script" ]; then
        chown root:root "$INSTALL_DIR/$root_script"
        chmod 755 "$INSTALL_DIR/$root_script"
    fi
done

# Đồng bộ user chạy service + PBR với user thực tế trên Pi (vd. cm5drone6425)
sed -i "s/^User=.*/User=${RUN_USER}/" "$INSTALL_DIR/etc/systemd/dronebridge.service"
sed -i "s/^Group=.*/Group=${GROUP_NAME}/" "$INSTALL_DIR/etc/systemd/dronebridge.service"
sed -i "s/^DRONEBRIDGE_USER=\".*\"/DRONEBRIDGE_USER=\"${RUN_USER}\"/" "$INSTALL_DIR/install/network/setup_pbr.sh"

# ── 4. Cấu hình PBR routing table ────────────────────────────────────────────
echo "▶ [4/5] Cấu hình Policy-Based Routing (table $TABLE_ID=$TABLE_NAME)..."

# Đảm bảo các package hệ thống được cài đặt
echo "   ℹ️ Kiểm tra & cài đặt phụ tùng hệ thống..."
apt-get update -q
apt-get install -y -q \
    iproute2 \
    python3-yaml \
    python3-serial \
    libqmi-utils \
    python3-opencv \
    python3-picamera2 \
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    gstreamer1.0-plugins-ugly \
    gstreamer1.0-libav \
    gstreamer1.0-rtsp \
    libx264-dev \
    ffmpeg

# Tạo file rt_tables nếu chưa có (một số Pi OS minimal không có sẵn)
RT_TABLES="/etc/iproute2/rt_tables"
if [ ! -f "$RT_TABLES" ]; then
    echo "   ℹ️ Tạo $RT_TABLES..."
    mkdir -p /etc/iproute2
    cat > "$RT_TABLES" << 'EOF'
#
# reserved values
#
255     local
254     main
253     default
0       unspec
#
# local
#
EOF
fi

# Thêm routing tables (3-plane policy)
for entry in "101 wlan_reply" "103 eth_pixhawk"; do
    tid="${entry%% *}"
    tname="${entry##* }"
    if grep -qE "^${tid}[[:space:]]+${tname}" "$RT_TABLES"; then
        echo "   ℹ️ Table '${tname}' đã có trong rt_tables"
    else
        echo "${tid}   ${tname}" >> "$RT_TABLES"
        echo "   ✅ Thêm table '${tname}' vào rt_tables"
    fi
done

# Thêm table dronebridge nếu chưa có
if grep -qE "^${TABLE_ID}[[:space:]]+${TABLE_NAME}" "$RT_TABLES"; then
    echo "   ℹ️ Table '$TABLE_NAME' đã có trong rt_tables"
else
    echo "${TABLE_ID}   ${TABLE_NAME}" >> "$RT_TABLES"
    echo "   ✅ Thêm table '$TABLE_NAME' vào rt_tables"
fi

# ── 5. Cài đặt và enable systemd services ─────────────────────────────────────
echo "▶ [5/6] Cài đặt Systemd services..."
cp "$INSTALL_DIR/etc/systemd/dronebridge-4g-init.service" /etc/systemd/system/
cp "$INSTALL_DIR/etc/systemd/dronebridge-netmon.service"  /etc/systemd/system/
cp "$INSTALL_DIR/etc/systemd/dronebridge.service"         /etc/systemd/system/

systemctl daemon-reload
systemctl enable dronebridge-4g-init.service
systemctl enable dronebridge-netmon.service
systemctl enable dronebridge.service

echo "▶ Cấu hình máy (PBR, Ethernet PX4, LCD, camera overlay)..."
configure_host

if [ -x "$INSTALL_DIR/install/camera/install_camera_watch.sh" ]; then
    echo "▶ Camera overlay watcher..."
    bash "$INSTALL_DIR/install/camera/install_camera_watch.sh"
fi

# Dọn crontab cũ nếu có startup_4g.sh
crontab -u "$RUN_USER" -l 2>/dev/null \
    | grep -vE "startup_4g\.sh|connection_manager\.py" \
    | crontab -u "$RUN_USER" - 2>/dev/null || true

# ── [6/6] Restart stack (binary mới + go:embed UI) ───────────────────────────
echo "▶ [6/6] Restart DroneBridge..."
if [ -f "$INSTALL_DIR/install/service/dronebridge-ctl.sh" ]; then
    # shellcheck source=/dev/null
    source "$INSTALL_DIR/install/service/dronebridge-ctl.sh"
    restart_dronebridge || {
        echo "⚠️  Restart thất bại — thử thủ công: sudo systemctl restart dronebridge"
    }
else
    systemctl restart dronebridge || systemctl start dronebridge || true
fi

# ── Xong ──────────────────────────────────────────────────────────────────────
echo ""
echo "===================================================="
if [ "$FAILS" -ne 0 ]; then
    echo "Cài file xong, nhưng $FAILS bước cấu hình thất bại."
    echo "Sửa rồi chạy lại: sudo bash install/install.sh --check"
    exit 1
fi
echo "✅ Cài đặt và cấu hình DroneBridge xong."
echo "Nếu dòng phía trên báo chờ reboot, reboot một lần để kernel nhận camera/LCD."
echo ""
echo "Khởi động ngay:"
echo "  sudo systemctl start dronebridge-4g-init"
echo "  sudo systemctl start dronebridge-netmon"
echo "  sudo systemctl start dronebridge"
echo ""
echo "Xem log:"
echo "  journalctl -u dronebridge -f"
echo "  journalctl -u dronebridge-netmon -f"
echo "  ip route show table dronebridge"
echo "===================================================="

# Docker (tuỳ chọn): sudo DRONEBRIDGE_USE_DOCKER=1 bash install.sh
if [ "${DRONEBRIDGE_USE_DOCKER:-0}" = "1" ]; then
    SETUP_SH="$INSTALL_DIR/docker/setup.sh"
    [ -f "$SETUP_SH" ] || SETUP_SH="$STAGING_DIR/docker/setup.sh"
    if [ -f "$SETUP_SH" ]; then
        echo "▶ Docker mode — chạy $SETUP_SH..."
        bash "$SETUP_SH"
    else
        echo "⚠️  DRONEBRIDGE_USE_DOCKER=1 nhưng không tìm thấy docker/setup.sh"
    fi
fi
