# 🛡️ Hướng Dẫn Tích Hợp WireGuard VPN Client vào Pi_CM5_DroneBridgeService

**Dành cho AI Agent triển khai và User giám sát**

---

## 📋 Tổng Quan

Tài liệu này hướng dẫn từng bước sửa code project `Pi_CM5_DroneBridgeService` (Go) để tích hợp:
1. **WireGuard VPN Client** chạy **Userspace** (không cần `sudo`, không phụ thuộc kernel module).
2. **VPN Provisioning Protocol** (gói `0xB0`/`0xB1`) để tự động nhận cấu hình VPN từ Hybrid Router.
3. **Chuyển hướng kênh MAVLink** sang tunnel VPN sau khi đã provisioning thành công.

### Nguyên Tắc Cốt Lõi (KHÔNG ĐƯỢC PHÁ VỠ)
- ✅ **Backward Compatible**: Nếu VPN provisioning thất bại, drone **vẫn chạy bình thường** qua kênh UDP public.
- ✅ **Zero Sudo**: Dùng thư viện `golang.zx2c4.com/wireguard` (userspace), không cài kernel module.
- ✅ **Tách biệt traffic**: MAVLink qua VPN (`10.8.0.1:14550`). Video/Camera giữ nguyên qua mạng public.

### Kiến Trúc Sau Khi Tích Hợp

```
Pixhawk ─UDP:14540─► DroneBridgeService ─ [Auth TCP:5770] ──► Hybrid Router
                              │
                              ├─ [MAVLink UDP] ──── 0xB0/0xB1 Provisioning ──► Server
                              │                      Nhận VPN config
                              │
                              └─ [MAVLink qua WG Tunnel: UDP 10.8.0.1:14550] ──► Router
```

---

## 🗺️ Kế Hoạch Triển Khai (5 Giai Đoạn)

