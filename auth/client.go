package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"DroneBridge/metrics"
	"DroneBridge/network"
	"DroneBridge/vpn"
)

var (
	authForceFreshConn = strings.ToLower(strings.TrimSpace(os.Getenv("DRONEBRIDGE_AUTH_FORCE_FRESH_CONN"))) != "0"
	authChallengeReadTimeout = envDurationSeconds("DRONEBRIDGE_AUTH_CHALLENGE_TIMEOUT_S", 15)
	authAckReadTimeout = envDurationSeconds("DRONEBRIDGE_AUTH_ACK_TIMEOUT_S", 15)
	refreshAckReadTimeout = envDurationSeconds("DRONEBRIDGE_REFRESH_ACK_TIMEOUT_S", 8)
)

// DisconnectCause classifies why the server session is unreachable.
type DisconnectCause string

const (
	DisconnectNone            DisconnectCause = ""
	DisconnectNetwork         DisconnectCause = "network_error"
	DisconnectSessionInvalid  DisconnectCause = "session_invalid"
	DisconnectAuthFailed      DisconnectCause = "auth_failed"
	DisconnectServerTransient DisconnectCause = "server_transient"
)

// ErrRefreshSkipped means refresh was intentionally skipped (e.g. recent IP change).
var ErrRefreshSkipped = errors.New("session refresh skipped")

func envDurationSeconds(key string, defaultSec int) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return time.Duration(defaultSec) * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(defaultSec) * time.Second
	}
	return time.Duration(n) * time.Second
}

// Client handles drone authentication with the router
type Client struct {
	host              string
	port              int
	droneUUID         string // UUID from drones_v2 table
	sharedSecret      string // Shared secret for registration
	secret            string // Loaded secret key (in-memory cache)
	configPath        string // Path to config file for saving updates
	keepaliveInterval time.Duration
	sessionToken      string
	expiresAt         time.Time
	refreshInterval   time.Duration // Server-recommended refresh interval

	conn              net.Conn
	running           bool
	stopCh            chan struct{}
	mu                sync.RWMutex
	tcpMu             sync.Mutex // For synchronizing TCP operations
	reconnectDelay     time.Duration
	reconnectInterval  time.Duration
	nextReconnectAt    time.Time
	serverReachable    bool
	everConnected      bool
	disconnectCause    DisconnectCause
	lastRefreshOK      time.Time
	previousLocalIP    string        // Track previous local IP for change detection
	lastIPChangeTime  time.Time     // Track last IP change time
	ipChangeThreshold time.Duration // Minimum time between IP changes before retrying refresh

	// API Key management channels
	apiKeyRespCh        chan *APIKeyResponse
	apiKeyRevokeAckCh   chan *APIKeyRevokeAck
	apiKeyStatusCh      chan *APIKeyStatusResponse
	apiKeyDeleteAckCh   chan *APIKeyDeleteAck
	sessionRefreshAckCh chan *SessionRefreshAck

	OnNetworkError    func()            // Callback when network error is detected
	OnDisconnectCause func(DisconnectCause) // Callback with classified disconnect reason
	OnSessionRestored func()            // Callback when TCP session verified / auth recovered
	OnStatusMessage   func(string)      // Callback for status messages (for UI)

	// VPN Provisioning
	vpnManager   *vpn.Manager
	vpnReadyCh   chan struct{}
	vpnPublicKey string
	vpnRouterIP  string

	// One-time registration metadata (REGISTER_INIT v2)
	vehicleType uint8
	model       string
}

// NewClient creates a new authentication client using UUID-based protocol
func NewClient(host string, port int, droneUUID string, sharedSecret string, keepaliveInterval int) *Client {
	// If UUID is empty, try to get or generate one
	if droneUUID == "" {
		droneUUID = getOrGenerateUUID()
		log.Printf("[AUTH] No UUID provided in config, using auto-generated: %s", droneUUID)
	}

	return &Client{
		host:                host,
		port:                port,
		droneUUID:           droneUUID,
		sharedSecret:        sharedSecret,
		secret:              "",            // Will be loaded on demand
		configPath:          "config.yaml", // Default config path
		keepaliveInterval:   time.Duration(keepaliveInterval) * time.Second,
		stopCh:              make(chan struct{}),
		reconnectDelay:      5 * time.Second,
		reconnectInterval:   5 * time.Minute,
		ipChangeThreshold:   10 * time.Second,
		apiKeyRespCh:        make(chan *APIKeyResponse, 1),
		apiKeyRevokeAckCh:   make(chan *APIKeyRevokeAck, 1),
		apiKeyStatusCh:      make(chan *APIKeyStatusResponse, 1),
		apiKeyDeleteAckCh:   make(chan *APIKeyDeleteAck, 1),
		sessionRefreshAckCh: make(chan *SessionRefreshAck, 1),
		vpnReadyCh:          make(chan struct{}),
	}
}

// resolveDialTarget returns TCP dial target. We now FORCE TCP to always use public network.
// This separates the TCP Auth/Session from the UDP VPN telemetry, ensuring that if the VPN
// tunnel is flapping or reconnecting due to 4G changes, the TCP session will not drop alongside it.
func (c *Client) resolveDialTarget() (string, bool) {
	c.mu.RLock()
	targetHost := c.host
	c.mu.RUnlock()
	return targetHost, false // NEVER use VPN for TCP Auth
}

// dialWithLocalIP creates a TCP connection.
// TCP auth/refresh ONLY uses the public network (không qua VPN tunnel).
// Routing egress theo PBR table 100 — netmon quyết định wwan0/wlan0 (SYSTEM_ARCHITECTURE.md §7.1).
func (c *Client) dialWithLocalIP() (net.Conn, error) {
	targetHost, _ := c.resolveDialTarget()
	publicTarget := fmt.Sprintf("%s:%d", targetHost, c.port)
	return network.DialTCP(10*time.Second, "tcp", publicTarget)
}

