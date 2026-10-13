# DroneBridge Startup Flow

> Phiên bản: 3.0 — Cập nhật theo code thực tế (`main.go`)  
> Cập nhật: 2026-03-23

---

## Toàn cảnh hệ thống (Boot Order)

```
Pi CM5 Boot
    │
    ├─ dronebridge-4g-init.service  (oneshot, root)
    │   └─ enable_4g_auto.py        → wwan0 UP + IP
    │
    ├─ dronebridge-netmon.service   (after 4g-init, root)
    │   ├─ setup_pbr.sh             → ip rule + iptables mark
    │   └─ connection_manager.py   → PBR routing 4G/WiFi loop
    │
    └─ dronebridge.service          (after netmon, user pi)
        └─ main.go                  → MAVLink + Auth + VPN
```

---

## Startup Flow — `main.go` (dronebridge binary)

```
┌─────────────────────────────────────────────────────────────────────┐
│  dronebridge --config config.yaml                                   │
└────────────────────────────┬────────────────────────────────────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │  Parse CLI flags             │
              │  --config  --log             │
              │  --register --sync-dir       │
              │  --drone-id                  │
              └──────────────┬───────────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │  config.Load(config.yaml)    │
              │  logger.SetLevel()           │
              │  logger.InstallStdLogBridge()│
              └──────────────┬───────────────┘
                             │
                             ▼
              ┌──────────────────────────────────────────┐
              │  auth.NewClient(host, port, uuid, ...)   │
              │  authClient.SetVPNRouterIP(...)          │
              │                                          │
              │  [if VPN.Enabled]                        │
              │    vpn.NewManager(configFile)            │
              │    authClient.SetVPNManager(vpnMgr)      │
              └──────────────────────┬───────────────────┘
                                     │
                       ┌─────────────┴──────────────┐
                       │ --register flag?            │
                       │                            │
                    YES│                         NO │
                       ▼                            │
         ┌─────────────────────────┐                │
         │  authClient.Register()  │                │
         │  Save .drone_secret     │                │
         │  os.Exit(0)             │                │
         └─────────────────────────┘                │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 0: Create listener node ONLY       │
                             │  forwarder.NewListener(cfg)              │
                             │  web.InitMAVLinkBridge(listenerNode)     │
                             │  (senderNode chưa có — chưa biết SysID) │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 1: Chờ Pixhawk Heartbeat           │
                             │                                          │
                             │  goroutine đọc listenerNode.Events()     │
                             │  timeout = cfg.Ethernet.                 │
                             │           PixhawkConnectionTimeout        │
                             │                                          │
                             │  Lọc SysID 255 (Simulator/GCS) → SKIP   │
                             └──────────────────────┬───────────────────┘
                                                    │
                                          ┌─────────┴──────────┐
                                          │                    │
                                          ▼                    ▼
                                   ✅ HB received        ❌ Timeout
                                   pixhawkSysID=N        pixhawkConnected=false
                                          │                    │
                                          │          ┌─────────┴──────────────┐
                                          │          │ AllowMissingPixhawk?   │
                                          │          └──────┬─────────────────┘
                                          │                 │
                                          │         ┌───────┴───────┐
                                          │         │               │
                                          │       true            false
                                          │         │               │
                                          │         ▼               ▼
                                          │     ⚠️ WARN         ❌ FATAL
                                          │     Continue        os.Exit(1)
                                          │         │
                                          └────┬────┘
                                               │
                                               ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 2: Tạo full Forwarder              │
                             │                                          │
                             │  forwarder.NewWithVPN(                   │
                             │    cfg, authClient,                      │
                             │    listenerNode,   ← reuse từ STEP 0    │
                             │    vpnMgr)                               │
                             │                                          │
                             │  [if VPN already running]               │
                             │    Bind senderNode → vpnMgr.DialUDP()   │
                             │  [else]                                  │
                             │    senderNode → UDPClient{server:14550} │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  [if --sync-dir]                         │
                             │  fwd.SetSyncManager(syncManager)         │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 3: Start Forwarder                 │
                             │  fwd.Start()                             │
                             │                                          │
                             │  Goroutines launched:                    │
                             │  • receiveAndForward (Pixhawk → Server)  │
                             │  • receiveFromServer  (Server → Pixhawk) │
                             │  • sendMavlinkSessionHeartbeat (ID 42999)│
                             │  • monitorIPChange (every 5s)            │
                             │  • reportBandwidth  (every 1s)           │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 4: Authenticate với server         │
                             │                                          │
                             │  authClient.Start()                      │
                             │    → LoadSecret(.drone_secret)           │
                             │    → authenticate() [TCP :5770]          │
                             │    → go keepaliveLoop()                  │
                             │                                          │
                             │  Đợi tối đa 10s (100 × 100ms poll)      │
                             │  ⚠️ Timeout → Warn + tiếp tục            │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 4.5: VPN Startup                   │
                             │                                          │
                             │  [if VPN.Enabled]                        │
                             │    Fast path: vpnMgr.Start()            │
                             │    ├─ vpn_config.json exists → UP ✅    │
                             │    └─ No config → Slow path:            │
                             │         authClient.RequestVPN            │
                             │         Provisioning()  [TCP 0xB0]      │
                             │         WaitForVPN(10s)                  │
                             │         vpnMgr.Start()                   │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 5: Start Web Server                │
                             │                                          │
                             │  web.StartServer(port, authClient, uuid) │
                             │  fwd.SetAuthClient(authClient)           │
                             │    → wire OnNetworkError callback        │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  [if syncManager]                        │
                             │  syncManager.MarkReady()                 │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  [if Camera.Enabled && Camera.AutoStart] │
                             │  cameraManager.Start()                   │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  ✅ FULLY OPERATIONAL                    │
                             │                                          │
                             │  signal.Notify(SIGINT, SIGTERM)         │
                             │  <-sigCh  (block forever)               │
                             └──────────────────────┬───────────────────┘
                                                    │ Ctrl+C / SIGTERM
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  Graceful Shutdown                       │
                             │  cameraManager.Stop()                    │
                             │  fwd.Stop()                              │
                             │    → authClient.Stop()                   │
                             │    → listenerNode.Close()                │
                             │    → senderNode.Close()                  │
                             └──────────────────────────────────────────┘
```

