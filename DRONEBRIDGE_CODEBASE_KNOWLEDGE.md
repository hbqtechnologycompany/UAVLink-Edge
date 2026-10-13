# Pi_CM5_DroneBridgeService — Codebase Knowledge

> Internal reference for developers maintaining or extending the DroneBridge companion computer software.
> Covers module structure, key data flows, design decisions, and pitfalls.

---

## Table of Contents

1. [Module Summary](#module-summary)
2. [Directory Structure](#directory-structure)
3. [Dependencies](#dependencies)
4. [Package Reference](#package-reference)
   - [cmd/dronebridge](#cmddronebridge--entrypoint)
   - [config](#config--configuration-loader)
   - [auth](#auth--authentication-client)
   - [forwarder](#forwarder--mavlink-bridge)
   - [mavlink_custom](#mavlink_custom--custom-dialect)
   - [web](#web--http-server--mavlink-bridge)
   - [metrics](#metrics--telemetry)
   - [logger](#logger--logging-facade)
5. [Critical Data Flows](#critical-data-flows)
   - [Startup Flow](#startup-flow)
   - [Auth Registration Flow](#auth-registration-flow)
   - [Auth Session Flow](#auth-session-flow)
   - [MAVLink Forward Flow (Pixhawk → Server)](#mavlink-forward-flow-pixhawk--server)
   - [MAVLink Downlink Flow (Server → Pixhawk)](#mavlink-downlink-flow-server--pixhawk)
   - [Session Heartbeat Flow](#session-heartbeat-flow)
   - [IP Change / Reconnect Flow](#ip-change--reconnect-flow)
6. [Key Design Decisions](#key-design-decisions)
7. [State Management](#state-management)
8. [Concurrency Model](#concurrency-model)
9. [Protocol Specification](#protocol-specification)
   - [Binary Auth Protocol](#binary-auth-protocol)
   - [Custom MAVLink Message 42999](#custom-mavlink-message-42999)
10. [Configuration Schema](#configuration-schema)
11. [File Layout (Runtime)](#file-layout-runtime)
12. [Common Pitfalls](#common-pitfalls)
13. [Extension Points](#extension-points)

---

## Module Summary

| Field | Value |
|-------|-------|
| Go module | `DroneBridge` |
| Go version | 1.23 |
| Primary MAVLink lib | `github.com/bluenviron/gomavlib/v3 v3.2.1` |
| Config format | YAML (`gopkg.in/yaml.v3`) |
| Binary target | Linux ARM64 (Raspberry Pi CM5) |
| Server endpoint | `45.117.171.237` TCP:5770 (auth) + Public UDP |
| VPN endpoint | `10.8.0.1` UDP:14550 (MAVLink via tunnel) |
| Routing Mode | Policy-Based Routing (Table 100) — SSH via WiFi always safe |
| OS Services | Systemd (dronebridge, netmon, 4g-init) |

---

## Directory Structure

```
Pi_CM5_DroneBridgeService/
├── cmd/
│   └── dronebridge/
│       └── main.go         ← Entry point; orchestrates all packages
├── auth/
│   ├── client.go           ← TCP auth client (registration, session, keepalive)
│   ├── client_apikey.go    ← API key request/revoke/status over same TCP conn
│   ├── hmac.go             ← HMAC-SHA256 computation
│   ├── protocol.go         ← All binary message type constants + structs
│   └── secret_storage.go   ← .drone_secret file persistence
├── config/
│   └── config.go           ← YAML config structs + Load() + Validate() + GetAddress()
├── forwarder/
│   └── forwarder.go        ← MAVLink bridging (Pixhawk ↔ Router), IP monitoring
├── mavlink_custom/
│   └── custom.go           ← MessageSessionHeartbeat (ID 42999) + combined dialect
├── vpn/
│   └── manager.go          ← WireGuard userspace manager (Go netstack)
├── web/
│   ├── server.go           ← HTTP server, MAVLink parameter bridge, API endpoints
│   └── static/             ← Embedded HTML/JS/CSS for dashboard + param editor
├── metrics/
│   └── metrics.go          ← Global metrics singleton; snapshot for /api/status
├── logger/
│   └── logger.go           ← Leveled logger facade over stdlib log
├── etc/
│   └── systemd/            ← Systemd unit files + setup_pbr.sh
├── Module_4G/
│   ├── enable_4g_auto.py    ← Hardware/QMI initialization
│   ├── connection_manager.py ← PBR switcher loop (4G/WiFi)
│   └── set_4g_mode.py       ← AT command utility
├── docs/
│   ├── STARTUP_FLOW.md      ← Detailed startup sequence
│   └── NETWORK_MANAGEMENT.md ← User guide for 4G/WiFi/PBR
├── install.sh               ← All-in-one local installer
├── deploy.sh                ← Ubuntu-to-Pi deployer script
├── config.yaml              ← Production configuration
├── go.mod
└── go.sum
```

---

## Dependencies

```
github.com/bluenviron/gomavlib/v3 v3.2.1   — MAVLink node, dialects, serial/UDP endpoints
gopkg.in/yaml.v3 v3.0.1                    — YAML config parsing
github.com/creack/goselect v0.1.2          — (indirect) serial port select
github.com/pion/logging v0.2.2             — (indirect) pion UDP transport
github.com/pion/transport/v2 v2.2.10       — (indirect) pion UDP transport
go.bug.st/serial v1.6.2                    — (indirect) serial port driver
golang.org/x/net v0.33.0                   — (indirect) network utilities
golang.org/x/sys v0.28.0                   — (indirect) syscall wrappers
```

---

## Package Reference

### `cmd/dronebridge` — Entrypoint

**File**: `cmd/dronebridge/main.go` (or root `main.go`; ~225 lines)

**Responsibilities**:
- Parse CLI flags: `--config`, `--register`
- Orchestrate startup sequence (see [Startup Flow](#startup-flow))
- Block on `os.Signal` for graceful shutdown (SIGINT / SIGTERM)

**Key globals / variables**:
```go
cfg        *config.Config
authClient *auth.Client
fwd        *forwarder.Forwarder
```

**Notable logic**:
- `--register` flag triggers one-time `authClient.Register()` and exits
- Creates `listenerNode` via `forwarder.NewListener(cfg)` to start hearing Pixhawk *before* auth
- Calls `fwd.WaitForPixhawkConnection(30 * time.Second)` — blocks until first HEARTBEAT
- `web.InitMAVLinkBridge(fwd.GetListenerNode())` wires the gomavlib node into the HTTP parameter bridge
- Auth is started *after* Pixhawk is confirmed, ensuring `OutSystemID` is correct

---

### `config` — Configuration Loader

**File**: `config/config.go` (~274 lines)

**Key structs**:

```go
type Config struct {
    Log      LogConfig
    Auth     AuthConfig
    Network  NetworkConfig
    Ethernet EthernetConfig
    Web      WebConfig
    Camera   CameraConfig
    VPN      VPNConfig
}

type VPNConfig struct {
    Enabled        bool   `yaml:"enabled"`
    ConfigFile     string `yaml:"config_file"`
    ServerEndpoint string `yaml:"server_endpoint"`
    ServerPubKey   string `yaml:"server_pub_key"`
    VPNPort        int    `yaml:"vpn_port"`
    RouterVPNIP    string `yaml:"router_vpn_ip"`
}

type AuthConfig struct {
    Enabled                    bool
    Host                       string
    Port                       int
    UUID                       string
    SharedSecret               string  `yaml:"shared_secret"`
    KeepaliveInterval          int     `yaml:"keepalive_interval"`
    SessionHeartbeatFrequency  float64 `yaml:"session_heartbeat_frequency"`
}

type NetworkConfig struct {
    LocalListenPort int    `yaml:"local_listen_port"`   // 14540
    TargetHost      string `yaml:"target_host"`
    TargetPort      int    `yaml:"target_port"`         // 14550
    Protocol        string                               // "udp"
    ConnectionType  string `yaml:"connection_type"`     // "serial" | "ethernet"
    SerialPort      string `yaml:"serial_port"`         // /dev/ttyAMA0
    SerialBaud      int    `yaml:"serial_baud"`         // 57600
    Mode            string `yaml:"mode"`                // "4g_only", "wifi_only", "prefer_4g"
    FallbackDelay   int    `yaml:"fallback_delay"`      // seconds, delay before auto-switching to WiFi (default: 300)
}

type EthernetConfig struct {
    Interface                string
    LocalIP                  string  `yaml:"local_ip"`         // 10.41.10.1
    BroadcastIP              string  `yaml:"broadcast_ip"`
    PixhawkIP                string  `yaml:"pixhawk_ip"`       // 10.41.10.2
    AutoSetup                bool    `yaml:"auto_setup"`
    Subnet                   string                            // "24"
    AllowMissingPixhawk      bool    `yaml:"allow_missing_pixhawk"`
    PixhawkConnectionTimeout int     `yaml:"pixhawk_connection_timeout"` // seconds
}
```

**`cfg.GetAddress()`**: Returns `"host:port"` string for the senderNode UDP endpoint.

---

### `auth` — Authentication Client

**File**: `auth/client.go` (~915 lines)

**Struct**:
```go
type Client struct {
    host              string
    port              int
    droneUUID         string
    sharedSecret      string   // for --register only
    secretKey         string   // from .drone_secret
    sessionToken      string   // in-memory after auth
    sessionExpiry     time.Time
    conn              net.Conn
    tcpMu             sync.Mutex
    stopCh            chan struct{}
    apiKeyStatusCh    chan APIKeyStatusResponse
    apiKeyDeleteCh    chan bool
    // VPN Provisioning
    vpnManager        *vpn.Manager
    vpnReadyCh        chan struct{}
}
```

**Key methods**:

| Method | Description |
|--------|-------------|
| `NewClient(host, port, uuid, sharedSecret, keepalive)` | Create client; resolves UUID priority chain |
| `Start()` | Load `.drone_secret` → `authenticate()` → `go keepaliveLoop()` |
| `Register()` | One-time REGISTER_INIT→ACK using shared_secret |
| `authenticate()` | AUTH_INIT→SESSION_ACK using device secret_key |
| `keepaliveLoop()` | Sends SESSION_REFRESH every `keepalive_interval` seconds |
| `ForceReconnect()` | Close TCP conn → triggers reconnect on next keepalive cycle |
| `Stop()` | Close stopCh to terminate background goroutine |
| `GetSessionInfo()` | Returns `(tokenHex, isActive, expiresAt)` |
| `IsAuthenticated()` | Returns `true` if valid token in memory |
| `RequestAPIKey(hours)` | Sends 0x20 API_KEY_REQUEST over TCP |
| `RevokeAPIKey()` | Sends 0x22 API_KEY_REVOKE over TCP |
| `SetVPNManager(mgr)` | Wires the VPN manager for provisioning |
| `RequestVPNProvisioning()` | Sends 0xB0 and handles 0xB1 response |
| `WaitForVPN(timeout)` | Blocks until tunnel is established |

**UUID priority chain** (inside `NewClient`):
```
1. uuid param (from cfg.Auth.UUID)     — empty string = skip
2. .drone_uuid file                    — read from working directory
3. MAC address of primary interface    — stable across reboots
4. random UUID                         — last resort, changes on restart
```

**`auth/hmac.go`**:
```go
func ComputeHMAC(secret, droneUUID, nonce, timestamp string) string {
    message := fmt.Sprintf("%s:%s:%s", droneUUID, nonce, timestamp)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(message))
    return hex.EncodeToString(mac.Sum(nil))
}
```

**`auth/secret_storage.go`**:
```go
type DroneSecret struct {
    DroneUUID string    `json:"drone_uuid"`
    SecretKey string    `json:"secret_key"`
    CreatedAt time.Time `json:"created_at"`
}
// Saved as JSON at `.drone_secret`, permissions 0600
```

**`auth/client_apikey.go`**:
- All API key operations share the same TCP conn with `tcpMu` mutex lock
- Uses dedicated response channels (`apiKeyRespCh`, etc.) so the keepalive goroutine can route responses back to callers

---

### `forwarder` — MAVLink Bridge

**File**: `forwarder/forwarder.go` (~1011 lines)

**Struct**:
```go
type Forwarder struct {
    cfg              *config.Config
    listenerNode     *gomavlib.Node   // receives from Pixhawk
    senderNode       *gomavlib.Node   // sends to server
    authClient       *auth.Client
    mu               sync.RWMutex
    seqMu            sync.Mutex
    stopCh           chan struct{}
    pixhawkConnected chan struct{}     // closed on first real HEARTBEAT
    pixhawkOnce      sync.Once
    previousIP       string
    isHealthy        bool
    forceCheckCh     chan struct{}     // trigger immediate IP check
    udpHeartbeatSent chan struct{}     // gates MAVLink forwarding start
    lastSeqNum       map[uint8]uint8  // dedup: sysID → last seq
    verboseMode      bool
    lastHeartbeatLog time.Time
    lastGPSLog       time.Time
    lastAttitudeLog  time.Time
}
```

**Key functions**:

| Function | Description |
|----------|-------------|
| `NewListener(cfg)` | Creates only `listenerNode`; called before Pixhawk SysID is known |
| `New(cfg, authClient, listenerNode)` | Creates full forwarder with `senderNode`; `listenerNode` reused if provided |
| `NewWithVPN(cfg, auth, listener, vpn)` | Creates forwarder and binds sender to VPN tunnel if available |
| `(f) Start()` | Launches goroutines: `receiveAndForward`, `receiveFromServer`, `sendMavlinkSessionHeartbeat`, `monitorIPChange` |
| `(f) WaitForPixhawkConnection(timeout)` | Blocks until `pixhawkConnected` channel is closed |
| `(f) GetListenerNode()` | Returns `listenerNode` for external use (web server) |
| `(f) SetAuthClient(client)` | Wires auth client's `OnNetworkError` callback |
| `(f) Stop()` | Closes `stopCh`, calls `authClient.Stop()`, closes both nodes |

**Goroutines started by `Start()`**:

| Goroutine | Description |
|-----------|-------------|
| `receiveAndForward()` | Reads EventFrame from `listenerNode` channel, applies filters, writes to `senderNode` |
| `receiveFromServer()` | Reads EventFrame from `senderNode` channel, writes to `listenerNode` (to Pixhawk) |
| `sendMavlinkSessionHeartbeat()` | Sends MAVLink msg 42999 via `senderNode` at configured Hz |
| `monitorIPChange()` | Every 5s: re-dial `8.8.8.8:80` to detect IP change; recreates `senderNode` + force-reconnects auth |

**Forwarding filter chain (receiveAndForward)**:
```
EventFrame received from listenerNode
  ├── SysID == 255 → DROP (simulator/GCS)
  ├── seqNum == lastSeqNum[sysID] → DROP (duplicate)
  ├── pixhawkConnected not yet closed → DROP (Pixhawk not ready)
  ├── isHealthy == false → COUNT as failed, do NOT forward
  ├── vpnManager.IsRunning() → forward via VPN tunnel (`10.8.0.1:14550`)
  └── PASS → forward via Public IP
```

**Ethernet IP detection (`getEthernetIP`)**:
1. If `cfg.Ethernet.LocalIP` is set and exists on an interface → use it
2. If `cfg.Ethernet.AutoSetup=true` and IP missing → call `sudo ip addr add {ip}/{subnet} dev {iface}`
3. Otherwise → auto-detect first `eth*` / `end*` / `enp*` / `eno*` interface with IPv4

---

### `mavlink_custom` — Custom Dialect

**File**: `mavlink_custom/custom.go`

Defines the custom `MessageSessionHeartbeat` (ID 42999) and combines it with `all.Dialect`:

```go
type MessageSessionHeartbeat struct {
    Token            [32]byte  // Session token hash (SHA-256 of token string)
    ExpiresAt        uint32    // Unix timestamp of session expiry
    Sequence         uint16    // Monotonic counter per heartbeat
    PixhawkConnected uint8     // 0 = not connected, 1 = connected
}
func (*MessageSessionHeartbeat) GetID() uint32 { return 42999 }
```

`GetCombinedDialect()` creates a new `*dialect.Dialect` (never mutates `all.Dialect`) containing all standard messages plus `MessageSessionHeartbeat`.

**Why ID 42999?** Chosen to avoid overlap with standard MAVLink IDs (0–255 common, up to ~400 extended). 42999 is in the vendor-specific range and unlikely to conflict.

---

### `web` — HTTP Server & MAVLink Bridge

**File**: `web/server.go` (~1581 lines)

**Static files**: Embedded with `//go:embed static/*`; served at `/` with 1-hour cache headers. Root `/` redirects to `/dashboard.html`.

**MAVLink Bridge** (`MAVLinkBridge` struct):
```go
type MAVLinkBridge struct {
    node            *gomavlib.Node    // same listenerNode as forwarder
    pixhawkSysID    uint8
    connected       bool
    responseTimeout time.Duration     // 5s for param set
    paramCache      map[string]CachedParameter
    paramLoading    bool
    paramTotal      int
    paramReceived   int
    paramLastUpdate time.Time
    paramValueCh    chan *common.MessageParamValue  // buffered 100
}
```

Initialized via `InitMAVLinkBridge(node)` — called once from `main.go` after forwarder is ready. The bridge holds a reference to the **same `listenerNode`** used by the forwarder, so `PARAM_SET` messages written via `node.WriteMessageAll()` go directly to Pixhawk.

**Parameter cache flow**:
1. Forwarder `receiveAndForward` receives `MessageParamValue` from Pixhawk
2. Calls `web.HandleParamValue(msg)` → enqueues to `paramValueCh`
3. `processParamValues()` goroutine drains channel → populates `paramCache`
4. `GET /api/param/list` / `GET /api/param/status` serve from cache

**Heartbeat capture**:
- Forwarder calls `web.HandleHeartbeat(sysID)` on first real Pixhawk HEARTBEAT
- Bridge stores `pixhawkSysID` (used as `TargetSystem` for PARAM_SET / PARAM_REQUEST_LIST)
- `GetPixhawkSystemID()` returns stored SysID (fallback: 1 if bridge not init)

**API endpoints** (full list):

```
GET  /                              → redirect to /dashboard.html
GET  /api/status                    → metrics.Global.GetSnapshot()
GET  /api/health                    → {"status": "ok"}
GET  /api/connection                → {connected, systemId, message}
POST /api/param/request-list        → Send PARAM_REQUEST_LIST to Pixhawk
GET  /api/param/status              → {loading, totalCount, receivedCount, progress, lastUpdated}
GET  /api/param/status?include=params → same + parameters array
GET  /api/param/list                → []CachedParameter
GET  /api/param/get?name=<N>        → {found, param}
POST /api/param/set                 → {paramName, paramValue, paramType}
GET  /api/v1/drone/api-key/status   → auth key status from server
POST /api/v1/drone/api-key/request  → {hours: N}
DEL  /api/v1/drone/api-key/revoke   → revoke active key
GET  /api/4g/status                 → modem/SIM info (via 4G_control.py or AT commands)
POST /api/4g/restart                → restart 4G module
GET  /api/camera/status             → camera streamer state
POST /api/camera/start              → start camera_streamer.py
POST /api/camera/stop               → kill camera_streamer.py
```

**Camera management**:
- Managed via `exec.Command("python3", "/home/pi/HBQ_server_drone/Find_landing/camera_streamer.py")`
- `cameraMutex` prevents concurrent starts
- Process death monitored in goroutine; `cameraCmd` set to `nil` on exit

---

### `metrics` — Telemetry

**File**: `metrics/metrics.go`

Exposes a `Global` singleton with methods:
- `IncSent(msgType)` / `IncFailedSend(msgType)` / `IncFailedUnhealthy(msgType)`
- `SetIP(ip)`
- `AddLog(level, message)` — rolling event log
- `GetSnapshot()` → JSON-serializable struct served at `/api/status`

---

### `logger` — Logging Facade

**File**: `logger/logger.go`

Leveled wrapper around stdlib `log`:
```go
func Debug(format string, args ...interface{})
func Info(format string, args ...interface{})
func Warn(format string, args ...interface{})
func Error(format string, args ...interface{})
```

Level controlled by `cfg.Log.Level`. Verbose mode enables per-field MAVLink message parsing in `forwarder.parseMessageVerbose()`.

---

| Feature | Description |
|---------|-------------|
| **Enforced Pixhawk Conn** | App mặc định sẽ dừng nếu không thấy Heartbeat Pixhawk sau 30s. |
| **allow_missing_pixhawk** | Cấu hình `true` trong YAML để bỏ qua bước kiểm tra này (Debug/Test không cần drone). |
| **Startup Delay** | Trì hoãn auth/VPN cho đến khi xác định được System ID của Pixhawk. |

---

## Critical Data Flows

### Startup Flow

```
main()
 ├── config.Load("config.yaml")
 ├── auth.NewClient(...)
 ├── [cfg.VPN.Enabled] → vpn.NewManager(...)
 ├── [--register flag] → authClient.Register() → exit
 ├── forwarder.NewListener(cfg)                   ← creates listenerNode only
 ├── fwd.Start() [listener-only mode]             ← goroutines: receiveAndForward (partial)
 ├── fwd.WaitForPixhawkConnection(30s)            ← blocks on pixhawkConnected channel
 ├── forwarder.NewWithVPN(cfg, ..., vpnMgr)        ← node with correct SysID + VPN bind
 ├── fwd.Start()                                  ← full forwarder loop
 ├── authClient.Start()                            ← TCP → authenticate() → keepaliveLoop()
 ├── wait up to 10s for authClient.IsAuthenticated()
 ├── [cfg.VPN.Enabled] → authClient.RequestVPNProvisioning() → vpnMgr.Start()
 ├── web.StartServer(port, authClient, uuid)       ← HTTP server goroutine
 ├── web.InitMAVLinkBridge(fwd.GetListenerNode())  ← wire listenerNode into bridge
 ├── [camera.enabled] → web.startCameraStreamer()
 └── signal.Notify(SIGINT, SIGTERM) → block
      └── on signal → fwd.Stop() → exit
```

### Auth Registration Flow

```
authClient.Register()
 ├── TCP connect to host:5770
 ├── send REGISTER_INIT {uuid}
 ├── recv REGISTER_CHALLENGE {nonce}
 ├── compute hmac = HMAC(shared_secret, "{uuid}:{nonce_hex}:{ts}")
 ├── send REGISTER_RESPONSE {uuid, hmac}
 ├── recv REGISTER_ACK {secret_key}
 └── secret_storage.SaveSecret({uuid, secret_key})
```

### Auth Session Flow

```
authClient.Start()
 ├── LoadSecret(".drone_secret") → secretKey
 ├── authenticate()
 │    ├── TCP connect to host:5770
 │    ├── send AUTH_INIT {uuid}
 │    ├── recv AUTH_CHALLENGE {nonce}
 │    ├── compute authKey = SHA256(shared_secret + secretKey) [Combined Key logic]
 │    ├── compute hmac = HMAC(authKey, "{uuid}:{nonce_hex}:{ts}")
 │    ├── send AUTH_RESPONSE {uuid, hmac}
 │    ├── recv AUTH_ACK (contains session token directly in V2)
 │    └── store sessionToken, sessionExpiry in memory
 └── go keepaliveLoop()
      └── every keepalive_interval seconds:
           send SESSION_REFRESH
           recv SESSION_ACK (or reconnect on error)
```

### MAVLink Forward Flow (Pixhawk → Server)

```
listenerNode.Events() channel
 └── EventFrame received
      ├── sysID == 255? → DROP
      ├── seqNum == lastSeqNum[sysID]? → DROP (dedup)
      ├── pixhawkConnected closed? → continue, else DROP
      ├── isHealthy? → continue, else metrics.IncFailedUnhealthy
      ├── msg == MessageHeartbeat?
      │    └── web.HandleHeartbeat(sysID)
      ├── msg == MessageParamValue?
      │    └── web.HandleParamValue(msg)
      └── senderNode.WriteFrameAll(frame) → UDP to server:14550
```

### MAVLink Downlink Flow (Server → Pixhawk)

```
senderNode.Events() channel
 └── EventFrame received
      └── listenerNode.WriteMessageAll(msg) → to Pixhawk via serial/UDP
```

### Session Heartbeat Flow

```
sendMavlinkSessionHeartbeat() [every 1/Hz seconds]
 ├── authClient.GetSessionInfo() → tokenHex, _, expiresAt
 ├── tokenHex == ""? → skip
 (548) ├── sha256.Sum256([]byte(tokenHex)) → tokenBinary [32]byte
 ├── pixhawkConnected? → pixhawkConnected=1 else 0
 ├── msg = MessageSessionHeartbeat{Token, ExpiresAt, Sequence, PixhawkConnected}
 ├── senderNode.WriteMessageAll(msg) → UDP to server:14550
 └── first send → close(udpHeartbeatSent) → unblocks ForwardLoop start
```

### IP Change / Reconnect Flow (`monitorIPChange`)

```
monitorIPChange() [every 5s]
 ├── getLocalIP() via UDP dial to 8.8.8.8:80
 ├── currentIP == previousIP? → no-op
 └── IP changed or Network Restored:
      ├── senderNode.Rebuild() (force re-bind to new local IP)
      ├── authClient.ForceReconnect() → closes TCP conn to trigger fresh Auth
      ├── isHealthy = true
      └── metrics.SetIP(currentIP)
```

### Network Management Architecture (PBR)

1. **`dronebridge-4g-init.service`**: Bật nguồn module, QMI connect, lấy IP cho `wwan0`.
2. **`dronebridge-netmon.service`**: 
   - Chạy `setup_pbr.sh` để tạo `ip rule` trỏ user `pi` vào `table 100`.
   - Chạy `connection_manager.py` để duy trì `table 100` (4G hoặc WiFi fallback).
3. **`dronebridge.service`**: Chạy app chính bằng user `pi` để traffic được đánh dấu PBR.

---

## Key Design Decisions

### 1. Listener-first startup
The `listenerNode` is created before auth to capture the Pixhawk System ID from the first heartbeat. This ensures `senderNode.OutSystemID` matches the real drone, which the Hybrid Router uses for MAVLink routing.

### 2. No GCS heartbeat from DroneBridge
`sendHeartbeat()` is deliberately disabled. Sending a GCS heartbeat (SysID=255 / Type=GCS) from DroneBridge caused MAVLink ID conflicts with QGC. DroneBridge only forwards Pixhawk-originated messages.

### 3. Custom MAVLink ID 42999 for session sync
UDP source port changes unpredictably with 4G reconnects. The server cannot rely on IP:port being stable. Embedding the session token inside a MAVLink message sent over the *same UDP socket* as the data stream allows the server to bind `IP:Port → DroneUUID` reliably.

### 4. Shared authClient between forwarder and web
`main.go` creates one `auth.Client` and passes the same pointer to both `forwarder.New()` and `web.StartServer()`. This prevents duplicate TCP connections and ensures both components see the same session token.

### 5. Shared listenerNode between forwarder and web
`web.InitMAVLinkBridge(fwd.GetListenerNode())` reuses the forwarder's listenerNode to write `PARAM_SET` and `PARAM_REQUEST_LIST` to Pixhawk directly, without a separate connection.

### 6. OnNetworkError callback
`authClient.OnNetworkError` is a callback set by `forwarder.New()`. When the TCP auth connection experiences an error, it calls this callback, which sets `isHealthy=false` on the forwarder and signals the IP monitor to check immediately.

### 7. TCP/UDP Channel Separation (VPN)
To ensure reliability, **TCP Authentication and Session Refresh always stay on the Public IP** channel. Only **UDP traffic (MAVLink/Telemetry)** is routed through the WireGuard VPN tunnel (`10.8.0.1`) once provisioned. This prevents "VPN-in-VPN" deadlocks and ensures management connectivity even if the tunnel fails.

### 8. Authentication V2 & Combined Key
The current implementation uses **Auth V2**, where the session token is returned directly in the `AUTH_ACK` packet to reduce round-trips. Additionally, if a `shared_secret` is configured, the system uses a **Combined Key** (`SHA256(shared_secret + secret_key)`) as the HMAC-SHA256 secret. This prevents brute-force attacks on individual drone secrets if the system-wide shared secret is kept secure.

### 9. Dynamic System ID Detection
DroneBridge tự động nhận diện `System ID` của Pixhawk thay vì fix cứng là 1.
- **Cơ chế:** Khi nhận được Heartbeat đầu tiên từ Pixhawk, `forwarder` sẽ gọi `web.HandleHeartbeat(sysID)`.
- **Lưu trữ:** ID này được lưu trong `web.MAVLinkBridge` và có thể truy cập qua `web.GetPixhawkSystemID()`.
- **Ứng dụng:** Các lệnh gửi tới Pixhawk (như `PARAM_REQUEST_LIST`, `PARAM_SET`) sẽ dùng đúng ID này làm `TargetSystem`, đảm bảo tương thích với mọi drone (PX4/ArduPilot) có ID khác 1.
- **Fallback:** Trả về 1 nếu chưa kết nối drone.

---

## State Management

| State | Owner | Type | Description |
|-------|-------|------|-------------|
| `sessionToken` | `auth.Client` | `string` (in-memory) | Current session token hex |
| `secretKey` | `auth.Client` | `string` (in-memory, from `.drone_secret`) | Device auth secret |
| `pixhawkConnected` | `Forwarder` | `chan struct{}` (closed once) | Closed on first heartbeat |
| `isHealthy` | `Forwarder` | `bool` (guarded by `mu`) | False when IP is changing |
| `pixhawkSysID` | `web.MAVLinkBridge` | `uint8` | Captured from first heartbeat |
| `paramCache` | `web.MAVLinkBridge` | `map[string]CachedParameter` | All known Pixhawk params |
| `.drone_secret` | Filesystem | JSON file | Device secret persistent store |
| `config.yaml` | Filesystem | YAML file | All configuration |

---

## Concurrency Model

| Goroutine | Package | Description |
|-----------|---------|-------------|
| `receiveAndForward` | forwarder | Read Pixhawk events → filter → forward to server |
| `receiveFromServer` | forwarder | Read server events → forward to Pixhawk |
| `sendMavlinkSessionHeartbeat` | forwarder | Periodic MAVLink ID 42999 to server |
| `monitorIPChange` | forwarder | Detect 4G IP changes, recreate UDP conn |
| `keepaliveLoop` | auth | TCP SESSION_REFRESH every N seconds |
| `processParamValues` | web | Drain param channel → update cache |
| HTTP server | web | `http.ListenAndServe(:8080)` |
| Camera monitor | web | Watch camera_streamer.py process |

**Mutexes**:
- `Forwarder.mu` (RWMutex) — protects `isHealthy`, `senderNode`
- `Forwarder.seqMu` (Mutex) — protects `lastSeqNum` dedup map
- `auth.Client.tcpMu` (Mutex) — serializes all TCP writes (keepalive + API key ops)
- `web.MAVLinkBridge.mutex` (RWMutex) — protects `connected`, `pixhawkSysID`
- `web.MAVLinkBridge.paramCacheMutex` (RWMutex) — protects `paramCache`

---

## Protocol Specification

### Binary Auth Protocol

All messages over TCP port 5770 follow this framing:

```
┌──────────────┬────────────────────────────────┐
│  1 byte type │  N bytes payload (type-defined) │
└──────────────┴────────────────────────────────┘
```

Full message type table is in `auth/protocol.go`. Key structs:

```go
// AUTH_CHALLENGE (0x02) — server → drone
type AuthChallenge struct {
    Nonce [32]byte
}

// AUTH_RESPONSE (0x03) — drone → server
type AuthResponse struct {
    UUID      [36]byte   // UUID string bytes
    HMAC      [64]byte   // hex-encoded HMAC-SHA256
    Timestamp [20]byte   // Unix timestamp string
}

// SESSION_ACK (0x11) — server → drone
type SessionAck struct {
    Token     [64]byte   // hex session token
    ExpiresAt int64      // Unix timestamp
}
```

### Custom MAVLink Message 42999

```
Message ID: 42999
Fields (little-endian wire encoding):
  ExpiresAt         : uint32  (bytes 0-3)
  Sequence          : uint16  (bytes 4-5)
  Token             : [32]byte (bytes 6-37) — SHA-256 Hash of session token
  PixhawkConnected  : uint8   (byte 38)
```

### VPN Provisioning Protocol (0xB0 / 0xB1)

Used for automatic WireGuard configuration between Drone and Router.

**MSG_VPN_PROVISION_REQUEST (0xB0)**:
```
[TYPE:1]
[UUID_LEN:2][DRONE_UUID:var]
[TOKEN_LEN:2][SESSION_TOKEN:var]
[PUBKEY_LEN:2][WG_PUBLIC_KEY:var]
```

**MSG_VPN_PROVISION_ACK (0xB1)**:
- **Success**: `[TYPE:1][RESULT:0x00][IP_LEN:2][IP:var][SRV_PUBKEY_LEN:2][SRV_PUBKEY:var][EP_LEN:2][ENDPOINT:var]`
- **Failure**: `[TYPE:1][RESULT:0x01][ERROR_CODE:1]` (See `ErrVPN...` constants)

---

## Configuration Schema

See full YAML in [README.md#configuration](README.md#configuration).

**Important defaults**:
- `network.local_listen_port`: 14540 (Pixhawk-facing)
- `network.target_port`: 14550 (server-facing)
- `ethernet.local_ip`: 10.41.10.1 (Pi side)
- `ethernet.pixhawk_ip`: 10.41.10.2 (Pixhawk side)
- `auth.keepalive_interval`: 30 (seconds)
- `auth.session_heartbeat_frequency`: 1.0 (Hz)
- `ethernet.pixhawk_connection_timeout`: 30 (seconds)

---

## File Layout (Runtime)

```
/home/pi/DroneBridge/          (working directory)
├── dronebridge                ← compiled binary
├── config.yaml                ← production config
├── .drone_secret              ← JSON {drone_uuid, secret_key, created_at}, 0600 perms
└── .drone_uuid                ← optional: explicit UUID override file
```

---

## Common Pitfalls

### 1. `.drone_secret` missing after re-flash
If the Pi SD card is re-flashed, `.drone_secret` is lost. The drone **cannot authenticate** without it. Solution: Re-run `./dronebridge --register` with the correct `shared_secret` from the fleet operator.

### 2. SysID 255 messages from SITL polluting the stream
The forwarder explicitly drops SysID 255. If running SITL alongside the real flight controller on the same network, SITL packets are silently ignored.

### 3. `OutSystemID` mismatch
If `New()` is called before `WaitForPixhawkConnection()`, `web.GetPixhawkSystemID()` returns 0 → defaults to 1. This may cause MAVLink routing issues if the real Pixhawk uses a different system ID. Always wait for heartbeat before calling `New()`.

### 4. Auth and forwarder both starting at the same time
`authClient.Start()` was previously called inside `forwarder.Start()`. This led to duplicate TCP connections when `SetAuthClient()` was used. The current design: `main.go` calls `authClient.Start()` explicitly after `fwd.SetAuthClient()`.

### 5. Ethernet auto-setup requires sudo
`setupInterfaceIP()` runs `sudo ip addr add`. The `pi` user must have passwordless sudo for `ip` commands, or the ethernet auto_setup feature will fail silently (falls back to UDP server only).

### 6. Camera process path is hardcoded
`web/server.go` starts `python3 /home/pi/HBQ_server_drone/Find_landing/camera_streamer.py`. This path must exist on the Pi. Move to config if this needs to be portable.

### 7. API key operations block keepalive
API key requests and `SESSION_REFRESH` share `tcpMu`. Long-running API key operations may delay keepalive and trigger server-side session expiry. The keepalive interval should be larger than the longest expected API key round-trip.

### 8. App exits immediately if Pixhawk is missing
Nếu drone chưa bật hoặc cáp serial bị lỏng, app sẽ thoát sau 30s timeout. 
- **Sửa:** Kiểm tra cáp hoặc set `allow_missing_pixhawk: true` trong `config.yaml` để test app mà không cần drone.

---

## Extension Points

| Feature | Where to extend |
|---------|----------------|
| New MAVLink message forwarding | `forwarder/forwarder.go` `receiveAndForward()` type switch |
| New HTTP API endpoints | `web/server.go` `StartServer()` function |
| New auth protocol messages | `auth/protocol.go` + client message dispatch in `auth/client.go` |
| New config options | `config/config.go` — add fields, update `Validate()` |
| New custom MAVLink messages | `mavlink_custom/custom.go` — add struct + register in `GetCombinedDialect()` |
| New metrics | `metrics/metrics.go` — add fields to snapshot struct |
| QoS / priority forwarding | Replace `WriteFrameAll` in `receiveAndForward` with priority queue |
| Per-drone session encryption | Replace binary token in `MessageSessionHeartbeat` with encrypted payload |