func (c *Client) logDialEgress(conn net.Conn) {
	socketIP := ""
	if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok && tcpAddr.IP != nil {
		socketIP = tcpAddr.IP.String()
	}
	iface, egressIP, ok := network.PhysicalEgress()
	if ok && egressIP != "" {
		log.Printf("[AUTH] ✓ Connected (socket=%s, PBR egress=%s via %s)", socketIP, egressIP, iface)
		return
	}
	log.Printf("[AUTH] ✓ Connected from local IP: %s", socketIP)
}

// getOrGenerateUUID attempts to retrieve a persistent UUID for this drone
func getOrGenerateUUID() string {
	uuidFile := ".drone_uuid"

	// 1. Try to read from file
	data, err := os.ReadFile(uuidFile)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}

	// 2. Try to generate from MAC address
	id := getIDFromMAC()
	if id == "" {
		// 3. Fallback to random UUID
		id = generateRandomUUID()
	}

	// Save for next time
	os.WriteFile(uuidFile, []byte(id), 0644)
	return id
}

func getIDFromMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback == 0 && iface.HardwareAddr != nil {
			mac := iface.HardwareAddr.String()
			if mac != "" {
				// Format: 00:11:22:33:44:55 -> 00112233-4455-0000-0000-000000000000 (just an example)
				cleanMAC := strings.ReplaceAll(mac, ":", "")
				if len(cleanMAC) >= 12 {
					return fmt.Sprintf("%s-%s-%s-%s-%s",
						cleanMAC[:8], cleanMAC[8:12], "5555", "8888", "999999999999")
				}
			}
		}
	}
	return ""
}

func generateRandomUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "static-fallback-uuid-0001"
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Start begins authentication and keepalive
func (c *Client) Start() error {
	c.mu.RLock()
	if c.running {
		c.mu.RUnlock()
		log.Printf("[AUTH] ⓘ Auth client already running, skipping Start()")
		return nil
	}
	c.mu.RUnlock()

	log.Printf("[AUTH] Starting authentication client for drone UUID=%s", c.droneUUID)

	// Check if already have valid session from REGISTER
	c.mu.RLock()
	hasValidSession := c.sessionToken != "" && time.Now().Before(c.expiresAt)
	c.mu.RUnlock()

	if hasValidSession {
		// Session created during REGISTER - just start keepalive
		log.Printf("[AUTH] ✓ Valid session from REGISTER flow, starting keepalive")
	} else {
		// No session yet - perform AUTH
		err := c.authenticate()
		if err != nil {
			return fmt.Errorf("initial authentication failed: %w", err)
		}
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	// Start keepalive goroutine (immediate SESSION_REFRESH happens inside keepaliveLoop).
	go c.keepaliveLoop()

	c.mu.RLock()
	interval := c.refreshInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	c.mu.RUnlock()
	log.Printf("[AUTH] ✅ Authenticated - keepalive active every %.0fs", interval.Seconds())
	return nil
}

// SetRegistrationMeta sets vehicle_type and model for REGISTER_INIT v2.
// Trailer is omitted when both are zero/empty (legacy v1 packet).
func (c *Client) SetRegistrationMeta(vehicleType uint8, model string) {
	if len(model) > MaxRegisterModelLen {
		model = model[:MaxRegisterModelLen]
	}
	c.mu.Lock()
	c.vehicleType = vehicleType
	c.model = model
	c.mu.Unlock()
}

// Register performs the one-time registration process
// Flow: REGISTER_INIT(UUID) → REGISTER_CHALLENGE → REGISTER_RESPONSE(HMAC-Shared) → REGISTER_ACK(Secret+Session)
func (c *Client) Register() error {
	if c.sharedSecret == "" {
		return fmt.Errorf("shared secret is required for registration")
	}

	log.Printf("========================================")
	log.Printf("⚠️  [REGISTER] ONE-TIME REGISTRATION STARTING")
	log.Printf("⚠️  [REGISTER] UUID: %s", c.droneUUID)
	log.Printf("⚠️  [REGISTER] This should only happen ONCE.")
	log.Printf("⚠️  [REGISTER] If you see this repeatedly, --register")
	log.Printf("⚠️  [REGISTER] must NOT be in the systemd service file!")
	log.Printf("========================================")
	targetHost, _ := c.resolveDialTarget()
	log.Printf("[REGISTER] Connecting to %s:%d...", targetHost, c.port)

	// Connect to auth server (using local bind IP if configured)
	conn, err := c.dialWithLocalIP()
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	// DO NOT defer conn.Close() - we want to keep this connection alive!

	// Enable TCP keepalive to prevent disconnects
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		log.Printf("[REGISTER] ✓ TCP keepalive enabled (30s interval)")
	}

	// Step 1: Send REGISTER_INIT
	c.mu.RLock()
	vehicleType := c.vehicleType
	model := c.model
	c.mu.RUnlock()

	init := &RegisterInit{
		DroneUUID:   c.droneUUID,
		VehicleType: vehicleType,
		Model:       model,
	}

	packet := SerializeRegisterInit(init)
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send REGISTER_INIT: %w", err)
	}
	if vehicleType != 0 || model != "" {
		log.Printf("[REGISTER] ▶ REGISTER_INIT v2 sent → server (UUID=%s, vehicle_type=%d, model=%q)", c.droneUUID, vehicleType, model)
	} else {
		log.Printf("[REGISTER] ▶ REGISTER_INIT sent → server (UUID=%s)", c.droneUUID)
	}

	// Step 2: Receive REGISTER_CHALLENGE
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to receive REGISTER_CHALLENGE: %w", err)
	}

	challenge, err := ParseRegisterChallenge(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse REGISTER_CHALLENGE: %w", err)
	}
	log.Printf("[REGISTER] ✓ Received challenge")

	// Step 3: Compute HMAC with SHARED SECRET
	timestamp := uint64(time.Now().Unix())
	hmacSig := ComputeHMAC(c.sharedSecret, c.droneUUID, challenge.Nonce, timestamp)

	// Step 4: Send REGISTER_RESPONSE
	resp := &RegisterResponse{
		DroneUUID: c.droneUUID,
		HMAC:      hmacSig,
		Timestamp: timestamp,
	}

	packet = SerializeRegisterResponse(resp)
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send REGISTER_RESPONSE: %w", err)
	}
	log.Printf("[REGISTER] ✓ Sent REGISTER_RESPONSE")

	// Step 5: Receive REGISTER_ACK with SECRET (no session)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err = conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to receive REGISTER_ACK: %w", err)
	}

	ack, err := ParseRegisterAck(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse REGISTER_ACK: %w", err)
	}

	if ack.Result != ResultSuccess {
		return fmt.Errorf("registration failed (error=%d)", ack.ErrorCode)
	}

	log.Printf("[REGISTER] ✅ Registration successful!")
	log.Printf("[REGISTER] 🔑 Received SECRET KEY (len=%d)", len(ack.SecretKey))

	// Step 6: Save Secret Key
	if err := SaveSecret(c.droneUUID, ack.SecretKey); err != nil {
		return fmt.Errorf("failed to save secret key: %w", err)
	}
	log.Printf("[REGISTER] 💾 Secret key saved to '%s'", SecretFileName)

	// Step 7: Close connection - session will be obtained via AUTH flow
	// This prevents duplicate session creation issue
	conn.Close()
	log.Printf("[REGISTER] ✅ Registration complete. Connection closed.")

	c.mu.Lock()
	c.secret = ack.SecretKey
	c.conn = nil        // Connection closed, will reconnect during AUTH
	c.sessionToken = "" // No session from REGISTER (will get from AUTH)
	c.expiresAt = time.Time{}
	c.mu.Unlock()

	log.Printf("[REGISTER] ℹ️  Session will be obtained during AUTH flow (Start())")

	return nil
}