---

## Goroutines chạy khi OPERATIONAL

| Goroutine | Package | Mô tả |
|---|---|---|
| `receiveAndForward` | forwarder | Đọc Pixhawk events → filter → gửi server |
| `receiveFromServer` | forwarder | Đọc server events → gửi Pixhawk |
| `sendMavlinkSessionHeartbeat` | forwarder | MAVLink ID 42999, tần suất `session_heartbeat_frequency` Hz |
| `monitorIPChange` | forwarder | Mỗi 5s: kiểm tra IP, rebuild senderNode nếu đổi/mất |
| `reportBandwidth` | forwarder | Mỗi 1s: log thống kê packet |
| `keepaliveLoop` | auth | Gửi SESSION_REFRESH mỗi `keepalive_interval` giây |
| HTTP server | web | `http.ListenAndServe(:8080)` |
| processParamValues | web | Drain param channel → cập nhật cache |
| Camera monitor | web | Giám sát process camera_streamer.py |

---

## VPN Startup — Decision Tree

```
cfg.VPN.Enabled = true
        │
        ▼
vpnMgr.Start() ─── vpn_config.json tồn tại? ─── YES → ✅ Tunnel UP (<1s)
        │                                                 (WG keepalive tự bắt tay)
        └── NO (file missing/corrupt)
                │
                ▼
        authClient.RequestVPNProvisioning()  [TCP 0xB0 → server]
                │
         ┌──────┴──────┐
         │             │
      Success       Fail/Timeout
         │             │
         ▼             ▼
   WaitForVPN(10s)  ⚠️ WARN, continue
         │
         ▼
   vpnMgr.Start()  ← lần này có config
         │
         ▼
   ✅ Tunnel UP
```

---

## IP Change Recovery Flow (`monitorIPChange`)

