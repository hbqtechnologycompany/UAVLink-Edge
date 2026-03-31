.PHONY: build run run-debug install deploy deploy-services \
        clean status logs restart stop help

# ─── Cấu hình ─────────────────────────────────────────────────────────────────
BINARY_NAME  = UAVLink-Edge
CONFIG       ?= config.yaml
PI_HOST      ?= pi@raspberrypi.local   # mDNS hostname
DEPLOY_DIR   ?= /opt/UAVLink-Edge
DRONE_ID     ?= drone-01

# Prefer Go mới nhất nếu có
GO ?= $(shell if [ -x /usr/local/go/bin/go ]; then echo /usr/local/go/bin/go; else echo go; fi)

# ─── Build ────────────────────────────────────────────────────────────────────

## Build binary cho host hiện tại
build:
	@echo "▶ Building $(BINARY_NAME)..."
	$(GO) build -mod=vendor -o $(BINARY_NAME) .
	@echo "✅ Build complete: $(BINARY_NAME)"

## Build cross-compile cho Raspberry Pi CM5 (ARM64)
build-pi:
	@echo "▶ Cross-compiling for Pi CM5 (linux/arm64)..."
	GOOS=linux GOARCH=arm64 $(GO) build -mod=vendor -o $(BINARY_NAME) .
	@echo "✅ ARM64 binary ready: $(BINARY_NAME)"

# ─── Run (dev trực tiếp trên Pi, KHÔNG dùng systemd) ─────────────────────────

## Chạy đầy đủ: 4G init → PBR setup → UAVLink-Edge
run: build
	@echo "▶ Starting UAVLink-Edge (manual mode)..."
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

## Chạy debug: chỉ chạy UAVLink-Edge (cần Pixhawk hoặc allow_missing=true)
run-debug: build
	@echo "▶ Starting UAVLink-Edge in DEBUG mode..."
	./$(BINARY_NAME) --config $(CONFIG) --log debug

# ─── Systemd (chạy trên Pi) ───────────────────────────────────────────────────

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
	@echo "▶ Restarting UAVLink-Edge service..."
	ssh $(PI_HOST) "sudo systemctl restart UAVLink-Edge.service"
	@echo "✅ Deploy complete"

## Cài đặt/cập nhật systemd services trên Pi
deploy-services:
	@echo "▶ Installing systemd services on $(PI_HOST)..."
	rsync -avz etc/systemd/*.service $(PI_HOST):/tmp/
	rsync -avz etc/systemd/setup_pbr.sh $(PI_HOST):$(DEPLOY_DIR)/setup_pbr.sh
	ssh $(PI_HOST) "bash -s" << 'ENDSSH'
		sudo cp /tmp/UAVLink-Edge-4g-init.service /etc/systemd/system/
		sudo cp /tmp/UAVLink-Edge-netmon.service   /etc/systemd/system/
		sudo cp /tmp/UAVLink-Edge.service          /etc/systemd/system/
		sudo chmod +x $(DEPLOY_DIR)/setup_pbr.sh
		grep -q "UAVLink-Edge" /etc/iproute2/rt_tables || \
			echo "100   UAVLink-Edge" | sudo tee -a /etc/iproute2/rt_tables
		sudo systemctl daemon-reload
		sudo systemctl enable UAVLink-Edge-4g-init.service
		sudo systemctl enable UAVLink-Edge-netmon.service
		sudo systemctl enable UAVLink-Edge.service
		echo "✅ Services installed and enabled"
ENDSSH

# ─── Quản lý services trên Pi ─────────────────────────────────────────────────

## Xem status tất cả UAVLink-Edge services
status:
	@ssh $(PI_HOST) "systemctl status 'UAVLink-Edge*' --no-pager -l" 2>/dev/null || \
		(echo "⚠️  Không SSH được $(PI_HOST), xem local:"; \
		 systemctl status 'UAVLink-Edge*' --no-pager -l 2>/dev/null || echo "Service chưa được cài.")

## Xem log real-time của tất cả services
logs:
	ssh $(PI_HOST) "journalctl -u 'UAVLink-Edge*' -f --since '5 minutes ago'"

logs-4g:
	ssh $(PI_HOST) "journalctl -u UAVLink-Edge-4g-init -f -n 50"

logs-netmon:
	ssh $(PI_HOST) "journalctl -u UAVLink-Edge-netmon -f -n 50"

logs-app:
	ssh $(PI_HOST) "journalctl -u UAVLink-Edge -f -n 100"

## Restart tất cả services (đúng thứ tự)
restart:
	ssh $(PI_HOST) "sudo systemctl restart UAVLink-Edge-4g-init && \
	                sudo systemctl restart UAVLink-Edge-netmon && \
	                sudo systemctl restart UAVLink-Edge"

## Dừng tất cả services
stop:
	ssh $(PI_HOST) "sudo systemctl stop UAVLink-Edge UAVLink-Edge-netmon UAVLink-Edge-4g-init"

## Xem routing table UAVLink-Edge trên Pi
network-status:
	@ssh $(PI_HOST) "echo '=== System route ==='; ip route show; \
	                 echo ''; echo '=== UAVLink-Edge PBR table ==='; ip route show table UAVLink-Edge 2>/dev/null || echo '(empty)'; \
	                 echo ''; echo '=== ip rule ==='; ip rule show; \
	                 echo ''; echo '=== Network status file ==='; cat /run/UAVLink-Edge/network_status.json 2>/dev/null || echo '(not found)'"

# ─── Build & Test cục bộ ──────────────────────────────────────────────────────

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -v -timeout 30s

vendor:
	$(GO) mod tidy
	$(GO) mod vendor
	@echo "✅ vendor/ updated"

clean:
	@echo "▶ Cleaning..."
	rm -f $(BINARY_NAME)
	@echo "✅ Clean complete"

# ─── Help ─────────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║          UAVLink-Edge Makefile — Available Targets        ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  BUILD                                                   ║"
	@echo "║  make build          Build binary (host arch)            ║"
	@echo "║  make build-pi       Cross-compile ARM64 cho Pi CM5      ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  RUN (thủ công trên Pi)                                  ║"
	@echo "║  make run            4G init → PBR → UAVLink-Edge         ║"
	@echo "║  make run-debug      Chỉ UAVLink-Edge, bỏ qua 4G/PBR     ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  DEPLOY (từ máy dev → Pi qua SSH)                        ║"
	@echo "║  make deploy         Build ARM64 + rsync + restart svc   ║"
	@echo "║  make deploy-services Cài/update .service files lên Pi   ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  QUẢN LÝ SERVICES (SSH vào Pi)                          ║"
	@echo "║  make status         Xem status tất cả services          ║"
	@echo "║  make logs           Log real-time tất cả services       ║"
	@echo "║  make logs-4g        Log UAVLink-Edge-4g-init             ║"
	@echo "║  make logs-netmon    Log UAVLink-Edge-netmon (PBR)        ║"
	@echo "║  make logs-app       Log UAVLink-Edge app                 ║"
	@echo "║  make restart        Restart đúng thứ tự                 ║"
	@echo "║  make stop           Dừng tất cả services                ║"
	@echo "║  make network-status Xem routing table + PBR trên Pi     ║"
	@echo "╠══════════════════════════════════════════════════════════╣"
	@echo "║  PHÁT TRIỂN                                              ║"
	@echo "║  make vet            Go vet                              ║"
	@echo "║  make test           Go test                             ║"
	@echo "║  make vendor         go mod tidy + vendor               ║"
	@echo "║  make clean          Xóa binary                          ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