// computeCombinedKey generates a combined key from shared and private keys
// Logic: SHA256(shared_key + private_key) -> Hex String
func computeCombinedKey(sharedKey, privateKey string) string {
	hash := sha256.Sum256([]byte(sharedKey + privateKey))
	return hex.EncodeToString(hash[:])
}

// ComputeHMACWithKey removed - using ComputeHMAC from hmac.go

// Stop stops the authentication client
func (c *Client) Stop() {
	log.Println("[AUTH] Stopping authentication client...")

	c.mu.Lock()
	c.running = false
	c.mu.Unlock()

	close(c.stopCh)

	if c.conn != nil {
		c.conn.Close()
	}

	log.Println("[AUTH] 👋 Authentication client stopped")
}

// Send sends arbitrary data (e.g. MAVLink) over the authenticated connection
func (c *Client) Send(data []byte) error {
	c.mu.RLock()
	conn := c.conn
	running := c.running
	c.mu.RUnlock()

	if !running || conn == nil {
		return fmt.Errorf("client not connected")
	}

	// Lock TCP write to ensure atomic packets
	c.tcpMu.Lock()
	defer c.tcpMu.Unlock()

	// Set write deadline
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := conn.Write(data)
	conn.SetWriteDeadline(time.Time{}) // Reset

	if err != nil {
		if c.OnNetworkError != nil {
			go c.OnNetworkError()
		}
		return fmt.Errorf("failed to send data: %w", err)
	}

	return nil
}

// GetSessionRefreshPacket returns the serialized SESSION_REFRESH packet
// This is used to send the refresh packet via UDP to register the endpoint
func (c *Client) GetSessionRefreshPacket() ([]byte, error) {
	c.mu.RLock()
	token := c.sessionToken
	uuid := c.droneUUID
	c.mu.RUnlock()

	if token == "" {
		return nil, fmt.Errorf("no session token available")
	}

	req := &SessionRefreshRequest{
		SessionToken: token,
		DroneUUID:    uuid,
	}

	return SerializeSessionRefresh(req), nil
}

// SetReconnectInterval sets how long to wait between full reconnect attempts after failure.
func (c *Client) SetReconnectInterval(seconds int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seconds <= 0 {
		seconds = 300
	}
	c.reconnectInterval = time.Duration(seconds) * time.Second
}

func (c *Client) markConnected() {
	now := time.Now()
	c.mu.Lock()
	c.serverReachable = true
	c.everConnected = true
	c.disconnectCause = DisconnectNone
	c.nextReconnectAt = time.Time{}
	c.lastRefreshOK = now
	c.mu.Unlock()

	metrics.Global.SetAuthStatus("Authenticated")
	metrics.Global.SetDisconnectReason("")
	metrics.Global.SetLastRefreshOK(now)

	if c.OnSessionRestored != nil {
		go c.OnSessionRestored()
	}
}

