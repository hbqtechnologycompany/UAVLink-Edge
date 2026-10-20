package vpn

import (
"context"
"encoding/json"
"encoding/hex"
"fmt"
"net"
"net/netip"
"os"
"strings"
"sync"
"time"

"DroneBridge/logger"

"golang.zx2c4.com/wireguard/conn"
"golang.zx2c4.com/wireguard/device"
"golang.zx2c4.com/wireguard/tun/netstack"
"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type VPNConfig struct {
AssignedIP     string `json:"assigned_ip"`
PrivateKey     string `json:"private_key"`
PublicKey      string `json:"public_key"`
ServerPubKey   string `json:"server_pub_key"`
ServerEndpoint string `json:"server_endpoint"`
ProvisionedAt  int64  `json:"provisioned_at"`
}

type Manager struct {
configPath string
mu         sync.RWMutex
config     *VPNConfig
tun        *netstack.Net
dev        *device.Device
running    bool
startedAt  time.Time
}

func NewManager(configPath string) *Manager {
return &Manager{configPath: configPath}
}

func GenerateKeyPair() (string, string, error) {
privKey, err := wgtypes.GeneratePrivateKey()
if err != nil {
return "", "", fmt.Errorf("generate WireGuard key: %w", err)
}
pubKey := privKey.PublicKey()
return privKey.String(), pubKey.String(), nil
}

func (m *Manager) GetPublicKey() string {
m.mu.RLock()
defer m.mu.RUnlock()
if m.config != nil {
return m.config.PublicKey
}
cfg, err := loadConfig(m.configPath)
if err != nil || cfg == nil {
return ""
}
return cfg.PublicKey
}

func (m *Manager) GetAssignedIP() string {
m.mu.RLock()
defer m.mu.RUnlock()
if m.config != nil && m.config.AssignedIP != "" {
ip := m.config.AssignedIP
if idx := strings.Index(ip, "/"); idx != -1 {
return ip[:idx]
}
return ip
}
cfg, _ := loadConfig(m.configPath)
if cfg != nil && cfg.AssignedIP != "" {
ip := cfg.AssignedIP
if idx := strings.Index(ip, "/"); idx != -1 {
return ip[:idx]
}
return ip
}
return ""
}

func (m *Manager) LoadOrGenerateKeyPair() (string, string, bool, error) {
cfg, _ := loadConfig(m.configPath)
if cfg != nil && cfg.PrivateKey != "" {
return cfg.PrivateKey, cfg.PublicKey, false, nil
}
priv, pub, err := GenerateKeyPair()
if err != nil {
return "", "", true, err
}
return priv, pub, true, nil
}

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

return nil
}

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

cleanIP := cfg.AssignedIP
if idx := strings.Index(cleanIP, "/"); idx != -1 {
cleanIP = cleanIP[:idx]
}

localAddr, err := netip.ParseAddr(cleanIP)
if err != nil {
return fmt.Errorf("parse assigned IP %q: %w", cleanIP, err)
}

tun, tnet, err := netstack.CreateNetTUN(
[]netip.Addr{localAddr},
[]netip.Addr{netip.MustParseAddr("8.8.8.8")},
1420,
)
if err != nil {
return fmt.Errorf("create netstack tun: %w", err)
}

dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, ""))

privKey, err := wgtypes.ParseKey(cfg.PrivateKey)
if err != nil {
return fmt.Errorf("parse private key: %w", err)
}
srvKey, err := wgtypes.ParseKey(cfg.ServerPubKey)
if err != nil {
return fmt.Errorf("parse server pub key: %w", err)
}

	wgConf := fmt.Sprintf(`private_key=%s
public_key=%s
endpoint=%s
allowed_ip=0.0.0.0/0
persistent_keepalive_interval=15
`, hex.EncodeToString(privKey[:]), hex.EncodeToString(srvKey[:]), cfg.ServerEndpoint)

	if err := dev.IpcSet(wgConf); err != nil {
		return fmt.Errorf("configure wireguard: %w", err)
	}

	if err := dev.Up(); err != nil {
		return fmt.Errorf("bring up wireguard device: %w", err)
	}

	m.tun = tnet
	m.dev = dev
	m.running = true
	m.startedAt = time.Now()
	m.config = cfg

return nil
}

// StartedAt returns when the tunnel was last brought up (zero when stopped).
func (m *Manager) StartedAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.running {
		return time.Time{}
	}
	return m.startedAt
}

// HandshakeAge returns the time since the last completed WireGuard handshake.
// ok is false while the tunnel is down or before the first handshake completes.
func (m *Manager) HandshakeAge() (time.Duration, bool) {
	m.mu.RLock()
	dev := m.dev
	running := m.running
	m.mu.RUnlock()
	if !running || dev == nil {
		return 0, false
	}
	state, err := dev.IpcGet()
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(state, "\n") {
		v, found := strings.CutPrefix(line, "last_handshake_time_sec=")
		if !found {
			continue
		}
		var secs int64
		if _, err := fmt.Sscan(v, &secs); err != nil || secs <= 0 {
			return 0, false
		}
		return time.Since(time.Unix(secs, 0)), true
	}
	return 0, false
}

func (m *Manager) DialUDP(remoteAddr string) (net.Conn, error) {
m.mu.RLock()
defer m.mu.RUnlock()

if !m.running || m.tun == nil {
return nil, fmt.Errorf("VPN tunnel not running yet")
}

addr, err := net.ResolveUDPAddr("udp", remoteAddr)
if err != nil {
return nil, err
}

conn, err := m.tun.DialUDP(nil, addr)
if err != nil {
return nil, fmt.Errorf("VPN dial UDP %s: %w", remoteAddr, err)
}
return conn, nil
}

func (m *Manager) DialContextTCP(ctx context.Context, remoteAddr string) (net.Conn, error) {
m.mu.RLock()
defer m.mu.RUnlock()

if !m.running || m.tun == nil {
return nil, fmt.Errorf("VPN tunnel not running yet")
}

addr, err := net.ResolveTCPAddr("tcp", remoteAddr)
if err != nil {
return nil, err
}

conn, err := m.tun.DialContextTCP(ctx, addr)
if err != nil {
return nil, fmt.Errorf("VPN dial TCP %s: %w", remoteAddr, err)
}
return conn, nil
}

func (m *Manager) IsRunning() bool {
m.mu.RLock()
defer m.mu.RUnlock()
return m.running
}

func (m *Manager) Stop() {
m.mu.Lock()
defer m.mu.Unlock()
if m.dev != nil {
m.dev.Down()
m.dev.Close()
m.dev = nil
}
m.tun = nil
m.running = false
logger.Info("[VPN] WireGuard userspace tunnel stopped")
}

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
