.PHONY: build run run-debug run-register run-sync install deploy deploy-services \
        clean status logs restart stop help sync-opt

# ─── Cấu hình ─────────────────────────────────────────────────────────────────
BINARY_NAME  = dronebridge
CONFIG       ?= config.yaml
PI_HOST      ?= pi@raspberrypi.local   # mDNS hostname — không đổi dù DHCP thay IP
DEPLOY_DIR   ?= /opt/dronebridge
DRONE_ID     ?= drone-01
SYNC_DIR     ?= /run/dronebridge/sync

# Prefer Go mới nhất nếu có
GO ?= $(shell if [ -x /usr/local/go/bin/go ]; then echo /usr/local/go/bin/go; else echo go; fi)

# ─── Build ────────────────────────────────────────────────────────────────────

## Build binary cho host hiện tại (dev/test trên Pi trực tiếp)
build:
	@echo "▶ Building $(BINARY_NAME)..."
	$(GO) build -mod=vendor -o $(BINARY_NAME) .
	@echo "✅ Build complete: $(BINARY_NAME)"

## Build cross-compile cho Raspberry Pi CM5 (ARM64) — chạy từ máy dev
build-pi:
	@echo "▶ Cross-compiling for Pi CM5 (linux/arm64)..."
	GOOS=linux GOARCH=arm64 $(GO) build -mod=vendor -o $(BINARY_NAME) .
	@echo "✅ ARM64 binary ready: $(BINARY_NAME)"

# ─── Run (dev trực tiếp trên Pi, KHÔNG dùng systemd) ─────────────────────────

## Chạy đầy đủ: 4G init → PBR setup → dronebridge
## Dùng khi test thủ công, KHÔNG nên dùng khi systemd services đang chạy
run: build
	@echo "▶ Starting DroneBridge (manual mode, without systemd)..."
	@bash -c 'set -e; \
		echo "[1/3] 4G Hardware Init..."; \
		sudo python3 ./Module_4G/enable_4g_auto.py || echo "⚠️  4G init skipped"; \
		echo "[2/3] PBR Routing Setup..."; \
		sudo bash ./etc/systemd/setup_pbr.sh; \
		echo "[3/3] Network Monitor (background)..."; \
		sudo python3 ./Module_4G/connection_manager.py > /tmp/netmon.log 2>&1 & NM_PID=$$!; \
		trap "kill $$NM_PID 2>/dev/null || true" EXIT INT TERM; \
		echo "    netmon PID=$$NM_PID, log=/tmp/netmon.log"; \
		echo "▶ Starting $(BINARY_NAME) --config $(CONFIG)..."; \
		exec ./$(BINARY_NAME) --config $(CONFIG)'

## Chạy debug: bỏ qua 4G + PBR, chỉ chạy dronebridge (cần Pixhawk hoặc allow_missing=true)
run-debug: build
	@echo "▶ Starting DroneBridge in DEBUG mode (no 4G/PBR setup)..."
	./$(BINARY_NAME) --config $(CONFIG) --log debug

## Chạy đăng ký drone lần đầu (lấy secret key từ server)
run-register: build
	@echo "▶ Registering drone with fleet server..."
	@echo "   Config: $(CONFIG)"
	./$(BINARY_NAME) --config $(CONFIG) --register

## Chạy multi-drone sync mode
run-sync: build
	@echo "▶ Starting DroneBridge in sync mode (drone-id=$(DRONE_ID))..."
	@mkdir -p $(SYNC_DIR)
	./$(BINARY_NAME) --config $(CONFIG) \
		--sync-dir $(SYNC_DIR) \
		--drone-id $(DRONE_ID)

# ─── Systemd (chạy trên Pi) ───────────────────────────────────────────────────

## Đồng bộ repo → /opt/dronebridge + build Go + restart Docker (trên Pi)
sync-opt:
	sudo bash scripts/sync-runtime.sh --restart

## Chỉ sync Python/scripts, không build Go
sync-opt-py:
	sudo bash scripts/sync-runtime.sh --no-build --restart

## Deploy binary + config lên Pi và restart services
deploy: build-pi
	@echo "▶ Deploying to $(PI_HOST):$(DEPLOY_DIR)..."
	ssh $(PI_HOST) "mkdir -p $(DEPLOY_DIR)/Module_4G $(DEPLOY_DIR)/etc/systemd $(DEPLOY_DIR)/docs"
	rsync -avz --progress \
		$(BINARY_NAME) \
		$(CONFIG) \
		Module_4G/enable_4g_auto.py \
		Module_4G/connection_manager.py \
		Module_4G/set_4g_mode.py \
		$(PI_HOST):$(DEPLOY_DIR)/
	rsync -avz etc/systemd/ $(PI_HOST):$(DEPLOY_DIR)/etc/systemd/
	@echo "▶ Restarting dronebridge service..."
	ssh $(PI_HOST) "sudo systemctl restart dronebridge.service"
	@echo "✅ Deploy complete"