func (c *Client) markDisconnected(cause DisconnectCause) {
	c.mu.Lock()
	c.serverReachable = false
	c.disconnectCause = cause
	c.mu.Unlock()

	metrics.Global.SetAuthStatus("Disconnected")
	metrics.Global.SetDisconnectReason(string(cause))
	metrics.Global.AddLog("WARN", fmt.Sprintf("Server disconnected: %s", cause))

	if c.OnDisconnectCause != nil {
		go c.OnDisconnectCause(cause)
	}
	if cause == DisconnectNetwork && c.OnNetworkError != nil {
		go c.OnNetworkError()
	}
}

func (c *Client) scheduleReconnectBackoff() {
	c.mu.RLock()
	ever := c.everConnected
	interval := c.reconnectInterval
	c.mu.RUnlock()
	if !ever {
		// Boot / never connected: keep retrying on keepalive interval, not 5-minute backoff.
		return
	}
	c.mu.Lock()
	c.nextReconnectAt = time.Now().Add(interval)
	next := c.nextReconnectAt
	c.mu.Unlock()
	log.Printf("[RECONNECT] ⏳ Next full reconnect attempt at %s (interval %.0fs)",
		next.Format("15:04:05"), interval.Seconds())
}

func (c *Client) reconnectBackoffActive() bool {
	c.mu.RLock()
	ever := c.everConnected
	next := c.nextReconnectAt
	c.mu.RUnlock()
	if !ever {
		return false
	}
	return !next.IsZero() && time.Now().Before(next)
}

// IsAuthenticated returns true only when the server session was verified recently.
func (c *Client) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.serverReachable {
		return false
	}
	if c.sessionToken == "" || !time.Now().Before(c.expiresAt) {
		return false
	}
	// Stale if no successful refresh for too long (server may have restarted).
	staleAfter := c.refreshInterval * 2
	if staleAfter == 0 {
		staleAfter = 60 * time.Second
	}
	if !c.lastRefreshOK.IsZero() && time.Since(c.lastRefreshOK) > staleAfter {
		return false
	}
	return true
}

// IsServerReachable reports live TCP/session health (not just local token expiry).
func (c *Client) IsServerReachable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverReachable
}

// GetDisconnectCause returns the last classified disconnect reason.
func (c *Client) GetDisconnectCause() DisconnectCause {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.disconnectCause
}

// authenticate performs the authentication handshake (UUID-based with Secret Key)
// Flow: AUTH_INIT(UUID) → AUTH_CHALLENGE → AUTH_RESPONSE(HMAC-Combined) → AUTH_ACK(Session)
func (c *Client) authenticate() error {
	resetConn := func(reason string) {
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()
		if reason != "" {
			log.Printf("[AUTH] 🔌 Reset TCP connection: %s", reason)
		}
	}

	// 1. Ensure we have secret key
	if c.secret == "" {
		// Try to load from storage
		uuid, key, err := LoadSecret()
		if err != nil {
			return fmt.Errorf("failed to load secret key (drone not registered?): %w. Run with --register first", err)
		}
		if uuid != c.droneUUID {
			log.Printf("[AUTH] Warn: Secret file UUID (%s) doesn't match config UUID (%s)", uuid, c.droneUUID)
		}
		c.secret = key
		log.Printf("[AUTH] Loaded secret key from storage")
	}

	// 2. Check if we already have a connection (from Register())
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil && authForceFreshConn {
		resetConn("force fresh auth connection")
		conn = nil
	}

	if conn == nil {
		// No existing connection - create new one
		targetHost, _ := c.resolveDialTarget()
		log.Printf("[AUTH] Connecting to %s:%d...", targetHost, c.port)

		newConn, err := c.dialWithLocalIP()
		if err != nil {
			return fmt.Errorf("connection failed: %w", err)
		}

		// Enable TCP keepalive to prevent disconnects
		if tcpConn, ok := newConn.(*net.TCPConn); ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
			log.Printf("[AUTH] ✓ TCP keepalive enabled (30s interval)")
		}

		c.mu.Lock()
		c.conn = newConn
		c.previousLocalIP = newConn.LocalAddr().(*net.TCPAddr).IP.String()
		c.mu.Unlock()

		conn = newConn
		c.logDialEgress(newConn)
	} else {
		log.Printf("[AUTH] ✓ Reusing existing connection from REGISTER")
	}

	// Step 2: Send AUTH_INIT with UUID
	init := &AuthInit{
		DroneUUID: c.droneUUID,
	}

	packet := SerializeAuthInit(init)
	if _, err := conn.Write(packet); err != nil {
		resetConn("AUTH_INIT write failed")
		return fmt.Errorf("failed to send AUTH_INIT: %w", err)
	}
	log.Printf("[AUTH] ✓ Sent AUTH_INIT (UUID=%s)", c.droneUUID)

	// Step 3: Receive AUTH_CHALLENGE
	conn.SetReadDeadline(time.Now().Add(authChallengeReadTimeout))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		resetConn("AUTH_CHALLENGE read failed")
		return fmt.Errorf("failed to receive AUTH_CHALLENGE: %w", err)
	}

	challenge, err := ParseAuthChallenge(buf[:n])
	if err != nil {
		resetConn("AUTH_CHALLENGE parse failed")
		return fmt.Errorf("failed to parse AUTH_CHALLENGE: %w", err)
	}
	log.Printf("[AUTH] ✓ Received challenge")

	// Step 4: Compute HMAC (Combined Key = SHA256(Secret + Shared))
	// If shared secret is not configured, we might use just secret?
	// The requirement is "combined_key (derived from hash(secret_key + shared_secret))"
	// Backend expects combined key if shared secret is known.
	// If shared secret is missing in client config, this will fail if backend enforces combined key.
	// Assuming config has shared secret or it's empty.

	authKey := c.secret
	if c.sharedSecret != "" {
		authKey = computeCombinedKey(c.sharedSecret, c.secret)
		log.Printf("[AUTH] Using COMBINED KEY for authentication")
	} else {
		log.Printf("[AUTH] Warn: No shared secret in config, using RAW SECRET KEY")
	}

	timestamp := uint64(time.Now().Unix())
	hmacSig := ComputeHMAC(authKey, c.droneUUID, challenge.Nonce, timestamp)
	currentIP := "0.0.0.0"
	if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		if tcpAddr.IP != nil {
			currentIP = tcpAddr.IP.String()
		}
	}

	// Step 5: Send AUTH_RESPONSE
	resp := &AuthResponse{
		DroneUUID: c.droneUUID,
		HMAC:      hmacSig,
		Timestamp: timestamp,
		IP:        currentIP,
	}

	packet = SerializeAuthResponse(resp)
	if _, err := conn.Write(packet); err != nil {
		resetConn("AUTH_RESPONSE write failed")
		return fmt.Errorf("failed to send AUTH_RESPONSE: %w", err)
	}
	log.Printf("[AUTH] ✓ Sent AUTH_RESPONSE")

	// Step 6: Receive AUTH_ACK with SESSION
	conn.SetReadDeadline(time.Now().Add(authAckReadTimeout))
	n, err = conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		resetConn("AUTH_ACK read failed")
		return fmt.Errorf("failed to receive AUTH_ACK: %w", err)
	}

	ack, err := ParseAuthAck(buf[:n])
	if err != nil {
		resetConn("AUTH_ACK parse failed")
		return fmt.Errorf("failed to parse AUTH_ACK: %w", err)
	}

	if ack.Result != ResultSuccess {
		resetConn(fmt.Sprintf("AUTH_ACK reject error=%d", ack.ErrorCode))
		return fmt.Errorf("authentication failed (error=%d, wait=%ds)", ack.ErrorCode, ack.WaitSec)
	}

	// AUTH_ACK now contains session token directly
	if ack.SessionToken == "" {
		return fmt.Errorf("authentication successful but no session token received")
	}

	log.Printf("[AUTH] ✅ Authentication successful! (identity verified)")
	metrics.Global.AddLog("INFO", "Authentication successful - UUID: "+c.droneUUID)

	// Store session info
	c.mu.Lock()
	c.sessionToken = ack.SessionToken
	c.expiresAt = time.Unix(int64(ack.ExpiresAt), 0)
	c.refreshInterval = time.Duration(ack.Interval) * time.Second
	c.mu.Unlock()

	metrics.Global.SetSessionInfo(c.expiresAt, c.refreshInterval)
	c.markConnected()

	log.Printf("[SESSION] ✅ Session ready!")
	log.Printf("[SESSION]    Token: %s...", c.sessionToken[:20])
	log.Printf("[SESSION]    Expires: %s", c.expiresAt.Format("2006-01-02 15:04:05"))

	return nil
}