| Giai đoạn | Mô tả | File thay đổi |
|---|---|---|
| **1** | Thêm dependency WireGuard Go | [go.mod](file:///d:/FirmwareGit/QCC-CloudControlStation/drone-fleet-backend/go.mod) |
| **2** | Thêm `VPNConfig` vào [config.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go) + [config.yaml](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config.yaml) | [config/config.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go), [config.yaml](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config.yaml) |
| **3** | Định nghĩa Protocol `0xB0`/`0xB1` | [auth/protocol.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/protocol.go) |
| **4** | Thêm logic VPN Provisioning vào Auth Client | [auth/client.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go) |
| **5** | Thêm VPN Manager + Chuyển hướng UDP sender | `vpn/manager.go` (mới), [forwarder/forwarder.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go) |

---

## Giai Đoạn 1: Thêm Dependency WireGuard

### 1.1 Lý do
Thư viện `golang.zx2c4.com/wireguard` cung cấp WireGuard Userspace hoạt động thuần trong tiến trình Go, không cần cài `wireguard-tools` hay `wg` trên Pi.

### 1.2 Hành động

**File: [go.mod](file:///d:/FirmwareGit/QCC-CloudControlStation/drone-fleet-backend/go.mod)**

Chạy lệnh sau trong thư mục `Pi_CM5_DroneBridgeService`:

```bash
go get golang.zx2c4.com/wireguard@latest
go get golang.zx2c4.com/wireguard/tun/netstack@latest
go mod tidy
```

> **Kiểm tra (User):** Sau khi chạy, mở [go.mod](file:///d:/FirmwareGit/QCC-CloudControlStation/drone-fleet-backend/go.mod) và xác nhận có dòng `golang.zx2c4.com/wireguard` trong phần `require`.

---

## Giai Đoạn 2: Mở Rộng Config

### 2.1 Lý do
Cần lưu trạng thái VPN (IP được cấp, keypair) và cấu hình server WireGuard vào config. File VPN config được lưu riêng ra `.drone_vpn.json` để bảo toàn khi drone khởi động lại.

### 2.2 Sửa [config/config.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go)

**Thêm struct `VPNConfig` vào cuối phần khai báo:**

Tìm dòng `// CameraConfig contains camera streaming settings` (khoảng line 67), chèn struct mới **TRƯỚC** [CameraConfig](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go#68-74):

```go
// VPNConfig contains WireGuard VPN settings
type VPNConfig struct {
    Enabled        bool   `yaml:"enabled"`          // Enable VPN provisioning
    ConfigFile     string `yaml:"config_file"`       // Path to store .drone_vpn.json (default: .drone_vpn.json)
    ServerEndpoint string `yaml:"server_endpoint"`   // WireGuard server endpoint (IP:Port), e.g., "45.117.171.237:51820"
    ServerPubKey   string `yaml:"server_pub_key"`    // WireGuard server public key (known in advance)
    VPNPort        int    `yaml:"vpn_port"`           // MAVLink port INSIDE tunnel (default: 14550)
    RouterVPNIP    string `yaml:"router_vpn_ip"`      // Router's IP inside VPN tunnel (default: "10.8.0.1")
}
```

**Thêm `VPN VPNConfig` vào struct [Config](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go#11-19):**

Tìm dòng:
```go
type Config struct {
    Log      LogConfig      `yaml:"log"`
    Auth     AuthConfig     `yaml:"auth"`
    Network  NetworkConfig  `yaml:"network"`
    Ethernet EthernetConfig `yaml:"ethernet"`
    Web      WebConfig      `yaml:"web"`
    Camera   CameraConfig   `yaml:"camera"`
}
```

Sửa thành:
```go
type Config struct {
    Log      LogConfig      `yaml:"log"`
    Auth     AuthConfig     `yaml:"auth"`
    Network  NetworkConfig  `yaml:"network"`
    Ethernet EthernetConfig `yaml:"ethernet"`
    Web      WebConfig      `yaml:"web"`
    Camera   CameraConfig   `yaml:"camera"`
    VPN      VPNConfig      `yaml:"vpn"`            // ← THÊM DÒNG NÀY
}
```

**Thêm default cho VPN trong hàm [Load](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go#145-174):**

Trong hàm [Load](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go#145-174), sau dòng `if cfg.Ethernet.PixhawkConnectionTimeout <= 0`, thêm:
```go
// VPN defaults
if cfg.VPN.ConfigFile == "" {
    cfg.VPN.ConfigFile = ".drone_vpn.json"
}
if cfg.VPN.VPNPort == 0 {
    cfg.VPN.VPNPort = 14550
}
if cfg.VPN.RouterVPNIP == "" {
    cfg.VPN.RouterVPNIP = "10.8.0.1"
}
```

### 2.3 Sửa [config.yaml](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config.yaml)

**Thêm section `vpn` vào cuối file [config.yaml](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config.yaml):**

```yaml
vpn:
    enabled: true
    config_file: .drone_vpn.json
    server_endpoint: "45.117.171.237:51820"
    server_pub_key: "mGo0+CYziYGED5KeuX+N5D2yvflEGnRJ8I1eSS+Zq0A="
    vpn_port: 14550
    router_vpn_ip: "10.8.0.1"
```

> **Kiểm tra (User):** Build thử `go build ./...` trong thư mục project. Nếu không có lỗi compile, giai đoạn này thành công.

---

## Giai Đoạn 3: Định Nghĩa Protocol VPN (`0xB0` / `0xB1`)

### 3.1 Lý do
Gói `0xB0` (Drone → Router): Drone gửi WireGuard Public Key lên để xin IP.
Gói `0xB1` (Router → Drone): Router trả về IP được cấp, Public Key server và Endpoint.

### 3.2 Sửa [auth/protocol.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/protocol.go)

**Thêm hằng số message type** vào block `const` hiện có (sau `MsgRegisterAck uint8 = 0xA3`):

```go
    // VPN Provisioning (WireGuard Auto-Provisioning)
    MsgVPNProvisionRequest uint8 = 0xB0 // Drone → Router: request VPN config
    MsgVPNProvisionAck     uint8 = 0xB1 // Router → Drone: VPN config response
```

**Thêm struct VPN** vào cuối file [auth/protocol.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/protocol.go):

```go
// ============================================================================
// VPN PROVISIONING PROTOCOL (0xB0 / 0xB1)
// ============================================================================

// VPNProvisionRequest represents MSG_VPN_PROVISION_REQUEST (0xB0)
// Drone → Router: gửi Session Token và WireGuard Public Key mới
type VPNProvisionRequest struct {
    SessionToken string // Current valid session token
    WGPublicKey  string // Base64-encoded WireGuard public key (44 chars)
}

// VPNProvisionAck represents MSG_VPN_PROVISION_ACK (0xB1)
// Router → Drone: trả về IP được cấp và thông tin server WireGuard
type VPNProvisionAck struct {
    Result         byte   // 0x00 = success, 0x01 = failure
    ErrorCode      byte   // Error code if failed
    AssignedIP     string // Assigned VPN IP, e.g., "10.8.1.5"
    ServerPubKey   string // Server WireGuard public key (base64)
    ServerEndpoint string // Server WireGuard endpoint, e.g., "45.117.171.237:51820"
}

// SerializeVPNProvisionRequest creates MSG_VPN_PROVISION_REQUEST packet (0xB0)
// Format: [TYPE:1][TOKEN_LEN:2][TOKEN:var][PUBKEY_LEN:2][PUBKEY:var]
func SerializeVPNProvisionRequest(req *VPNProvisionRequest) []byte {
    tokenBytes := []byte(req.SessionToken)
    keyBytes := []byte(req.WGPublicKey)
    packet := make([]byte, 0, 1+2+len(tokenBytes)+2+len(keyBytes))

    packet = append(packet, MsgVPNProvisionRequest)

    buf := make([]byte, 2)
    binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
    packet = append(packet, buf...)
    packet = append(packet, tokenBytes...)

    binary.LittleEndian.PutUint16(buf, uint16(len(keyBytes)))
    packet = append(packet, buf...)
    packet = append(packet, keyBytes...)

    return packet
}

// ParseVPNProvisionAck parses MSG_VPN_PROVISION_ACK (0xB1)
// Format success: [TYPE:1][RESULT:1][IP_LEN:2][IP:var][SRV_KEY_LEN:2][SRV_KEY:var][SRV_EP_LEN:2][SRV_EP:var]
// Format failure: [TYPE:1][RESULT:1][ERROR_CODE:1]
func ParseVPNProvisionAck(data []byte) (*VPNProvisionAck, error) {
    if len(data) < 2 {
        return nil, fmt.Errorf("vpn ack packet too short")
    }
    if data[0] != MsgVPNProvisionAck {
        return nil, fmt.Errorf("invalid msg type: 0x%02x (expected 0xB1)", data[0])
    }

    ack := &VPNProvisionAck{}
    ack.Result = data[1]
    offset := 2

    if ack.Result != ResultSuccess {
        if len(data) > offset {
            ack.ErrorCode = data[offset]
        }
        return ack, nil
    }

    // Helper to read length-prefixed string
    readStr := func() (string, error) {
        if len(data) < offset+2 {
            return "", fmt.Errorf("buffer too short")
        }
        l := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
        offset += 2
        if len(data) < offset+l {
            return "", fmt.Errorf("buffer too short for string")
        }
        s := string(data[offset : offset+l])
        offset += l
        return s, nil
    }

    var err error
    if ack.AssignedIP, err = readStr(); err != nil {
        return nil, fmt.Errorf("read AssignedIP: %w", err)
    }
    if ack.ServerPubKey, err = readStr(); err != nil {
        return nil, fmt.Errorf("read ServerPubKey: %w", err)
    }
    if ack.ServerEndpoint, err = readStr(); err != nil {
        return nil, fmt.Errorf("read ServerEndpoint: %w", err)
    }

    return ack, nil
}
```

> **Kiểm tra (User):** Chạy `go build ./auth/...`. Nếu không có lỗi, giai đoạn 3 thành công.

---

## Giai Đoạn 4: Tạo VPN Manager

### 4.1 Lý do
Module `vpn/manager.go` chịu trách nhiệm:
- Sinh keypair WireGuard (dùng thư viện Go).
- Lưu/Tải VPN config vào `.drone_vpn.json`.
- Khởi tạo WireGuard Userspace tunnel.
- Cung cấp `net.Conn` để Forwarder dùng khi gửi MAVLink qua tunnel.

### 4.2 Tạo file mới `vpn/manager.go`

**Tạo thư mục `vpn/` và file `vpn/manager.go` với nội dung:**

```go
package vpn

import (
    "encoding/json"
    "fmt"
    "net"
    "net/netip"
    "os"
    "sync"
    "time"

    "DroneBridge/logger"

    "golang.zx2c4.com/wireguard/conn"
    "golang.zx2c4.com/wireguard/device"
    "golang.zx2c4.com/wireguard/tun/netstack"
    "golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// VPNConfig stores the provisioned VPN configuration on disk
type VPNConfig struct {
    AssignedIP     string `json:"assigned_ip"`      // e.g., "10.8.1.5"
    PrivateKey     string `json:"private_key"`      // Base64 WireGuard private key
    PublicKey      string `json:"public_key"`       // Corresponding public key
    ServerPubKey   string `json:"server_pub_key"`   // Server's WireGuard public key
    ServerEndpoint string `json:"server_endpoint"`  // e.g., "45.117.171.237:51820"
    ProvisionedAt  int64  `json:"provisioned_at"`   // Unix timestamp
}

// Manager manages the WireGuard userspace VPN tunnel
type Manager struct {
    configPath string
    mu         sync.RWMutex
    config     *VPNConfig
    tun        *netstack.Net // Virtual network stack
    dev        *device.Device
    running    bool
}

// NewManager creates a new VPN manager
func NewManager(configPath string) *Manager {
    return &Manager{configPath: configPath}
}

// GenerateKeyPair generates a new WireGuard keypair
// Returns (privateKeyBase64, publicKeyBase64, error)
func GenerateKeyPair() (string, string, error) {
    privKey, err := wgtypes.GeneratePrivateKey()
    if err != nil {
        return "", "", fmt.Errorf("generate WireGuard key: %w", err)
    }
    pubKey := privKey.PublicKey()
    return privKey.String(), pubKey.String(), nil
}

// GetPublicKey returns the cached public key from disk config (if exists)
// Returns "" if no config exists yet
func (m *Manager) GetPublicKey() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.config != nil {
        return m.config.PublicKey
    }
    // Try loading from disk
    cfg, err := loadConfig(m.configPath)
    if err != nil || cfg == nil {
        return ""
    }
    return cfg.PublicKey
}

// LoadOrGenerateKeyPair loads existing keypair from disk or generates a new one
// Returns (privateKey, publicKey, isNew, error)
func (m *Manager) LoadOrGenerateKeyPair() (string, string, bool, error) {
    cfg, _ := loadConfig(m.configPath)
    if cfg != nil && cfg.PrivateKey != "" {
        logger.Info("[VPN] Loaded existing keypair (pubkey: %.8s...)", cfg.PublicKey)
        return cfg.PrivateKey, cfg.PublicKey, false, nil
    }
    // Generate new
    priv, pub, err := GenerateKeyPair()
    if err != nil {
        return "", "", true, err
    }
    logger.Info("[VPN] Generated new keypair (pubkey: %.8s...)", pub)
    return priv, pub, true, nil
}

// SaveProvisionedConfig saves the full VPN config received from router
func (m *Manager) SaveProvisionedConfig(privKey, pubKey, assignedIP, srvPubKey, srvEndpoint string) error {
    cfg := &VPNConfig{
        AssignedIP:     assignedIP,
        PrivateKey:     privKey,
        PublicKey:      pubKey,
        ServerPubKey:   srvPubKey,
        ServerEndpoint: srvEndpoint,
        ProvisionedAt:  time.Now().Unix(),
    }

    data, err := json.MarshalIndent(cfg, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal vpn config: %w", err)
    }
    if err := os.WriteFile(m.configPath, data, 0600); err != nil {
        return fmt.Errorf("write vpn config: %w", err)
    }

    m.mu.Lock()
    m.config = cfg
    m.mu.Unlock()

    logger.Info("[VPN] ✅ VPN config saved: IP=%s, Server=%s", assignedIP, srvEndpoint)
    return nil
}

// Start initializes the WireGuard userspace tunnel
// Must call SaveProvisionedConfig first
func (m *Manager) Start() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.running {
        return nil
    }

    cfg := m.config
    if cfg == nil {
        var err error
        cfg, err = loadConfig(m.configPath)
        if err != nil {
            return fmt.Errorf("load vpn config: %w", err)
        }
    }

    // Parse assigned IP
    localAddr, err := netip.ParseAddr(cfg.AssignedIP)
    if err != nil {
        return fmt.Errorf("parse assigned IP %q: %w", cfg.AssignedIP, err)
    }

    // Create virtual TUN + netstack
    tun, tnet, err := netstack.CreateNetTUN(
        []netip.Addr{localAddr},
        []netip.Addr{netip.MustParseAddr("8.8.8.8")}, // DNS (unused)
        1420, // MTU
    )
    if err != nil {
        return fmt.Errorf("create netstack tun: %w", err)
    }

    // Create WireGuard device
    dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "[WG] "))

    // Configure WireGuard via IPC (wgctrl-style config string)
    privKey, err := wgtypes.ParseKey(cfg.PrivateKey)
    if err != nil {
        return fmt.Errorf("parse private key: %w", err)
    }
    srvKey, err := wgtypes.ParseKey(cfg.ServerPubKey)
    if err != nil {
        return fmt.Errorf("parse server pub key: %w", err)
    }

    wgConf := fmt.Sprintf(`private_key=%s
[Peer]
public_key=%s
endpoint=%s
allowed_ips=0.0.0.0/0
persistent_keepalive=15
`, privKey.String(), srvKey.String(), cfg.ServerEndpoint)

    if err := dev.IpcSet(wgConf); err != nil {
        return fmt.Errorf("configure wireguard: %w", err)
    }

    if err := dev.Up(); err != nil {
        return fmt.Errorf("bring up wireguard device: %w", err)
    }

    m.tun = tnet
    m.dev = dev
    m.running = true
    m.config = cfg

    logger.Info("[VPN] ✅ WireGuard tunnel started: LocalIP=%s → ServerEndpoint=%s", cfg.AssignedIP, cfg.ServerEndpoint)
    return nil
}

// DialUDP creates a UDP connection inside the WireGuard tunnel
// to the given remote address (e.g., "10.8.0.1:14550")
func (m *Manager) DialUDP(remoteAddr string) (net.Conn, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if !m.running || m.tun == nil {
        return nil, fmt.Errorf("VPN tunnel not running")
    }

    conn, err := m.tun.Dial("udp", remoteAddr)
    if err != nil {
        return nil, fmt.Errorf("VPN dial UDP %s: %w", remoteAddr, err)
    }
    return conn, nil
}

// IsRunning returns true if tunnel is active
func (m *Manager) IsRunning() bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.running
}

// Stop shuts down the WireGuard tunnel
func (m *Manager) Stop() {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.dev != nil {
        m.dev.Down()
        m.dev = nil
    }
    m.tun = nil
    m.running = false
    logger.Info("[VPN] WireGuard tunnel stopped")
}

// loadConfig reads VPN config from disk
func loadConfig(path string) (*VPNConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg VPNConfig
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

> **Kiểm tra (User):** Chạy `go build ./vpn/...`. Nếu không có lỗi compile, giai đoạn 4 thành công.

---

## Giai Đoạn 5: Tích Hợp VPN Provisioning vào Auth Client

### 5.1 Lý do
Sau khi Auth thành công và có Session Token, client gửi gói `0xB0` để xin VPN config. Sau khi nhận `0xB1` thành công, kích hoạt VPN tunnel và trả về tín hiệu cho Forwarder.

### 5.2 Thêm phương thức VPN vào [auth/client.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go)

**Import thêm `vpn` package** vào import block của [auth/client.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go):
```go
import (
    // ... existing imports ...
    "DroneBridge/vpn"
)
```

**Thêm trường VPN vào struct [Client](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go#19-51):**

Tìm struct [Client](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go#19-51) (khoảng line 19), thêm vào cuối:
```go
    // VPN Provisioning
    vpnManager     *vpn.Manager
    vpnReadyCh     chan struct{} // Closed when VPN is ready
    vpnPrivateKey  string       // Current WG private key (in-memory)
    vpnPublicKey   string       // Corresponding public key
```

**Thêm phương thức khởi tạo VPN vào [NewClient](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go#52-78):**

Trong hàm [NewClient](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go#52-78), trước `return &Client{...}`, thêm:
```go
    // (vpnManager và channel được khởi tạo trong block return bên dưới)
```

Và trong block `return &Client{...}`, thêm:
```go
        vpnReadyCh:          make(chan struct{}),
```

**Thêm method `SetVPNManager`:**

```go
// SetVPNManager sets the VPN manager for this auth client
// Call this BEFORE Start() if VPN is enabled
func (c *Client) SetVPNManager(mgr *vpn.Manager) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.vpnManager = mgr
}
```

**Thêm method `WaitForVPN`:**

```go
// WaitForVPN blocks until VPN tunnel is ready or timeout
// Returns true if VPN is ready, false if timeout
func (c *Client) WaitForVPN(timeout time.Duration) bool {
    select {
    case <-c.vpnReadyCh:
        return true
    case <-time.After(timeout):
        return false
    }
}
```

**Thêm method `RequestVPNProvisioning`:**

```go
// RequestVPNProvisioning sends 0xB0 to router and handles 0xB1 response
// MUST be called AFTER authentication succeeds (session token available)
// Runs synchronously - call in a goroutine if needed
func (c *Client) RequestVPNProvisioning() error {
    c.mu.RLock()
    token := c.sessionToken
    conn := c.conn
    mgr := c.vpnManager
    c.mu.RUnlock()

    if mgr == nil {
        return fmt.Errorf("VPN manager not set")
    }
    if token == "" {
        return fmt.Errorf("no session token available for VPN provisioning")
    }
    if conn == nil {
        return fmt.Errorf("no TCP connection available")
    }

    // Step 1: Load or generate keypair
    privKey, pubKey, isNew, err := mgr.LoadOrGenerateKeyPair()
    if err != nil {
        return fmt.Errorf("VPN keypair: %w", err)
    }
    if isNew {
        log.Printf("[VPN] New keypair generated (pubKey: %.8s...)", pubKey)
    }

    // Lưu keypair vào memory
    c.mu.Lock()
    c.vpnPrivateKey = privKey
    c.vpnPublicKey = pubKey
    c.mu.Unlock()

    // Step 2: Send 0xB0 request
    req := &VPNProvisionRequest{
        SessionToken: token,
        WGPublicKey:  pubKey,
    }
    packet := SerializeVPNProvisionRequest(req)

    c.tcpMu.Lock()
    conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
    _, err = conn.Write(packet)
    conn.SetWriteDeadline(time.Time{})
    c.tcpMu.Unlock()

    if err != nil {
        return fmt.Errorf("send VPN provision request: %w", err)
    }
    log.Printf("[VPN] ✓ Sent 0xB0 VPN_PROVISION_REQUEST (pubKey: %.8s...)", pubKey)

    // Step 3: Receive 0xB1 response
    // NOTE: Response đến qua vòng lặp đọc TCP chính (keepaliveLoop đọc 0xB1)
    // Nên đây chúng ta PHẢI đọc trực tiếp với timeout ngắn,
    // hoặc dùng channel để nhận từ readLoop. 
    // Cách đơn giản nhất cho PoC: đọc trực tiếp với deadline.
    c.tcpMu.Lock()
    conn.SetReadDeadline(time.Now().Add(10 * time.Second))
    buf := make([]byte, 4096)
    n, err := conn.Read(buf)
    conn.SetReadDeadline(time.Time{})
    c.tcpMu.Unlock()

    if err != nil {
        return fmt.Errorf("receive VPN provision ack: %w", err)
    }

    ack, err := ParseVPNProvisionAck(buf[:n])
    if err != nil {
        return fmt.Errorf("parse VPN provision ack: %w", err)
    }
    if ack.Result != ResultSuccess {
        return fmt.Errorf("VPN provisioning failed (errorCode=%d)", ack.ErrorCode)
    }

    log.Printf("[VPN] ✅ VPN provisioned! IP=%s, Server=%s", ack.AssignedIP, ack.ServerEndpoint)

    // Step 4: Save config to disk
    if err := mgr.SaveProvisionedConfig(privKey, pubKey, ack.AssignedIP, ack.ServerPubKey, ack.ServerEndpoint); err != nil {
        return fmt.Errorf("save VPN config: %w", err)
    }

    // Step 5: Start WireGuard tunnel
    if err := mgr.Start(); err != nil {
        return fmt.Errorf("start VPN tunnel: %w", err)
    }

    // Step 6: Signal VPN is ready
    select {
    case <-c.vpnReadyCh:
        // Already closed (duplicate call), ignore
    default:
        close(c.vpnReadyCh)
    }

    return nil
}
```

> **Kiểm tra (User):** Chạy `go build ./auth/...`. Nếu không có lỗi, tiếp tục.

---

## Giai Đoạn 6: Tích Hợp VPN vào [main.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/main.go) và [forwarder/forwarder.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go)

### 6.1 Sửa [main.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/main.go)

**Thêm import `vpn`:**
```go
import (
    // ... existing imports ...
    "DroneBridge/vpn"
)
```

**Thêm khởi tạo VPN Manager sau khi khởi tạo `authClient` (khoảng line 49):**

Sau dòng `authClient := auth.NewClient(...)`, thêm:
```go
    // Initialize VPN Manager if enabled
    var vpnMgr *vpn.Manager
    if cfg.VPN.Enabled {
        vpnMgr = vpn.NewManager(cfg.VPN.ConfigFile)
        authClient.SetVPNManager(vpnMgr)
        logger.Info("[VPN] VPN provisioning ENABLED (config: %s)", cfg.VPN.ConfigFile)
    } else {
        logger.Info("[VPN] VPN provisioning DISABLED")
    }
```

**Thêm logic khởi tạo VPN sau Auth thành công** (sau vòng lặp chờ `authClient.IsAuthenticated()`, khoảng line 166):

```go
    // STEP 4.5: Request VPN Provisioning (if enabled)
    if cfg.VPN.Enabled && vpnMgr != nil {
        logger.Info("[VPN] 📡 Requesting VPN provisioning from router...")
        go func() {
            // Chờ 1s để đảm bảo kết nối TCP ổn định sau auth
            time.Sleep(1 * time.Second)
            if err := authClient.RequestVPNProvisioning(); err != nil {
                logger.Warn("[VPN] ⚠️ VPN provisioning failed: %v - running without VPN", err)
                return
            }
            logger.Info("[VPN] ✅ VPN tunnel active - MAVLink will route via WireGuard")
        }()

        // Optional: chờ VPN sẵn sàng trước khi forward (tối đa 15s)
        if authClient.WaitForVPN(15 * time.Second) {
            logger.Info("[VPN] ✅ VPN ready before forwarder start")
        } else {
            logger.Warn("[VPN] ⏱️ VPN not ready in time - starting forwarder without VPN (will switch when ready)")
        }
    }
```

**Truyền `vpnMgr` vào forwarder:**

Sửa lệnh tạo forwarder (khoảng line 145):
```go
    // CŨBEFORE:
    fwd, err := forwarder.New(cfg, nil, listenerNode)
    // SAU:
    fwd, err := forwarder.NewWithVPN(cfg, nil, listenerNode, vpnMgr)
```

### 6.2 Sửa [forwarder/forwarder.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go)

**Thêm import `vpn`:**
```go
import (
    // ... existing imports ...
    "DroneBridge/vpn"
)
```

**Thêm trường `vpnManager` vào struct [Forwarder](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go#53-85):**
```go
type Forwarder struct {
    // ... existing fields ...
    vpnManager *vpn.Manager // WireGuard VPN manager (nil if VPN disabled)
}
```

**Thêm hàm `NewWithVPN`** (đặt sau hàm [New](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go#363-488) hiện tại):

```go
// NewWithVPN creates a forwarder with an optional VPN manager
// vpnMgr can be nil if VPN is disabled
func NewWithVPN(cfg *config.Config, authClient *auth.Client, listenerNode *gomavlib.Node, vpnMgr *vpn.Manager) (*Forwarder, error) {
    fwd, err := New(cfg, authClient, listenerNode)
    if err != nil {
        return nil, err
    }
    fwd.vpnManager = vpnMgr
    return fwd, nil
}
```

**Sửa hàm [receiveAndForward](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go#580-716) để dùng VPN khi sẵn sàng:**

Tìm đoạn code forward raw frame (khoảng line 697):
```go
// TRƯỚC:
if err := f.senderNode.WriteFrameAll(e.Frame); err != nil {

// SAU: thêm logic chuyển hướng qua VPN nếu tunnel active
```

Sửa block forward thành:

```go
                        // Check if VPN is ready - route MAVLink through VPN tunnel
                        if f.vpnManager != nil && f.vpnManager.IsRunning() {
                            // Forward via WireGuard tunnel
                            vpnAddr := fmt.Sprintf("%s:%d", f.cfg.VPN.RouterVPNIP, f.cfg.VPN.VPNPort)
                            vpnConn, err := f.vpnManager.DialUDP(vpnAddr)
                            if err != nil {
                                logger.Warn("[VPN_FORWARD] Failed to dial VPN: %v, falling back to UDP", err)
                                // Fallback to normal UDP
                                if err := f.senderNode.WriteFrameAll(e.Frame); err != nil {
                                    logger.Error("[FORWARD] Fallback UDP failed: %v", err)
                                    metrics.Global.IncFailedSend(msgTypeName)
                                } else {
                                    metrics.Global.IncSent(msgTypeName)
                                }
                            } else {
                                _, writeErr := vpnConn.Write(e.Frame.GetBytes())
                                vpnConn.Close()
                                if writeErr != nil {
                                    logger.Error("[VPN_FORWARD] Write failed: %v", writeErr)
                                    metrics.Global.IncFailedSend(msgTypeName)
                                } else {
                                    logger.Debug("[VPN_FORWARD] ✓ %s via WireGuard", msgTypeName)
                                    metrics.Global.IncSent(msgTypeName)
                                }
                            }
                        } else {
                            // Normal UDP forward (no VPN or VPN not ready)
                            if err := f.senderNode.WriteFrameAll(e.Frame); err != nil {
                                logger.Error("[FORWARD] Failed to forward frame %s: %v", msgTypeName, err)
                                metrics.Global.IncFailedSend(msgTypeName)
                            } else {
                                logger.Debug("[FORWARD] %s #%d", msgTypeName, forwardedCount)
                                metrics.Global.IncSent(msgTypeName)
                            }
                        }
```

> **⚠️ Lưu ý Performance**: Cách `DialUDP` mỗi message sẽ có overhead. Bước tối ưu hóa sau là cache kết nối UDP trong Forwarder. Hiện tại đây là PoC đúng logic.

---

## 🧪 Test Logic Từng Bước

### Test 1: Kiểm tra Compile

```bash
# Trong thư mục Pi_CM5_DroneBridgeService
go build ./...
```
✅ Không có lỗi = OK

### Test 2: Kiểm tra Keypair Generation

```go
// Tạo file test/vpn_test.go
package test

import (
    "testing"
    "DroneBridge/vpn"
)

func TestKeyPairGeneration(t *testing.T) {
    priv, pub, err := vpn.GenerateKeyPair()
    if err != nil {
        t.Fatal(err)
    }
    if len(pub) != 44 { // Base64 WG key = 44 chars
        t.Fatalf("invalid pubkey length: %d", len(pub))
    }
    t.Logf("PrivKey: %.8s...", priv)
    t.Logf("PubKey: %.8s...", pub)
}
```

```bash
go test ./... -run TestKeyPairGeneration -v
```

### Test 3: Kiểm tra Protocol Serialization

```go
func TestVPNProtocol(t *testing.T) {
    req := &auth.VPNProvisionRequest{
        SessionToken: "test-session-token-12345",
        WGPublicKey:  "abc123def456abc123def456abc123def456abc123de=",
    }
    data := auth.SerializeVPNProvisionRequest(req)
    if data[0] != 0xB0 {
        t.Fatalf("wrong message type: 0x%02x", data[0])
    }
    t.Logf("Packet size: %d bytes", len(data))
}
```

### Test 4: Kiểm tra Config Load

```bash
# Chạy với flag debug để xem VPN config được load
./dronebridge --config config.yaml --log debug
```

Phải thấy log:
```
[VPN] VPN provisioning ENABLED (config: .drone_vpn.json)
```

### Test 5: End-to-End VPN Provisioning (cần Server đang chạy)

1. Đảm bảo `hybrid_router-server` đã implement xử lý gói `0xB0` và `0xB1`.
2. Đảm bảo backend API `/api/v1/internal/drones/:uuid/vpn-provision` đang hoạt động.
3. Build và chạy drone:
   ```bash
   ./dronebridge --config config.yaml
   ```
4. Quan sát log thành công:
   ```
   [AUTH] ✅ Authentication successful!
   [VPN] ✓ Sent 0xB0 VPN_PROVISION_REQUEST
   [VPN] ✅ VPN provisioned! IP=10.8.1.X, Server=45.117.171.237:51820
   [VPN] ✅ WireGuard tunnel started
   [VPN_FORWARD] ✓ HEARTBEAT via WireGuard
   ```

---

## 👁️ Cách User Giám Sát Hệ Thống

### Giám Sát Log Drone

SSH vào Pi CM5 và xem log:
```bash
journalctl -fu dronebridge.service
# Hoặc nếu chạy trực tiếp:
./dronebridge 2>&1 | tee drone.log
```

**Log cần thấy khi VPN hoạt động:**
```log
[AUTH] ✅ Authentication successful!
[SESSION] ✅ Session ready!
[VPN] 📡 Requesting VPN provisioning from router...
[VPN] ✓ Sent 0xB0 VPN_PROVISION_REQUEST (pubKey: mGo0+CYz...)
[VPN] ✅ VPN provisioned! IP=10.8.1.5, Server=45.117.171.237:51820
[VPN] ✅ WireGuard tunnel started: LocalIP=10.8.1.5 → ServerEndpoint=45.117.171.237:51820
[VPN_FORWARD] ✓ HEARTBEAT via WireGuard
```

### Kiểm tra File `.drone_vpn.json`

Sau khi provisioning thành công, file này sẽ xuất hiện:
```bash
cat .drone_vpn.json
```
Nội dung mẫu:
```json
{
  "assigned_ip": "10.8.1.5",
  "private_key": "...",
  "public_key": "abc123...",
  "server_pub_key": "mGo0+CYziYGED5KeuX+N5D2yvflEGnRJ8I1eSS+Zq0A=",
  "server_endpoint": "45.117.171.237:51820",
  "provisioned_at": 1741826296
}
```

### Kiểm tra Từ Server Side

Trên server, kiểm tra WireGuard nếu đang dùng kernel WG:
```bash
sudo wg show
# Sẽ thấy peer với IP 10.8.1.5 mới được thêm
```

Hoặc kiểm tra qua Backend API:
```bash
curl http://45.117.171.237:8081/api/v1/database/tables/drones/records | jq '.records[] | {name, vpn_assigned_ip, vpn_public_key}'
```

### Kiểm Tra Tunnel Hoạt Động

Từ server (trong VPN subnet `10.8.0.0/16`):
```bash
# Gói tin đến từ 10.8.1.5 nghĩa là MAVLink đang đi qua VPN
tcpdump -i wg0 udp port 14550
```

---

## ⚠️ Các Lỗi Thường Gặp và Cách Xử Lý

| Lỗi | Nguyên nhân | Xử lý |
|---|---|---|
| `VPN manager not set` | `SetVPNManager` chưa được gọi | Gọi `authClient.SetVPNManager(vpnMgr)` trước [Start()](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go#528-564) |
| `no session token` | Chưa auth xong trước khi request VPN | Đảm bảo `WaitForAuthenticated()` xong |
| `parse private key` | File `.drone_vpn.json` bị corrupt | Xóa file để tạo keypair mới |
| `Failed to dial VPN` | Tunnel chưa ready hoặc server peer chưa được cập nhật | Kiểm tra `wg show` trên server |
| Build error: `package not found` | Chưa chạy `go get` | Chạy `go get golang.zx2c4.com/wireguard@latest` |

---

## 📚 Checklist Hoàn Thành cho AI Agent

- [ ] **Giai đoạn 1**: Thêm dependency `golang.zx2c4.com/wireguard` vào [go.mod](file:///d:/FirmwareGit/QCC-CloudControlStation/drone-fleet-backend/go.mod)
- [ ] **Giai đoạn 2**: Thêm `VPNConfig` struct vào [config/config.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config/config.go) + thêm section `vpn` vào [config.yaml](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/config.yaml)
- [ ] **Giai đoạn 3**: Thêm `MsgVPNProvisionRequest/Ack` constants + `VPNProvisionRequest/Ack` structs + Serialize/Parse functions vào [auth/protocol.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/protocol.go)
- [ ] **Giai đoạn 4**: Tạo file mới `vpn/manager.go` với đầy đủ [Manager](file:///d:/FirmwareGit/QCC-CloudControlStation/drone-fleet-backend/internal/api/server.go#104-110), `GenerateKeyPair`, [Start](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go#528-564), `DialUDP`
- [ ] **Giai đoạn 5**: Thêm `SetVPNManager`, `WaitForVPN`, `RequestVPNProvisioning` methods vào [auth/client.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/auth/client.go)
- [ ] **Giai đoạn 6a**: Sửa [main.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/main.go) để khởi tạo `vpn.Manager` và gọi provisioning sau auth
- [ ] **Giai đoạn 6b**: Thêm `NewWithVPN` và logic chuyển hướng VPN vào [forwarder/forwarder.go](file:///d:/FirmwareGit/QCC-CloudControlStation/Pi_CM5_DroneBridgeService/forwarder/forwarder.go)
- [ ] **Test**: Chạy `go build ./...` không lỗi
- [ ] **Test**: Chạy unit test `go test ./...`
- [ ] **Deploy**: Build và chạy trên Pi CM5, xác nhận `[VPN] ✅ WireGuard tunnel started` trong log

---

*Tài liệu được tạo bởi AI Agent vào 2026-03-13. Dựa trên phân tích [AI_IMPLEMENTATION_GUIDE_VPN_SERVER.md](file:///d:/FirmwareGit/QCC-CloudControlStation/wireguard_setup/AI_IMPLEMENTATION_GUIDE_VPN_SERVER.md) và codebase `Pi_CM5_DroneBridgeService`.*