## Cài đặt/cập nhật systemd services trên Pi (chạy 1 lần đầu hoặc khi .service thay đổi)
deploy-services:
	@echo "▶ Installing systemd services on $(PI_HOST)..."
	rsync -avz etc/systemd/*.service $(PI_HOST):/tmp/
	rsync -avz etc/systemd/setup_pbr.sh $(PI_HOST):$(DEPLOY_DIR)/setup_pbr.sh
	ssh $(PI_HOST) "bash -s" <<-'ENDSSH'
		sudo cp /tmp/dronebridge-4g-init.service /etc/systemd/system/
		sudo cp /tmp/dronebridge-netmon.service   /etc/systemd/system/
		sudo cp /tmp/dronebridge.service          /etc/systemd/system/
		sudo chmod +x $(DEPLOY_DIR)/setup_pbr.sh
		grep -q "dronebridge" /etc/iproute2/rt_tables || \
			echo "100   dronebridge" | sudo tee -a /etc/iproute2/rt_tables
		sudo systemctl daemon-reload
		sudo systemctl enable dronebridge-4g-init.service
		sudo systemctl enable dronebridge-netmon.service
		sudo systemctl enable dronebridge.service
		echo "✅ Services installed and enabled"
	ENDSSH

# ─── Quản lý services trên Pi ─────────────────────────────────────────────────

## Xem status tất cả dronebridge services
status:
	@ssh $(PI_HOST) "systemctl status 'dronebridge*' --no-pager -l" 2>/dev/null || \
		(echo "⚠️  Không SSH được $(PI_HOST), xem local:"; \
		 systemctl status 'dronebridge*' --no-pager -l 2>/dev/null || echo "Service chưa được cài.")

## Xem log real-time của tất cả services
logs:
	ssh $(PI_HOST) "journalctl -u 'dronebridge*' -f --since '5 minutes ago'"

## Xem log của service cụ thể (make logs-4g / logs-netmon / logs-app)
logs-4g:
	ssh $(PI_HOST) "journalctl -u dronebridge-4g-init -f -n 50"

logs-netmon:
	ssh $(PI_HOST) "journalctl -u dronebridge-netmon -f -n 50"

logs-app:
	ssh $(PI_HOST) "journalctl -u dronebridge -f -n 100"

## Restart tất cả services (đúng thứ tự)
restart:
	ssh $(PI_HOST) "sudo systemctl restart dronebridge-4g-init && \
	                sudo systemctl restart dronebridge-netmon && \
	                sudo systemctl restart dronebridge"

## Dừng tất cả services
stop:
	ssh $(PI_HOST) "sudo systemctl stop dronebridge dronebridge-netmon dronebridge-4g-init"

## Xem routing table DroneBridge trên Pi
network-status:
	@ssh $(PI_HOST) "echo '=== System route ==='; ip route show; \
	                 echo ''; echo '=== DroneBridge PBR table ==='; ip route show table dronebridge 2>/dev/null || echo '(empty)'; \
	                 echo ''; echo '=== ip rule ==='; ip rule show; \
	                 echo ''; echo '=== Network status file ==='; cat /run/dronebridge/network_status.json 2>/dev/null || echo '(not found)'"

# ─── Build & Test cục bộ (không cần Pi) ──────────────────────────────────────

## Kiểm tra syntax Go (không build)
vet:
	$(GO) vet ./...

## Chạy unit tests
test:
	$(GO) test ./... -v -timeout 30s

## Tidy + vendor
vendor:
	$(GO) mod tidy
	$(GO) mod vendor
	@echo "✅ vendor/ updated"

## Xóa binary
clean:
	@echo "▶ Cleaning..."
	rm -f $(BINARY_NAME)
	@echo "✅ Clean complete"

# ─── Help ─────────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║          DroneBridge Makefile — Available Targets        ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  BUILD                                                   ║"
	@echo "║  make build          Build binary (host arch)            ║"
	@echo "║  make build-pi       Cross-compile ARM64 cho Pi CM5      ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  RUN (thủ công trên Pi, không dùng systemd)              ║"
	@echo "║  make run            4G init → PBR → dronebridge         ║"
	@echo "║  make run-debug      Chỉ dronebridge, bỏ qua 4G/PBR     ║"
	@echo "║  make run-register   Đăng ký drone lần đầu (--register)  ║"
	@echo "║  make run-sync       Multi-drone sync mode               ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  make sync-opt         Repo → /opt/dronebridge + restart (Pi) ║"
	@echo "║  make sync-opt-py      Chỉ Python Find_landing + restart      ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  DEPLOY (từ máy dev → Pi qua SSH)                        ║"
	@echo "║  make deploy         Build ARM64 + rsync + restart svc   ║"
	@echo "║  make deploy-services Cài/update .service files lên Pi   ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  QUẢN LÝ SERVICES (SSH vào Pi)                          ║"
	@echo "║  make status         Xem status tất cả services          ║"
	@echo "║  make logs           Log real-time tất cả services       ║"
	@echo "║  make logs-4g        Log dronebridge-4g-init             ║"
	@echo "║  make logs-netmon    Log dronebridge-netmon (PBR)        ║"
	@echo "║  make logs-app       Log dronebridge app                 ║"
	@echo "║  make restart        Restart đúng thứ tự                 ║"
	@echo "║  make stop           Dừng tất cả services                ║"
	@echo "║  make network-status Xem routing table + PBR trên Pi     ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  PHÁT TRIỂN                                              ║"
	@echo "║  make vet            Go vet                              ║"
	@echo "║  make test           Go test                             ║"
	@echo "║  make vendor         go mod tidy + vendor               ║"
	@echo "║  make clean          Xóa binary                          ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  BIẾN MÔI TRƯỜNG                                        ║"
	@echo "║  PI_HOST=pi@192.x.x.x  Target Pi (default: pi@raspberry.local) ║"
	@echo "║  CONFIG=config.yaml    Config file                       ║"
	@echo "║  DEPLOY_DIR=/opt/...   Thư mục trên Pi                  ║"
	@echo "║  DRONE_ID=drone-01     ID cho sync mode                  ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