// requestSession requests a session token from the server (after authentication)
func (c *Client) requestSession(conn net.Conn) error {
	log.Printf("[SESSION] 📋 Requesting session...")

	// Get old session token for potential reuse
	c.mu.RLock()
	oldToken := c.sessionToken
	c.mu.RUnlock()

	// Send SESSION_NEW request
	sessionReq := &SessionRequest{
		DroneUUID:       c.droneUUID,
		OldSessionToken: oldToken, // Server may reuse if still valid
	}

	packet := SerializeSessionRequest(sessionReq)
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send SESSION_NEW: %w", err)
	}
	log.Printf("[SESSION] ✓ Sent SESSION_NEW (UUID=%s, oldToken=%s)",
		c.droneUUID, truncateToken(oldToken))

	// Receive SESSION_ACK
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to receive SESSION_ACK: %w", err)
	}

	sessionAck, err := ParseSessionAck(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse SESSION_ACK: %w", err)
	}

	if sessionAck.Result != ResultSuccess {
		return fmt.Errorf("session request failed (error=%d)", sessionAck.ErrorCode)
	}

	// Store session info
	c.mu.Lock()
	c.sessionToken = sessionAck.Token
	c.expiresAt = time.Unix(int64(sessionAck.ExpiresAt), 0)
	c.refreshInterval = time.Duration(sessionAck.Interval) * time.Second
	c.mu.Unlock()

	// Update metrics
	metrics.Global.SetSessionInfo(c.expiresAt, c.refreshInterval)
	c.markConnected()

	log.Printf("[SESSION] ✅ Session ready!")
	log.Printf("[SESSION]    Token: %s...", c.sessionToken[:20])
	log.Printf("[SESSION]    Expires: %s", c.expiresAt.Format("2006-01-02 15:04:05"))
	log.Printf("[SESSION]    Refresh Interval: %.0fs", c.refreshInterval.Seconds())

	return nil
}

// truncateToken returns first 20 chars of token or "none"
func truncateToken(token string) string {
	if token == "" {
		return "none"
	}
	if len(token) > 20 {
		return token[:20] + "..."
	}
	return token
}

// RefreshError represents an error from session refresh with error code
type RefreshError struct {
	Message   string
	ErrorCode uint8 // 0 = no specific code, otherwise see ErrInvalidToken, ErrSessionExpired etc.
}

func (e *RefreshError) Error() string {
	return e.Message
}