```
Mỗi 5s (hoặc forceCheckCh khi auth error):
        │
        ▼
getCurrentIP()
        │
   IP = "" (mạng mất)
        │
        ├── networkWasDown=false → SET isHealthy=false, networkWasDown=true
        │   (Log WARN: "Network lost")
        │
        └── networkWasDown=true → Debug "still down", skip
                │
        Khi IP trở lại (!= ""):
                │
           networkWasDown=true → RESTORE (rebuild sender + ForceReconnect auth)
                │
           networkWasDown=false + IP != previousIP → REBUILD (ip changed)
                │
           networkWasDown=false + IP == previousIP → no-op
```

---

## Log Timeline — Khởi động thành công (với VPN)

```
T+0.0s  Loading configuration from config.yaml
T+0.0s  [VPN] VPN provisioning ENABLED
T+0.0s  [STARTUP] Creating MAVLink listener for Pixhawk...
T+0.0s  [STARTUP] ⏳ Waiting for Pixhawk heartbeat... (timeout: 30s)
T+1.2s  [PIXHAWK_CONNECTED] ✅ First heartbeat received from Pixhawk (SysID: 1)
T+1.2s  [STARTUP] ✅ Pixhawk connected successfully! System ID: 1
T+1.2s  [STARTUP] ✈️  Creating forwarder with correct System ID...
T+1.2s  [STARTUP] Starting forwarder...
T+1.2s  [STARTUP] ✈️  Now proceeding with server authentication...
T+1.3s  [AUTH] Authenticating with server...
T+2.1s  ✅ Auth client authenticated with router
T+2.1s  [VPN] ✅ VPN tunnel started from saved config (no provisioning needed)
T+2.1s  [WEB] Starting web server on :8080
T+2.2s  [IP_MONITOR] Initial IP: 10.8.0.x — Binding sender node
T+2.2s  [MAVLINK_HB] ✓ First MAVLink session heartbeat sent (ID 42999)
T+2.2s  MAVLink forwarder running.
```

---

## Log Timeline — Lần đầu tiên (VPN chưa có config)

```
T+0.0s  [VPN] VPN provisioning ENABLED
T+2.1s  ✅ Auth client authenticated with router
T+2.1s  [VPN] No saved VPN config (...) — requesting provisioning from Router...
T+2.6s  [VPN] 📡 Requesting VPN provisioning from router...
T+3.1s  [VPN] ✅ VPN provisioned! IP=10.8.0.x, Server=...
T+3.1s  [VPN] ✅ VPN tunnel provisioned and started
T+3.1s  [WEB] Starting web server on :8080
```

---

## Log Timeline — Mạng mất và phục hồi

```
          [IP_MONITOR] ⚠️  Network lost (no valid IP) - marking unhealthy
          [NETWORK] Network error detected via Auth Client - Marking unhealthy
          ... (5s cycles, network still down)
          [IP_MONITOR] Network still down, waiting...
          [IP_MONITOR] ✅ Network restored! IP=10.8.0.x (prev=10.8.0.x)
          [IP_MONITOR] Sender node rebuilt (network-restored)
          [RECONNECT] Attempting to reconnect TCP to server:5770
          [RECONNECT] ✓ TCP reconnected successfully from local IP: ...
          [REFRESH] ✅ TCP reconnected, will retry refresh next cycle
```

---

## Configuration Impact

### Production
```yaml
ethernet:
  allow_missing_pixhawk: false
  pixhawk_connection_timeout: 30

auth:
  keepalive_interval: 30
  session_heartbeat_frequency: 1.0

vpn:
  enabled: true
  config_file: vpn_config.json
```

### Debug (không có Pixhawk thực tế)
```yaml
ethernet:
  allow_missing_pixhawk: true
  pixhawk_connection_timeout: 5

auth:
  session_heartbeat_frequency: 0   # Tắt heartbeat → CẢNH BÁO: vô hiệu IP Roaming

vpn:
  enabled: false
```

> ⚠️ **Lưu ý**: Đặt `session_heartbeat_frequency: 0` sẽ tắt MAVLink 42999 heartbeat.
> Server sẽ không tự cập nhật IP mới khi Drone chuyển mạng (4G ↔ WiFi).
> Chỉ dùng trong môi trường debug cố định.
