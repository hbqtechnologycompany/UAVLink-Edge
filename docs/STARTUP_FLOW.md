# UAVLink-Edge Startup Flow

> Phiên bản: 4.0 — Cập nhật theo code thực tế (`main.go`)  
> Cập nhật: 2026-03-27

---

## Toàn cảnh hệ thống (Boot Order)

```
Pi CM5 Boot
    │
    ├─ UAVLink-Edge-4g-init.service  (oneshot, root)
    │   └─ enable_4g_auto.py        → wwan0 UP + IP
    │
    ├─ UAVLink-Edge-netmon.service   (after 4g-init, root)
    │   ├─ setup_pbr.sh             → ip rule + iptables mark
    │   └─ connection_manager.py   → PBR routing 4G/WiFi loop
    │
    └─ UAVLink-Edge.service          (after netmon, user pi)
        └─ main.go                  → MAVLink + Authentication
```

---

## Startup Flow — `main.go` (UAVLink-Edge binary)

```
┌─────────────────────────────────────────────────────────────────────┐
│  UAVLink-Edge --config config.yaml                                   │
└────────────────────────────┬────────────────────────────────────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │  Parse CLI flags             │
              │  --config  --log             │
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
              └──────────────────────┬───────────────────┘
                                     │
                                     ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 0: Create listener node ONLY       │
                             │  forwarder.NewListener(cfg)              │
                             │  web.InitMAVLinkBridge(listenerNode)     │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 1: Chờ Pixhawk Heartbeat           │
                             │                                          │
                             │  goroutine đọc listenerNode.Events()     │
                             │  timeout = cfg.Ethernet.                 │
                             │           PixhawkConnectionTimeout        │
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
                             │  forwarder.New(                          │
                             │    cfg, authClient,                      │
                             │    listenerNode)    ← reuse từ STEP 0    │
                             │                                          │
                             │  senderNode → UDPClient{server:14550}    │
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
                             │    → authenticate() [TCP :5770]          │
                             │    → go keepaliveLoop()                  │
                             │                                          │
                             │  Đợi tối đa 10s (100 × 100ms poll)      │
                             │  ⚠️ Timeout → Warn + tiếp tục            │
                             └──────────────────────┬───────────────────┘
                                                    │
                                                    ▼
                             ┌──────────────────────────────────────────┐
                             │  STEP 5: Start Web Server                │
                             │                                          │
                             │  web.StartServer(port, authClient, uuid) │
                             │  fwd.SetAuthClient(authClient)           │
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

## Log Timeline — Khởi động thành công

```
T+0.0s  Loading configuration from config.yaml
T+0.0s  [STARTUP] Creating MAVLink listener for Pixhawk...
T+0.0s  [STARTUP] ⏳ Waiting for Pixhawk heartbeat... (timeout: 30s)
T+1.2s  [PIXHAWK_CONNECTED] ✅ First heartbeat received from Pixhawk (SysID: 1)
T+1.2s  [STARTUP] ✅ Pixhawk connected successfully! System ID: 1
T+1.2s  [STARTUP] ✈️  Creating forwarder with correct System ID...
T+1.2s  [STARTUP] Starting forwarder...
T+1.2s  [STARTUP] ✈️  Now proceeding with server authentication...
T+1.3s  [AUTH] Authenticating with server...
T+2.1s  ✅ Auth client authenticated with router
T+2.1s  [WEB] Starting web server on :8080
T+2.2s  [IP_MONITOR] Initial IP: 10.100.x.x — Binding sender node
T+2.2s  [MAVLINK_HB] ✓ First MAVLink session heartbeat sent (ID 42999)
T+2.2s  MAVLink forwarder running.
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
```

### Debug (không có Pixhawk thực tế)
```yaml
ethernet:
  allow_missing_pixhawk: true
  pixhawk_connection_timeout: 5

auth:
  session_heartbeat_frequency: 0   # Tắt heartbeat → CẢNH BÁO: vô hiệu IP Roaming
```

> ⚠️ **Lưu ý**: Đặt `session_heartbeat_frequency: 0` sẽ tắt MAVLink 42999 heartbeat.
> Server sẽ không tự cập nhật IP mới khi Drone chuyển mạng (4G ↔ WiFi).