// sendRefresh sends SESSION_REFRESH to extend session
// Returns RefreshError with ErrorCode if server rejects the refresh
func (c *Client) sendRefresh() error {
	c.tcpMu.Lock() // 🔒 Lock for entire send+receive cycle
	defer c.tcpMu.Unlock()

	c.mu.RLock()
	token := c.sessionToken
	conn := c.conn
	timeSinceIPChange := time.Since(c.lastIPChangeTime)
	c.mu.RUnlock()

	// Skip refresh if IP changed too recently (avoid re-auth loop)
	if timeSinceIPChange < c.ipChangeThreshold {
		log.Printf("[SESSION_REFRESH] ⏭️ Skipping (IP changed %v ago, threshold: %v)", timeSinceIPChange, c.ipChangeThreshold)
		return ErrRefreshSkipped
	}

	if token == "" {
		return &RefreshError{Message: "no session token", ErrorCode: ErrInvalidToken}
	}

	// Reconnect if connection lost
	if conn == nil {
		log.Printf("[SESSION_REFRESH] Connection lost, attempting to reconnect...")
		if err := c.reconnectTCP(); err != nil {
			return &RefreshError{Message: fmt.Sprintf("failed to reconnect: %v", err)}
		}
		c.mu.RLock()
		conn = c.conn
		c.mu.RUnlock()
	}

	// Send SESSION_REFRESH
	refreshReq := &SessionRefreshRequest{
		SessionToken: token,
		DroneUUID:    c.droneUUID,
	}

	packet := SerializeSessionRefresh(refreshReq)
	if _, err := conn.Write(packet); err != nil {
		return &RefreshError{Message: fmt.Sprintf("failed to send SESSION_REFRESH: %v", err)}
	}
	log.Printf("[SESSION_REFRESH] ✓ Sent SESSION_REFRESH")

	// Receive SESSION_REFRESH_ACK - use shorter timeout to avoid blocking other operations
	conn.SetReadDeadline(time.Now().Add(refreshAckReadTimeout))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // Reset deadline

	if err != nil {
		return &RefreshError{Message: fmt.Sprintf("failed to receive SESSION_REFRESH_ACK: %v", err)}
	}

	ackResp, err := ParseSessionRefreshAck(buf[:n])
	if err != nil {
		return &RefreshError{Message: fmt.Sprintf("failed to parse SESSION_REFRESH_ACK: %v", err)}
	}

	if ackResp.Result != ResultSuccess {
		return &RefreshError{
			Message:   fmt.Sprintf("session refresh rejected (error=%d)", ackResp.ErrorCode),
			ErrorCode: ackResp.ErrorCode,
		}
	}

	// Update expiration
	c.mu.Lock()
	c.expiresAt = time.Unix(int64(ackResp.ExpiresAt), 0)
	refreshInterval := c.refreshInterval
	c.mu.Unlock()

	// Update metrics
	metrics.Global.SetSessionInfo(c.expiresAt, refreshInterval)

	log.Printf("[SESSION_REFRESH] ✓ Session extended (expires: %s)",
		time.Unix(int64(ackResp.ExpiresAt), 0).Format("15:04:05"))

	c.markConnected()
	return nil
}

// handleRefreshFailure classifies the error, updates status, and attempts recovery.
func (c *Client) handleRefreshFailure(err error) {
	if errors.Is(err, ErrRefreshSkipped) {
		return
	}

	var needReauth bool
	var isNetworkError bool
	var isTransientServerError bool
	var cause DisconnectCause

	if refreshErr, ok := err.(*RefreshError); ok {
		switch refreshErr.ErrorCode {
		case ErrInvalidToken, ErrSessionExpired:
			needReauth = true
			cause = DisconnectSessionInvalid
		case ErrInternalError, ErrRateLimited:
			isTransientServerError = true
			cause = DisconnectServerTransient
		default:
			isNetworkError = true
			cause = DisconnectNetwork
		}
	} else {
		isNetworkError = true
		cause = DisconnectNetwork
	}

	if isTransientServerError {
		c.markDisconnected(cause)
		log.Printf("[REFRESH] ⏭️ Server transient error, keeping TCP and retrying next cycle")
		return
	}

	c.markDisconnected(cause)

	if c.reconnectBackoffActive() {
		c.mu.RLock()
		next := c.nextReconnectAt
		c.mu.RUnlock()
		log.Printf("[REFRESH] ⏳ Reconnect backoff active — next attempt at %s", next.Format("15:04:05"))
		return
	}

	metrics.Global.SetAuthStatus("Reconnecting")

	if isNetworkError {
		log.Printf("[REFRESH] 🔌 Network error detected, closing connection for clean reconnect")
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()
	}

	recovered := false

	if needReauth {
		log.Printf("[REFRESH] 🔄 Re-authenticating (session invalid on server)...")
		if err := c.authenticate(); err != nil {
			log.Printf("[AUTH] ❌ Re-authentication failed: %v", err)
			c.markDisconnected(DisconnectAuthFailed)
		} else {
			log.Printf("[AUTH] ✅ Re-authentication successful - Session recovered!")
			recovered = true
		}
	} else if isNetworkError {
		c.mu.RLock()
		tokenValid := c.sessionToken != "" && time.Now().Before(c.expiresAt)
		c.mu.RUnlock()

		if tokenValid {
			log.Printf("[REFRESH] 🔄 Token still valid locally, reconnecting TCP...")
			if err := c.reconnectTCP(); err != nil {
				log.Printf("[REFRESH] ❌ TCP reconnect failed: %v - re-authenticating", err)
				if err2 := c.authenticate(); err2 != nil {
					log.Printf("[AUTH] ❌ Authentication failed: %v", err2)
					c.markDisconnected(DisconnectAuthFailed)
				} else {
					log.Printf("[AUTH] ✅ Authentication successful - Session recovered!")
					recovered = true
				}
			} else {
				log.Printf("[REFRESH] 🔍 TCP reconnected — verifying session with server...")
				if err2 := c.sendRefresh(); err2 != nil {
					if errors.Is(err2, ErrRefreshSkipped) {
						// IP change cooldown — TCP is up; wait for next refresh tick.
					} else if rf2, ok := err2.(*RefreshError); ok &&
						(rf2.ErrorCode == ErrInvalidToken || rf2.ErrorCode == ErrSessionExpired) {
						log.Printf("[REFRESH] ⚠️ Session unknown after server restart (error=%d) - re-authenticating", rf2.ErrorCode)
						if err3 := c.authenticate(); err3 != nil {
							log.Printf("[AUTH] ❌ Re-authentication failed: %v", err3)
							c.markDisconnected(DisconnectAuthFailed)
						} else {
							log.Printf("[AUTH] ✅ Re-authentication successful after server restart!")
							recovered = true
						}
					} else {
						log.Printf("[REFRESH] ⚠️ Post-reconnect refresh failed: %v (will retry next cycle)", err2)
					}
				} else {
					log.Printf("[REFRESH] ✅ Session verified after TCP reconnect")
					recovered = true
				}
			}
		} else {
			log.Printf("[REFRESH] ⚠️ Token expired, re-authenticating...")
			if err := c.authenticate(); err != nil {
				log.Printf("[AUTH] ❌ Re-authentication failed: %v", err)
				c.markDisconnected(DisconnectAuthFailed)
			} else {
				log.Printf("[AUTH] ♻️ Re-authentication successful - Session recovered!")
				recovered = true
			}
		}
	}

	if !recovered {
		c.scheduleReconnectBackoff()
	}
}

// keepaliveLoop runs periodic keepalive messages - TCP session refresh
func (c *Client) keepaliveLoop() {
	// Start refresh ticker with server-recommended interval (default to 30s if not set)
	c.mu.RLock()
	refreshInterval := c.refreshInterval
	if refreshInterval == 0 {
		refreshInterval = 30 * time.Second
	}
	c.mu.RUnlock()

	refreshTicker := time.NewTicker(refreshInterval)
	defer refreshTicker.Stop()

	log.Printf("[KEEPALIVE] Starting refresh every %.0fs", refreshInterval.Seconds())

	// Verify session immediately instead of waiting for first ticker fire.
	c.mu.RLock()
	running := c.running
	c.mu.RUnlock()
	if running {
		if err := c.sendRefresh(); err != nil {
			log.Printf("[REFRESH] ❌ Initial refresh failed: %v", err)
			c.handleRefreshFailure(err)
		}
	}

	for {
		select {
		case <-c.stopCh:
			return

		case <-refreshTicker.C:
			// Send TCP refresh to maintain session
			c.mu.RLock()
			running := c.running
			c.mu.RUnlock()

			if running {
				if err := c.sendRefresh(); err != nil {
					log.Printf("[REFRESH] ❌ Failed: %v", err)
					c.handleRefreshFailure(err)
				}
			}
		}
	}
}

// TriggerReauth performs immediate re-authentication (for session recovery)
func (c *Client) TriggerReauth() error {
	log.Printf("[REAUTH] 🔄 Triggering immediate re-authentication...")
	c.closeTCP()
	return c.authenticate()
}

// TriggerSessionRecovery attempts session refresh first, falls back to re-auth if needed
// Key insight: Session can be reused across IP changes if still valid
func (c *Client) TriggerSessionRecovery() error {
	c.mu.RLock()
	token := c.sessionToken
	expiresAt := c.expiresAt
	running := c.running
	conn := c.conn
	c.mu.RUnlock()

	if !running {
		return fmt.Errorf("auth client not running")
	}

	// Check if we have a valid session that can be refreshed
	if token != "" && time.Now().Before(expiresAt) {
		log.Printf("[SESSION_RECOVERY] 🔄 Session still valid (expires %s), attempting refresh...",
			expiresAt.Format("15:04:05"))

		// Try session refresh first (just extend TTL, keep same token)
		if err := c.sendRefresh(); err != nil {
			log.Printf("[SESSION_RECOVERY] ⚠️ Session refresh failed: %v", err)

			// If refresh fails but session still valid, try requesting new session
			// (re-auth may not be needed if TCP connection still valid)
			if conn != nil {
				log.Printf("[SESSION_RECOVERY] 🔄 Trying to request new session on existing TCP...")
				if err := c.requestSession(conn); err != nil {
					log.Printf("[SESSION_RECOVERY] ⚠️ Session request failed: %v, falling back to full re-auth", err)
					return c.TriggerReauth()
				}
				log.Printf("[SESSION_RECOVERY] ✅ New session obtained on existing TCP connection")
				return nil
			}

			// No valid connection, do full re-auth
			log.Printf("[SESSION_RECOVERY] 🔄 No TCP connection, doing full re-auth...")
			return c.TriggerReauth()
		}
		log.Printf("[SESSION_RECOVERY] ✅ Session refreshed successfully")
		return nil
	}

	// Session expired or doesn't exist - need new session
	log.Printf("[SESSION_RECOVERY] ⚠️ No valid session, need new session...")

	// Try to request session on existing TCP if available
	if conn != nil {
		log.Printf("[SESSION_RECOVERY] 🔄 Trying to request session on existing TCP...")
		if err := c.requestSession(conn); err != nil {
			log.Printf("[SESSION_RECOVERY] ⚠️ Session request failed (TCP may need re-auth): %v", err)
			return c.TriggerReauth()
		}
		log.Printf("[SESSION_RECOVERY] ✅ Session obtained!")
		return nil
	}

	// No connection, do full re-auth
	log.Printf("[SESSION_RECOVERY] 🔄 No TCP connection, doing full re-auth...")
	return c.TriggerReauth()
}

// GetSessionInfo returns current session information including token, local IP, and expiration
func (c *Client) GetSessionInfo() (token string, localIP string, expiresAt time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionToken, c.previousLocalIP, c.expiresAt
}

// reconnectTCP attempts to reconnect the TCP connection to the auth server
func (c *Client) reconnectTCP() error {
	targetHost, _ := c.resolveDialTarget()
	log.Printf("[RECONNECT] Attempting to reconnect TCP to %s:%d", targetHost, c.port)

	// Close existing connection if any
	c.mu.RLock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.RUnlock()

	// Create new connection (using local bind IP if configured)
	conn, err := c.dialWithLocalIP()
	if err != nil {
		return fmt.Errorf("reconnection failed: %w", err)
	}

	// Enable TCP keepalive to prevent disconnects
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Set keepalive
		tcpConn.SetKeepAlive(true)
		// Keepalive settings: send keepalive after 60s idle, every 30s, 3 probes
		tcpConn.SetKeepAlivePeriod(30 * time.Second) // Go 1.7+
		log.Printf("[RECONNECT] ✓ TCP keepalive enabled (30s interval)")
	}

	c.mu.Lock()
	c.conn = conn
	currentLocalIP := conn.LocalAddr().(*net.TCPAddr).IP.String()
	if c.previousLocalIP != "" && c.previousLocalIP != currentLocalIP {
		msg := fmt.Sprintf("TCP Local IP changed from %s to %s", c.previousLocalIP, currentLocalIP)
		log.Printf("[IP_CHANGE] 🔄 %s", msg)
		metrics.Global.AddLog("WARN", msg)
		c.lastIPChangeTime = time.Now() // Record IP change time to skip next refresh
	}
	c.previousLocalIP = currentLocalIP
	metrics.Global.SetIP(currentLocalIP)
	c.mu.Unlock()

	c.logDialEgress(conn)
	return nil
}

// ForceReconnect closes the current TCP connection and immediately attempts session recovery.
func (c *Client) ForceReconnect() {
	if !c.closeTCP() {
		return
	}

	c.mu.RLock()
	running := c.running
	c.mu.RUnlock()
	if !running {
		return
	}

	go func() {
		log.Println("[AUTH] ForceReconnect: recovering session immediately...")
		if err := c.TriggerSessionRecovery(); err != nil {
			log.Printf("[AUTH] ForceReconnect recovery failed: %v", err)
			c.handleRefreshFailure(&RefreshError{Message: err.Error()})
		}
	}()
}

func (c *Client) closeTCP() bool {
	c.tcpMu.Lock()
	defer c.tcpMu.Unlock()
	if c.conn == nil {
		return false
	}
	log.Println("[AUTH] Closing auth TCP connection...")
	c.conn.Close()
	c.conn = nil
	return true
}

// logStatus logs a message to stdout and also triggers the OnStatusMessage callback
func (c *Client) logStatus(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	if c.OnStatusMessage != nil {
		go c.OnStatusMessage("🔐 " + msg)
	}
}

// SetVPNManager sets the VPN manager for this auth client
// Call this BEFORE Start() if VPN is enabled
func (c *Client) SetVPNManager(mgr *vpn.Manager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vpnManager = mgr
}

// SetVPNRouterIP sets the router IP reachable inside WireGuard tunnel for TCP auth/refresh.
func (c *Client) SetVPNRouterIP(ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vpnRouterIP = strings.TrimSpace(ip)
}

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

// RequestVPNProvisioning sends 0xB0 to router and handles 0xB1 response
func (c *Client) RequestVPNProvisioning() error {
	c.mu.RLock()
	sessionToken := c.sessionToken
	conn := c.conn
	mgr := c.vpnManager
	c.mu.RUnlock()

	if mgr == nil {
		return fmt.Errorf("VPN manager not set")
	}
	if sessionToken == "" {
		return fmt.Errorf("no session token available")
	}
	if conn == nil {
		return fmt.Errorf("not connected to server")
	}

	priv, pub, isNew, err := mgr.LoadOrGenerateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to load/generate VPN keypair: %w", err)
	}

	c.mu.Lock()
	c.vpnPublicKey = pub
	c.mu.Unlock()

	log.Printf("[VPN] 📡 Requesting VPN provisioning from router...")
	if isNew {
		log.Printf("[VPN] Using newly generated keypair")
	}

	req := &VPNProvisionRequest{
		DroneUUID:    c.droneUUID,
		SessionToken: sessionToken,
		WGPublicKey:  pub,
	}

	packet := SerializeVPNProvisionRequest(req)
	c.tcpMu.Lock()
	defer c.tcpMu.Unlock()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})
	_, err = conn.Write(packet)
	if err != nil {
		return fmt.Errorf("failed to send VPN provision request: %w", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read VPN provision ack: %w", err)
	}

	log.Printf("[VPN_DEBUG] Received VPN Ack - Raw Packet Hex: %x", buf[:n])

	ack, err := ParseVPNProvisionAck(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse VPN provision ack: %w", err)
	}

	if ack.Result != ResultSuccess {
		return fmt.Errorf("vpn provisioning failed, error code: %d", ack.ErrorCode)
	}

	log.Printf("[VPN] ✅ VPN provisioned! IP=%s, Server=%s", ack.AssignedIP, ack.ServerEndpoint)

	err = mgr.SaveProvisionedConfig(priv, pub, ack.AssignedIP, ack.ServerPublicKey, ack.ServerEndpoint)
	if err != nil {
		return fmt.Errorf("failed to save VPN config: %w", err)
	}

	c.mu.Lock()
	close(c.vpnReadyCh)
	c.mu.Unlock()

	return nil
}
