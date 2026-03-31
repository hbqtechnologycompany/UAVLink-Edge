package forwarder

import (
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"UAVLink-Edge/auth"
	"UAVLink-Edge/config"
	"UAVLink-Edge/logger"
	"UAVLink-Edge/mavlink_custom"
	"UAVLink-Edge/metrics"
	"UAVLink-Edge/web"
)

// getMessageTypeName extracts clean message type name from message
// e.g., *common.MessageHeartbeat -> HEARTBEAT

// createSenderEndpoint creates an endpoint
func createSenderEndpoint(targetAddr string) gomavlib.EndpointConf {
	logger.Info("[FORWARDER] creating default UDP client to %s", targetAddr)
	return gomavlib.EndpointUDPClient{Address: targetAddr}
}

func getMessageTypeName(msg interface{}) string {
	fullType := fmt.Sprintf("%T", msg)

	// Remove *common. prefix if exists
	if strings.HasPrefix(fullType, "*common.Message") {
		name := strings.TrimPrefix(fullType, "*common.Message")
		return name
	}
	// Remove common. prefix if exists
	if strings.HasPrefix(fullType, "common.Message") {
		name := strings.TrimPrefix(fullType, "common.Message")
		return name
	}
	// Remove Message prefix if exists
	if strings.HasPrefix(fullType, "Message") {
		name := strings.TrimPrefix(fullType, "Message")
		return name
	}
	return fullType
}

// getPixhawkSystemID returns the actual Pixhawk system ID from the web bridge
// This ensures we use the dynamic system ID detected from the Pixhawk heartbeat
// instead of hardcoding it. Falls back to 1 if not yet connected.
func getPixhawkSystemID() uint8 {
	return web.GetPixhawkSystemID()
}

// Forwarder handles receiving real MAVLink messages from Pixhawk and forwarding to server
type Forwarder struct {
	cfg          *config.Config
	listenerNode *gomavlib.Node // Listens for messages from Pixhawk and sends heartbeats
	senderNode   *gomavlib.Node // Sends messages to server

	bytesReceived uint64
	bytesSent     uint64
	byteStatsMu   sync.Mutex

	// Message counters for diagnosing in/out mismatch by MAVLink message ID.
	msgStatsMu    sync.Mutex
	msgInByID     map[uint32]uint64 // Pixhawk -> UAVLink-Edge (after SysID filtering)
	msgOutByID    map[uint32]uint64 // UAVLink-Edge -> Server (successful writes)
	msgSrvInByID  map[uint32]uint64 // Server -> UAVLink-Edge
	msgSrvOutByID map[uint32]uint64 // UAVLink-Edge -> Pixhawk (successful writes)

	// Packet counters for debugging loss between UDP :14540 input and server uplink.
	pktStatsMu sync.Mutex
	// Cumulative counters since process start.
	rawIn14540Total         uint64
	acceptedIn14540Total    uint64
	outServerForwardTotal   uint64
	outServerGeneratedTotal uint64
	dropSysID255Total       uint64
	dropDuplicateTotal      uint64
	dropNoPixhawkTotal      uint64
	dropUnhealthyTotal      uint64
	dropSendErrorTotal      uint64
	// Per-second counters (reset every report tick).
	rawIn14540Sec         uint64
	acceptedIn14540Sec    uint64
	outServerForwardSec   uint64
	outServerGeneratedSec uint64
	dropSysID255Sec       uint64
	dropDuplicateSec      uint64
	dropNoPixhawkSec      uint64
	dropUnhealthySec      uint64
	dropSendErrorSec      uint64

	authClient *auth.Client
	stopCh     chan struct{}
	previousIP string // Track previous local IP for change detection

	// Pixhawk connection tracking
	pixhawkConnected chan struct{} // Signal when first heartbeat from Pixhawk received
	pixhawkOnce      sync.Once     // Ensure pixhawkConnected is closed only once

	// Network health
	isHealthy    bool
	forceCheckCh chan struct{}
	mu           sync.RWMutex

	// Logging control
	lastHeartbeatLog time.Time
	lastGPSLog       time.Time
	lastAttitudeLog  time.Time

	// UDP heartbeat status
	udpHeartbeatSent chan struct{} // Signal when first UDP heartbeat sent

	// Deduplication - track seen messages by sequence number
	lastSeqNum map[uint8]uint8 // SystemID -> last sequence number
	seqMu      sync.RWMutex

	// Verbose mode for detailed message parsing
	verboseMode bool

	// Synchronization manager for multi-drone coordination
	syncManager *SyncManager

	// senderNode lifecycle management
	senderMu        sync.RWMutex  // Protects f.senderNode pointer access
	senderRebuildCh chan struct{} // Signals receiveFromServer to re-subscribe after rebuild
}

func formatTopMsgIDs(stats map[uint32]uint64, limit int) string {
	if len(stats) == 0 {
		return "-"
	}

	type kv struct {
		id    uint32
		count uint64
	}
	list := make([]kv, 0, len(stats))
	for id, count := range stats {
		if count == 0 {
			continue
		}
		list = append(list, kv{id: id, count: count})
	}
	if len(list) == 0 {
		return "-"
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].count == list[j].count {
			return list[i].id < list[j].id
		}
		return list[i].count > list[j].count
	})

	if limit > len(list) {
		limit = len(list)
	}

	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, fmt.Sprintf("%d:%d", list[i].id, list[i].count))
	}
	return strings.Join(parts, ", ")
}

func subtractPositive(a map[uint32]uint64, b map[uint32]uint64) map[uint32]uint64 {
	if len(a) == 0 {
		return map[uint32]uint64{}
	}
	out := make(map[uint32]uint64)
	for id, av := range a {
		bv := b[id]
		if av > bv {
			out[id] = av - bv
		}
	}
	return out
}

func (f *Forwarder) incMsgIn(msgID uint32) {
	f.msgStatsMu.Lock()
	f.msgInByID[msgID]++
	f.msgStatsMu.Unlock()
}

func (f *Forwarder) incMsgOut(msgID uint32) {
	f.msgStatsMu.Lock()
	f.msgOutByID[msgID]++
	f.msgStatsMu.Unlock()
}

func (f *Forwarder) incMsgSrvIn(msgID uint32) {
	f.msgStatsMu.Lock()
	f.msgSrvInByID[msgID]++
	f.msgStatsMu.Unlock()
}

func (f *Forwarder) incMsgSrvOut(msgID uint32) {
	f.msgStatsMu.Lock()
	f.msgSrvOutByID[msgID]++
	f.msgStatsMu.Unlock()
}

func (f *Forwarder) takeMsgStatsSnapshot() (map[uint32]uint64, map[uint32]uint64, map[uint32]uint64, map[uint32]uint64) {
	f.msgStatsMu.Lock()
	defer f.msgStatsMu.Unlock()

	in := f.msgInByID
	out := f.msgOutByID
	srvIn := f.msgSrvInByID
	srvOut := f.msgSrvOutByID

	f.msgInByID = make(map[uint32]uint64)
	f.msgOutByID = make(map[uint32]uint64)
	f.msgSrvInByID = make(map[uint32]uint64)
	f.msgSrvOutByID = make(map[uint32]uint64)

	return in, out, srvIn, srvOut
}

type pktStatsSnapshot struct {
	rawIn14540         uint64
	acceptedIn14540    uint64
	outServerForward   uint64
	outServerGenerated uint64
	dropSysID255       uint64
	dropDuplicate      uint64
	dropNoPixhawk      uint64
	dropUnhealthy      uint64
	dropSendError      uint64
}

type pktTotalsSnapshot struct {
	rawIn14540         uint64
	acceptedIn14540    uint64
	outServerForward   uint64
	outServerGenerated uint64
	dropSysID255       uint64
	dropDuplicate      uint64
	dropNoPixhawk      uint64
	dropUnhealthy      uint64
	dropSendError      uint64
}

func (f *Forwarder) incPktRawIn14540() {
	f.pktStatsMu.Lock()
	f.rawIn14540Total++
	f.rawIn14540Sec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktAcceptedIn14540() {
	f.pktStatsMu.Lock()
	f.acceptedIn14540Total++
	f.acceptedIn14540Sec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktOutServerForward() {
	f.pktStatsMu.Lock()
	f.outServerForwardTotal++
	f.outServerForwardSec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktOutServerGenerated() {
	f.pktStatsMu.Lock()
	f.outServerGeneratedTotal++
	f.outServerGeneratedSec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktDropSysID255() {
	f.pktStatsMu.Lock()
	f.dropSysID255Total++
	f.dropSysID255Sec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktDropDuplicate() {
	f.pktStatsMu.Lock()
	f.dropDuplicateTotal++
	f.dropDuplicateSec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktDropNoPixhawk() {
	f.pktStatsMu.Lock()
	f.dropNoPixhawkTotal++
	f.dropNoPixhawkSec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktDropUnhealthy() {
	f.pktStatsMu.Lock()
	f.dropUnhealthyTotal++
	f.dropUnhealthySec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) incPktDropSendError() {
	f.pktStatsMu.Lock()
	f.dropSendErrorTotal++
	f.dropSendErrorSec++
	f.pktStatsMu.Unlock()
}

func (f *Forwarder) takePktStatsSnapshot() (pktStatsSnapshot, pktTotalsSnapshot) {
	f.pktStatsMu.Lock()
	defer f.pktStatsMu.Unlock()

	sec := pktStatsSnapshot{
		rawIn14540:         f.rawIn14540Sec,
		acceptedIn14540:    f.acceptedIn14540Sec,
		outServerForward:   f.outServerForwardSec,
		outServerGenerated: f.outServerGeneratedSec,
		dropSysID255:       f.dropSysID255Sec,
		dropDuplicate:      f.dropDuplicateSec,
		dropNoPixhawk:      f.dropNoPixhawkSec,
		dropUnhealthy:      f.dropUnhealthySec,
		dropSendError:      f.dropSendErrorSec,
	}

	tot := pktTotalsSnapshot{
		rawIn14540:         f.rawIn14540Total,
		acceptedIn14540:    f.acceptedIn14540Total,
		outServerForward:   f.outServerForwardTotal,
		outServerGenerated: f.outServerGeneratedTotal,
		dropSysID255:       f.dropSysID255Total,
		dropDuplicate:      f.dropDuplicateTotal,
		dropNoPixhawk:      f.dropNoPixhawkTotal,
		dropUnhealthy:      f.dropUnhealthyTotal,
		dropSendError:      f.dropSendErrorTotal,
	}

	f.rawIn14540Sec = 0
	f.acceptedIn14540Sec = 0
	f.outServerForwardSec = 0
	f.outServerGeneratedSec = 0
	f.dropSysID255Sec = 0
	f.dropDuplicateSec = 0
	f.dropNoPixhawkSec = 0
	f.dropUnhealthySec = 0
	f.dropSendErrorSec = 0

	return sec, tot
}

// loggedConn wraps an underlying ReadWriteCloser and attributes actual
// bytes read/written to the Forwarder's counters.
type loggedConn struct {
	conn io.ReadWriteCloser
	f    *Forwarder
}

func (l *loggedConn) Read(b []byte) (int, error) {
	n, err := l.conn.Read(b)
	if n > 0 && l.f != nil {
		l.f.byteStatsMu.Lock()
		l.f.bytesReceived += uint64(n)
		l.f.byteStatsMu.Unlock()
	}
	return n, err
}

func (l *loggedConn) Write(b []byte) (int, error) {
	n, err := l.conn.Write(b)
	if n > 0 && l.f != nil {
		l.f.byteStatsMu.Lock()
		l.f.bytesSent += uint64(n)
		l.f.byteStatsMu.Unlock()
	}
	return n, err
}

func (l *loggedConn) Close() error {
	return l.conn.Close()
}

// getLocalIP returns the current local IP address used for outbound connections
func getLocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

// getEthernetIP automatically detects the IP address of an ethernet interface
// It searches for interfaces matching common ethernet naming patterns: eth*, end*, enp*, eno*
// Returns the IP address and broadcast address for the found interface
func getEthernetIP(cfg *config.Config) (localIP string, broadcastIP string, ifaceName string, err error) {
	// Auto-detect from interface
	ethPatterns := []string{"eth", "end", "enp", "eno"}

	// If specific interface is configured, only look for that
	if cfg.Ethernet.Interface != "" {
		ethPatterns = []string{cfg.Ethernet.Interface}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Check if interface name matches patterns
		isMatch := false
		for _, pattern := range ethPatterns {
			if cfg.Ethernet.Interface != "" {
				// Exact match if interface is specified
				if iface.Name == pattern {
					isMatch = true
					break
				}
			} else {
				// Prefix match for auto-detect
				if strings.HasPrefix(iface.Name, pattern) {
					isMatch = true
					break
				}
			}
		}

		if !isMatch {
			continue
		}

		ifaceName = iface.Name

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			var ipNet *net.IPNet

			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
				ipNet = v
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip IPv6 addresses
			if ip == nil || ip.To4() == nil {
				continue
			}

			localIP = ip.String()

			// Calculate broadcast address
			if ipNet != nil {
				broadcast := make(net.IP, len(ip.To4()))
				for i := range ip.To4() {
					broadcast[i] = ip.To4()[i] | ^ipNet.Mask[i]
				}
				broadcastIP = broadcast.String()
			} else {
				ipParts := strings.Split(localIP, ".")
				if len(ipParts) == 4 {
					broadcastIP = fmt.Sprintf("%s.%s.%s.255", ipParts[0], ipParts[1], ipParts[2])
				}
			}

			logger.Info("[NETWORK] Auto-detected ethernet interface %s: IP=%s, Broadcast=%s", iface.Name, localIP, broadcastIP)
			return localIP, "", ifaceName, nil // disable broadcast for wg
		}

		// Interface found but no IP - try to configure if auto_setup is enabled
	}
	return "", "", "", fmt.Errorf("no ethernet interface found (patterns: %v)", ethPatterns)
}

// setupInterfaceIP configures an IP address on an interface using ip command
func setupInterfaceIP(ifaceName, ipAddr, subnet string) error {
	if subnet == "" {
		subnet = "24"
	}
	cmd := exec.Command("sudo", "ip", "addr", "add", fmt.Sprintf("%s/%s", ipAddr, subnet), "dev", ifaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if IP already exists
		if strings.Contains(string(output), "File exists") {
			logger.Debug("[NETWORK] IP %s already exists on %s", ipAddr, ifaceName)
			return nil
		}
		return fmt.Errorf("failed to add IP: %s - %v", string(output), err)
	}
	return nil
}

// New creates a new forwarder instance
// NewListener creates only the listener node to receive from Pixhawk
// This is called BEFORE connecting to Pixhawk to capture its System ID
func NewListener(cfg *config.Config) (*gomavlib.Node, error) {
	// Get ethernet IP for UDP broadcast
	_, broadcastEthIP, ifaceName, ethErr := getEthernetIP(cfg)

	// Build endpoints list
	endpoints := []gomavlib.EndpointConf{
		gomavlib.EndpointUDPServer{Address: fmt.Sprintf("0.0.0.0:%d", cfg.Network.LocalListenPort)},
	}

	enableBroadcast := ethErr == nil && broadcastEthIP != ""
	if enableBroadcast {
		endpoints = append(endpoints, gomavlib.EndpointUDPBroadcast{
			BroadcastAddress: fmt.Sprintf("%s:%d", broadcastEthIP, cfg.Network.LocalListenPort),
		})
		logger.Info("[NETWORK] UDP Broadcast enabled on %s: Broadcast=%s:%d",
			ifaceName, broadcastEthIP, cfg.Network.LocalListenPort)
	} else {
		if ethErr != nil {
			logger.Warn("[NETWORK] UDP Broadcast disabled: %v", ethErr)
		} else {
			logger.Info("[NETWORK] UDP Broadcast disabled by configuration")
		}
		logger.Info("[NETWORK] Running with UDP Server only on 0.0.0.0:%d", cfg.Network.LocalListenPort)
	}

	// Create listener node to receive from Pixhawk
	listenerNode, err := gomavlib.NewNode(gomavlib.NodeConf{
		Endpoints:        endpoints,
		Dialect:          mavlink_custom.GetCombinedDialect(),
		OutVersion:       gomavlib.V2,
		OutSystemID:      255, // Ground station ID
		HeartbeatDisable: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create listener MAVLink node: %w", err)
	}
	logger.Info("[LISTENER] MAVLink listener created on port %d", cfg.Network.LocalListenPort)
	return listenerNode, nil
}

// New creates a new forwarder instance with BOTH listener and sender nodes
// IMPORTANT: Call this AFTER Pixhawk has connected, so OutSystemID matches actual drone SysID
// If listenerNode is provided, it will be reused (don't create a new one)
func New(cfg *config.Config, authClient *auth.Client, listenerNode *gomavlib.Node) (*Forwarder, error) {
	// Use provided auth client (already created and authenticated in main.go)
	// This ensures both web server and forwarder use the SAME session token
	if cfg.Auth.Enabled && authClient == nil {
		logger.Warn("Authentication enabled but no authClient provided - creating new one")
		authClient = auth.NewClient(
			cfg.Auth.Host,
			cfg.Auth.Port,
			cfg.Auth.UUID,
			cfg.Auth.SharedSecret,
			cfg.Auth.KeepaliveInterval,
		)
	} else if cfg.Auth.Enabled {
		logger.Info("Authentication enabled, using shared authClient for drone UUID %s",
			cfg.Auth.UUID)
	} else {
		logger.Warn("Authentication disabled - running in insecure mode")
	}

	var err error
	if listenerNode == nil {
		// Build endpoints list
		endpoints := []gomavlib.EndpointConf{
			gomavlib.EndpointUDPServer{Address: fmt.Sprintf("0.0.0.0:%d", cfg.Network.LocalListenPort)},
		}

		// UDP broadcast disabled for now (or could be enabled based on ethErr)
		logger.Info("[NETWORK] Running with UDP Server only on 0.0.0.0:%d", cfg.Network.LocalListenPort)

		// Create listener node to receive from Pixhawk
		listenerNode, err = gomavlib.NewNode(gomavlib.NodeConf{
			Endpoints:        endpoints,
			Dialect:          mavlink_custom.GetCombinedDialect(),
			OutVersion:       gomavlib.V2,
			OutSystemID:      255, // Ground station ID
			HeartbeatDisable: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create listener MAVLink node: %w", err)
		}
		logger.Info("MAVLink listener created on port %d", cfg.Network.LocalListenPort)
	} else {
		logger.Info("[FORWARDER] Reusing existing listener node")
	}

	// Get actual Pixhawk System ID from web bridge (was captured from heartbeat)
	pixhawkSysID := web.GetPixhawkSystemID()
	// Use default System ID if Pixhawk not available (e.g., when allow_missing_pixhawk=true)
	if pixhawkSysID == 0 {
		pixhawkSysID = 1 // Default valid System ID for missing Pixhawk
	}
	logger.Info("[FORWARDER] Using Pixhawk System ID: %d for OutSystemID", pixhawkSysID)

	// Create sender node to forward to server WITH correct system ID
	senderNode, err := gomavlib.NewNode(gomavlib.NodeConf{
		Endpoints: []gomavlib.EndpointConf{
			createSenderEndpoint(cfg.GetAddress()),
		},
		Dialect:          mavlink_custom.GetCombinedDialect(),
		OutVersion:       gomavlib.V2,
		OutSystemID:      pixhawkSysID, // Use actual Pixhawk sys_id instead of hardcoded 1
		HeartbeatDisable: true,
	})
	if err != nil {
		listenerNode.Close()
		return nil, fmt.Errorf("failed to create sender MAVLink node: %w", err)
	}
	logger.Info("MAVLink sender created, forwarding to %s", cfg.GetAddress())

	// localIP resolved earlier

	fwd := &Forwarder{
		cfg:              cfg,
		listenerNode:     listenerNode,
		senderNode:       senderNode,
		authClient:       authClient,
		stopCh:           make(chan struct{}),
		previousIP:       "",
		msgInByID:        make(map[uint32]uint64),
		msgOutByID:       make(map[uint32]uint64),
		msgSrvInByID:     make(map[uint32]uint64),
		msgSrvOutByID:    make(map[uint32]uint64),
		pixhawkConnected: make(chan struct{}),
		isHealthy:        true,
		forceCheckCh:     make(chan struct{}, 1),
		udpHeartbeatSent: make(chan struct{}, 1),
		lastSeqNum:       make(map[uint8]uint8),
		verboseMode:      cfg.Log.Verbose,
		syncManager:      nil, // Set via SetSyncManager() if needed
		senderRebuildCh:  make(chan struct{}, 1),
	}

	// Wire up network error callback
	if authClient != nil {
		authClient.OnNetworkError = func() {
			fwd.mu.Lock()
			if fwd.isHealthy {
				logger.Warn("[NETWORK] Network error detected via Auth Client - Marking unhealthy")
				fwd.isHealthy = false
				// Trigger immediate IP check
				select {
				case fwd.forceCheckCh <- struct{}{}:
				default:
				}
			}
			fwd.mu.Unlock()
		}
	}

	return fwd, nil
}

// GetListenerNode returns the listener MAVLink node for external use
func (f *Forwarder) GetListenerNode() *gomavlib.Node {
	return f.listenerNode
}

// WaitForPixhawkConnection waits for first heartbeat from Pixhawk within timeout
// Returns true if connected, false if timeout
func (f *Forwarder) WaitForPixhawkConnection(timeout time.Duration) bool {
	select {
	case <-f.pixhawkConnected:
		return true
	case <-time.After(timeout):
		return false
	}
}

// SetAuthClient sets the auth client after forwarder creation
func (f *Forwarder) SetAuthClient(authClient *auth.Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authClient = authClient
	if f.authClient != nil {
		// Wire up network error callback
		f.authClient.OnNetworkError = func() {
			f.mu.Lock()
			if f.isHealthy {
				logger.Warn("[NETWORK] Network error detected via Auth Client - Marking unhealthy")
				f.isHealthy = false
				// Trigger immediate IP check
				select {
				case f.forceCheckCh <- struct{}{}:
				default:
				}
			}
			f.mu.Unlock()
		}
	}
}

// SetSyncManager sets the sync manager for multi-drone coordination
func (f *Forwarder) SetSyncManager(syncManager *SyncManager) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncManager = syncManager
	if f.syncManager != nil && f.syncManager.IsEnabled() {
		logger.Info("[SYNC] Multi-drone synchronization enabled")
	}
}

// Start begins the forwarder
func (f *Forwarder) Start() error {
	logger.Info("Starting MAVLink forwarder...")

	// NOTE: Auth client is already started in main.go before calling fwd.Start()
	// Do NOT call authClient.Start() here to avoid duplicate TCP connections
	// The auth client is set via SetAuthClient() after forwarder creation

	// Start IP change monitor
	go f.monitorIPChange()

	// Start receiving and forwarding messages
	go f.receiveAndForward()
	go f.receiveFromServer()

	if f.cfg != nil && f.cfg.Auth.SessionHeartbeatFrequency > 0 {
		go f.sendMavlinkKeepAlive() // Start this BEFORE waiting for udpHeartbeatSent
	} else {
		logger.Info("[MAVLINK_KA] Disabled (auth.session_heartbeat_frequency=0)")
	}

	// Wait for first UDP heartbeat only when auth is already authenticated.
	if f.authClient != nil && f.authClient.IsAuthenticated() {
		logger.Info("Waiting for first UDP heartbeat to be sent for session verification...")
		select {
		case <-f.udpHeartbeatSent:
			logger.Info("✅ Session verified via first UDP heartbeat")
		case <-time.After(5 * time.Second):
			logger.Warn("⚠️ Timeout waiting for UDP heartbeat/session verify, starting anyway...")
		}
	}

	go f.reportBandwidth()

	logger.Info("Forwarder started - listening on port %d, forwarding to %s",
		f.cfg.Network.LocalListenPort, f.cfg.GetAddress())
	return nil

}

// Stop stops the forwarder
func (f *Forwarder) Stop() {
	logger.Info("Stopping forwarder...")
	close(f.stopCh)

	// Stop authentication client
	if f.authClient != nil {
		f.authClient.Stop()
	}

	f.listenerNode.Close()
	f.senderNode.Close()
	logger.Info("Forwarder stopped")
}

// receiveAndForward listens for incoming MAVLink messages from Pixhawk and forwards them to server
func (f *Forwarder) receiveAndForward() {
	eventCh := f.listenerNode.Events()
	messageCount := 0
	forwardedCount := 0

	// Wait for sync signal if enabled (for multi-drone synchronization)
	if f.syncManager != nil && f.syncManager.IsEnabled() {
		logger.Info("[SYNC] ⏳ Waiting for synchronization signal before forwarding messages...")
		if err := f.syncManager.WaitForStart(60 * time.Second); err != nil {
			logger.Error("[SYNC] Failed to wait for sync signal: %v - continuing anyway", err)
		} else {
			logger.Info("[SYNC] ✅ Synchronized! Beginning MAVLink message forwarding...")
		}
	}

	for {
		select {
		case <-f.stopCh:
			return
		case event := <-eventCh:
			now := time.Now()
			switch e := event.(type) {
			case *gomavlib.EventFrame:
				// Received a MAVLink message from Pixhawk
				msg := e.Message()
				f.incPktRawIn14540()
				msgID := msg.GetID()
				msgTypeName := getMessageTypeName(msg)
				sysID := e.SystemID()
				seqNum := e.Frame.GetSequenceNumber()

				// REJECT messages from System ID 255 (Simulator/GCS/Fake data)
				// We only accept messages from real flight controllers (SysID 1-250)
				if sysID == 255 {
					// Completely ignore simulator/GCS messages
					f.incPktDropSysID255()
					logger.Debug("[REJECT] Simulator/GCS message %s (SysID: %d) - NOT FORWARDING", msgTypeName, sysID)
					continue
				}

				messageCount++
				f.incMsgIn(msgID)

				// Deduplicate messages by checking sequence number
				f.seqMu.Lock()
				lastSeq, exists := f.lastSeqNum[sysID]
				if exists && lastSeq == seqNum {
					// Duplicate message, skip
					f.seqMu.Unlock()
					f.incPktDropDuplicate()
					logger.Debug("[DUP] Skipping duplicate %s (SysID: %d, Seq: %d)", msgTypeName, sysID, seqNum)
					continue
				}
				f.lastSeqNum[sysID] = seqNum
				f.seqMu.Unlock()
				f.incPktAcceptedIn14540()

				forwardedCount++

				// Debug: Log all received messages
				logger.Debug("[RX] %s (SysID: %d, Seq: %d)", msgTypeName, sysID, seqNum)

				if forwardedCount%10000 == 0 {
					logger.Info("[STATS] Forwarded %d messages (received %d, dedup rate: %.1f%%)",
						forwardedCount, messageCount, float64(messageCount-forwardedCount)/float64(messageCount)*100)
				}

				// Log specific message types at INFO level (reduced frequency)
				switch m := msg.(type) {
				case *common.MessageHeartbeat:
					// Signal on first heartbeat from REAL Pixhawk ONLY (reject SysID 255 = Simulator)
					if sysID != 255 {
						f.pixhawkOnce.Do(func() {
							close(f.pixhawkConnected)
							logger.Info("[PIXHAWK_CONNECTED] ✅ First heartbeat received from Pixhawk (SysID: %d)", sysID)
						})
					}

					// Only log and notify web UI for REAL Pixhawk (not SysID 255)
					if sysID != 255 {
						if now.Sub(f.lastHeartbeatLog) > 30*time.Second {
							logger.Info("[PIXHAWK] Heartbeat: Type=%d, Mode=%d, Status=%d", m.Type, m.BaseMode, m.SystemStatus)
							f.lastHeartbeatLog = now
						}
						// Notify web server of connected Pixhawk - this captures the actual system ID
						web.HandleHeartbeat(sysID)
						actualSysID := web.GetPixhawkSystemID()
						logger.Debug("[SYSID] Detected Pixhawk System ID: %d (using for MAVLink operations)", actualSysID)
					}
				case *common.MessageGpsRawInt:
					if now.Sub(f.lastGPSLog) > 30*time.Second {
						logger.Info("[PIXHAWK] GPS: Fix=%d, Lat=%.6f, Lon=%.6f, Sats=%d",
							m.FixType, float64(m.Lat)/1e7, float64(m.Lon)/1e7, m.SatellitesVisible)
						f.lastGPSLog = now
					}
				case *common.MessageSysStatus:
					if now.Sub(f.lastAttitudeLog) > 30*time.Second {
						logger.Info("[PIXHAWK] Status: Voltage=%.2fV, Battery=%d%%",
							float64(m.VoltageBattery)/1000, m.BatteryRemaining)
						f.lastAttitudeLog = now
					}
				case *common.MessageParamValue:
					// Forward to web server for parameter caching
					web.HandleParamValue(m)
					logger.Debug("[PARAM] %s = %v (%d/%d)", m.ParamId, m.ParamValue, m.ParamIndex, m.ParamCount)
				}

				// Forward message to server
				f.mu.RLock()
				healthy := f.isHealthy
				f.mu.RUnlock()

				// Check if Pixhawk is actually connected (not just timeout allowed)
				// Only forward messages when we have a REAL Pixhawk connection
				pixhawkConnected := false
				select {
				case <-f.pixhawkConnected:
					pixhawkConnected = true
				default:
					// Not connected yet
				}

				if !pixhawkConnected {
					// No real Pixhawk connected - don't forward anything
					f.incPktDropNoPixhawk()
					logger.Debug("[SKIP] No Pixhawk connected - not forwarding %s", msgTypeName)
					continue
				}

				// No VPN mode, forward directly
				if !healthy {
					metrics.Global.IncFailedUnhealthy(msgTypeName)
					f.incPktDropUnhealthy()
				} else {
					// Forward the raw frame directly to preserve original message
					f.senderMu.RLock()
					senderNode := f.senderNode
					f.senderMu.RUnlock()
					if err := senderNode.WriteFrameAll(e.Frame); err != nil {
						logger.Error("[FORWARD] Failed to forward frame %s: %v", msgTypeName, err)
						metrics.Global.IncFailedSend(msgTypeName)
						f.incPktDropSendError()
					} else {
						logger.Debug("[FORWARD] %s #%d", msgTypeName, forwardedCount)
						metrics.Global.IncSent(msgTypeName)
						f.incMsgOut(msgID)
						f.incPktOutServerForward()
					}
				}

			case *gomavlib.EventChannelOpen:
				logger.Info("[LISTENER] Channel opened: %v", e.Channel)
			case *gomavlib.EventChannelClose:
				logger.Warn("[LISTENER] Channel closed: %v", e.Channel)
			case *gomavlib.EventParseError:
				logger.Debug("[LISTENER] Parse error: %v", e.Error)
			}
		}
	}
}

// parseMessageVerbose provides detailed field-by-field parsing of MAVLink messages from server
func (f *Forwarder) parseMessageVerbose(msg interface{}, sysID uint8) {
	switch m := msg.(type) {
	case *common.MessageHeartbeat:
		logger.Info("[VERBOSE] HEARTBEAT from server (SysID: %d) - Type=%d, Autopilot=%d, BaseMode=%d, CustomMode=%d, SystemStatus=%d",
			sysID, m.Type, m.Autopilot, m.BaseMode, m.CustomMode, m.SystemStatus)

	case *common.MessageSysStatus:
		logger.Info("[VERBOSE] SYS_STATUS from server - Load=%d%%, Battery=%dmV (%d%%), CommDrop=%d, CommErrors=%d, ErrorsCount1=%d",
			m.Load/10, m.VoltageBattery, m.BatteryRemaining,
			m.DropRateComm, m.ErrorsComm, m.ErrorsCount1)

	case *common.MessageGpsRawInt:
		logger.Info("[VERBOSE] GPS_RAW_INT from server - Fix=%d, Lat=%.7f, Lon=%.7f, Alt=%d cm, Sats=%d, HDOP=%d, VDOP=%d, Vel=%d cm/s, Cog=%d°",
			m.FixType, float64(m.Lat)/1e7, float64(m.Lon)/1e7, m.Alt, m.SatellitesVisible,
			m.Eph, m.Epv, m.Vel, m.Cog)

	case *common.MessageAttitude:
		logger.Info("[VERBOSE] ATTITUDE from server - Roll=%.2f rad, Pitch=%.2f rad, Yaw=%.2f rad, RollSpeed=%.2f rad/s, PitchSpeed=%.2f rad/s, YawSpeed=%.2f rad/s, TimeBootMs=%d ms",
			m.Roll, m.Pitch, m.Yaw, m.Rollspeed, m.Pitchspeed, m.Yawspeed, m.TimeBootMs)

	case *common.MessageLocalPositionNed:
		logger.Info("[VERBOSE] LOCAL_POSITION_NED from server - X=%.2f m, Y=%.2f m, Z=%.2f m, Vx=%.2f m/s, Vy=%.2f m/s, Vz=%.2f m/s, TimeBootMs=%d ms",
			m.X, m.Y, m.Z, m.Vx, m.Vy, m.Vz, m.TimeBootMs)

	case *common.MessageGlobalPositionInt:
		logger.Info("[VERBOSE] GLOBAL_POSITION_INT from server - Lat=%.7f°, Lon=%.7f°, Alt=%d mm, RelAlt=%d mm, Vx=%d cm/s, Vy=%d cm/s, Vz=%d cm/s, Hdg=%d cdeg, TimeBootMs=%d ms",
			float64(m.Lat)/1e7, float64(m.Lon)/1e7, m.Alt, m.RelativeAlt, m.Vx, m.Vy, m.Vz, m.Hdg, m.TimeBootMs)

	case *common.MessageVfrHud:
		logger.Info("[VERBOSE] VFR_HUD from server - Airspeed=%.2f m/s, Groundspeed=%.2f m/s, Heading=%d°, Throttle=%d%%, Altitude=%.2f m, ClimbRate=%.2f m/s",
			m.Airspeed, m.Groundspeed, m.Heading, m.Throttle, m.Alt, m.Climb)

	case *common.MessageBatteryStatus:
		logger.Info("[VERBOSE] BATTERY_STATUS from server - BatType=%d, ID=%d, BatFunction=%d, Temperature=%d°C, Voltage=%d mV, CurrentBattery=%d mA, ChargeState=%d, Cells=[%d, %d, %d, %d, %d, %d] mV",
			m.Type, m.Id, m.BatteryFunction, m.Temperature, m.Voltages[0], m.CurrentBattery, m.ChargeState,
			m.Voltages[0], m.Voltages[1], m.Voltages[2], m.Voltages[3], m.Voltages[4], m.Voltages[5])

	case *common.MessageServoOutputRaw:
		logger.Info("[VERBOSE] SERVO_OUTPUT_RAW from server - ServoPort=%d, TimeUsec=%d us, Outputs=[%d, %d, %d, %d, %d, %d, %d, %d]",
			m.Port, m.TimeUsec, m.Servo1Raw, m.Servo2Raw, m.Servo3Raw, m.Servo4Raw, m.Servo5Raw, m.Servo6Raw, m.Servo7Raw, m.Servo8Raw)

	case *common.MessageMissionItem:
		logger.Info("[VERBOSE] MISSION_ITEM from server - Seq=%d, Frame=%d, Command=%d, Current=%d, Autocontinue=%d, Params=[%.2f, %.2f, %.2f, %.2f], X=%.7f, Y=%.7f, Z=%.2f",
			m.Seq, m.Frame, m.Command, m.Current, m.Autocontinue,
			m.Param1, m.Param2, m.Param3, m.Param4, m.X, m.Y, m.Z)

	case *common.MessageParamValue:
		logger.Info("[VERBOSE] PARAM_VALUE from server - ParamId=%s, ParamValue=%.2f, ParamType=%d, ParamCount=%d, ParamIndex=%d",
			m.ParamId, m.ParamValue, m.ParamType, m.ParamCount, m.ParamIndex)

	case *common.MessageCommandAck:
		logger.Info("[VERBOSE] COMMAND_ACK from server - Command=%d, Result=%d, Progress=%d, ResultParam2=%d",
			m.Command, m.Result, m.Progress, m.ResultParam2)

	case *common.MessageSetMode:
		logger.Info("[VERBOSE] SET_MODE from server - TargetSystem=%d, BaseMode=%d, CustomMode=%d",
			m.TargetSystem, m.BaseMode, m.CustomMode)

	case *common.MessageManualControl:
		logger.Info("[VERBOSE] MANUAL_CONTROL from server - Target=%d, Pitch=%d, Roll=%d, Throttle=%d, Yaw=%d, Buttons=%d",
			m.Target, m.X, m.Y, m.Z, m.R, m.Buttons)

	default:
		// Generic message - just log the type name
		msgTypeName := getMessageTypeName(msg)
		logger.Debug("[VERBOSE] %s from server (SysID: %d) - message type not specifically parsed",
			msgTypeName, sysID)
	}
}

// receiveFromServer listens for incoming MAVLink messages from server and logs them
func (f *Forwarder) receiveFromServer() {
	// NOTE: senderNode is rebuilt by monitorIPChange whenever the network changes.
	// We must re-subscribe to the new node's event channel after each rebuild;
	// otherwise we'd spin on the closed channel of the old (now-dead) node,
	// burning 100% CPU and never processing events from the new node.
	f.senderMu.RLock()
	eventCh := f.senderNode.Events()
	f.senderMu.RUnlock()

	receivedCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-f.stopCh:
			return
		case <-f.senderRebuildCh:
			// senderNode was replaced — grab the new event channel and continue
			f.senderMu.RLock()
			eventCh = f.senderNode.Events()
			f.senderMu.RUnlock()
			logger.Info("[SERVER_RX] senderNode rebuilt — re-subscribed to new event channel")
			continue
		case event := <-eventCh:
			switch e := event.(type) {
			case *gomavlib.EventFrame:
				// Received a MAVLink message from server
				msg := e.Message()
				msgID := msg.GetID()
				msgTypeName := getMessageTypeName(msg)
				sysID := e.SystemID()

				pixhawkSysID := web.GetPixhawkSystemID()

				// LOOP PREVENTION & DEDUPLICATION:
				// 1. Drop messages that originated from our own drone (SysID == pixhawkSysID)
				if pixhawkSysID != 0 && sysID == pixhawkSysID {
					continue
				}

				// 2. Drop messages from GCS/Server (SysID 255) if it's an exact echo of what we just sent
				// Only forward commands going *to* the Pixhawk (like COMMAND_LONG, RC_CHANNELS_OVERRIDE, etc)
				// To do this strictly, we can check if the server is just reflecting telemetry.
				switch msg.(type) {
				case *common.MessageHeartbeat, *common.MessageGpsRawInt, *common.MessageSysStatus, *common.MessageAttitude, *common.MessageGlobalPositionInt, *common.MessageVfrHud, *common.MessageLocalPositionNed:
					// These are strictly Telemetry messages. QGC/MAVProxy often echo these back.
					// We NEVER want to forward telemetry back to the Pixhawk.
					continue
				}

				receivedCount++
				f.incMsgSrvIn(msgID)

				// Log statistics every 1000 messages or every 10 seconds
				now := time.Now()
				if receivedCount%1000 == 0 || now.Sub(lastLogTime) > 10*time.Second {
					logger.Info("[SERVER->PIXHAWK] Received %d messages from server", receivedCount)
					lastLogTime = now
				}

				// Verbose mode: parse and log detailed message fields
				if f.verboseMode {
					f.parseMessageVerbose(msg, sysID)
				}

				logger.Debug("[SERVER->PIXHAWK] %s (SysID: %d)", msgTypeName, sysID)

				// Forward message to Pixhawk
				if err := f.listenerNode.WriteMessageAll(msg); err != nil {
					logger.Error("[SERVER->PIXHAWK] Failed to forward %s: %v", msgTypeName, err)
				} else {
					logger.Debug("[SERVER->PIXHAWK] Forwarded %s", msgTypeName)
					f.incMsgSrvOut(msgID)
				}

			case *gomavlib.EventChannelOpen:
				logger.Info("[SENDER] Channel opened: %v", e.Channel)
			case *gomavlib.EventChannelClose:
				logger.Warn("[SENDER] Channel closed: %v", e.Channel)
			case *gomavlib.EventParseError:
				logger.Debug("[SENDER] Parse error: %v", e.Error)
			}
		}
	}
}
func (f *Forwarder) sendHeartbeat() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			msg := &common.MessageHeartbeat{
				Type:         6, // MAV_TYPE_GCS
				Autopilot:    0, // MAV_AUTOPILOT_INVALID
				BaseMode:     0, // MAV_MODE_FLAG enum
				CustomMode:   0,
				SystemStatus: 4, // MAV_STATE_ACTIVE
			}
			if err := f.listenerNode.WriteMessageAll(msg); err != nil {
				logger.Error("[HEARTBEAT] Failed to send GCS heartbeat: %v", err)
			} else {
				logger.Debug("[HEARTBEAT] Sent GCS heartbeat")
			}
		}
	}
}

// sendMavlinkKeepAlive sends MAVLINK_KEEPALIVE messages with session token to sync IP:Port
// This ensures the UDP source port matches between keepalive and MAVLink data
func (f *Forwarder) sendMavlinkKeepAlive() {
	if f.authClient == nil {
		logger.Warn("[MAVLINK_KA] No auth client, skipping MAVLink session keepalive")
		return
	}

	// Get frequency from config (Hz)
	frequency := f.cfg.Auth.SessionHeartbeatFrequency
	if frequency <= 0 {
		frequency = 1.0 // Default 1 Hz
	}
	interval := time.Duration(1.0 / frequency * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Compatibility mode for server-side SESSION_HB token parser.
	// Modes:
	//   shifted (default): token is shifted by 1 byte; last byte moved into PixhawkConnected
	//   normal: send raw token directly in Token[32]
	hbMode := strings.ToLower(strings.TrimSpace(os.Getenv("UAVLINK_EDGE_SESSION_HB_MODE")))
	if hbMode == "" {
		hbMode = "shifted"
	}
	if hbMode != "normal" && hbMode != "shifted" {
		logger.Warn("[UAVLINK_EDGE_SESSION_HB] Invalid UAVLINK_EDGE_SESSION_HB_MODE=%s, fallback to shifted", hbMode)
		hbMode = "shifted"
	}
	logger.Info("[UAVLINK_EDGE_SESSION_HB] SESSION_HB mode: %s", hbMode)

	firstSent := false
	sequence := uint16(0)

	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			tokenHex, _, expiresAt := f.authClient.GetSessionInfo()
			if tokenHex == "" {
				continue // No session yet
			}

			if !firstSent {
				logger.Info("[MAVLINK_KA] Starting MAVLink session keepalive at %.1f Hz (Session ready)", frequency)
			}

			// Auth session token is a 64-char hex string representing 32 raw bytes.
			// Server validates SESSION_HEARTBEAT by hex-encoding the 32-byte payload
			// and matching it directly against active session tokens.
			decodedToken, err := hex.DecodeString(tokenHex)
			if err != nil || len(decodedToken) != 32 {
				logger.Error("[MAVLINK_KA] Invalid session token format for heartbeat (len=%d, err=%v)", len(decodedToken), err)
				continue
			}

			var tokenBinary [32]byte
			copy(tokenBinary[:], decodedToken)

			// Create custom SESSION_HEARTBEAT message
			// Check if Pixhawk is connected
			pixhawkConnected := uint8(0)
			select {
			case <-f.pixhawkConnected:
				pixhawkConnected = 1 // Connected
			default:
				pixhawkConnected = 0 // Not connected
			}

			if hbMode == "shifted" {
				// Some server builds extract token from byte offset 7 (not 6).
				// Shift token by one byte so extracted value matches session token.
				var shifted [32]byte
				copy(shifted[1:], decodedToken[:31])
				tokenBinary = shifted
				pixhawkConnected = decodedToken[31]
			}

			msg := &mavlink_custom.MessageMavlinkKeepAlive{
				Token:            tokenBinary,
				ExpiresAt:        uint32(expiresAt.Unix()),
				Sequence:         sequence,
				PixhawkConnected: pixhawkConnected,
			}
			sequence++

			// Send via senderNode (to server) - this ensures same source port as MAVLink data
			f.senderMu.RLock()
			senderNode := f.senderNode
			f.senderMu.RUnlock()
			if err := senderNode.WriteMessageAll(msg); err != nil {
				logger.Error("[MAVLINK_KA] Failed to send session keepalive: %v", err)
			} else {
				f.incMsgOut(msg.GetID())
				f.incPktOutServerGenerated()
				if !firstSent {
					logger.Info("[MAVLINK_KA] ✓ First MAVLink session keepalive sent (ID 42999)")
					firstSent = true
					// Signal that keepalive is ready
					select {
					case f.udpHeartbeatSent <- struct{}{}:
					default:
					}
				}
				if (sequence-1)%10 == 0 {
					logger.Info("[MAVLINK_KA] Sent session keepalive #%d-#%d (ID 42999)", sequence-10, sequence-1)
				}
			}
		}
	}
}
func (f *Forwarder) getCurrentIP() string {
	// Use the current outbound local IP so health can recover.
	ip, err := getLocalIP()
	if err != nil {
		logger.Debug("[IP_MONITOR] Failed to get local IP: %v", err)
		return ""
	}
	return ip
}
func (f *Forwarder) getEndpointConf() gomavlib.EndpointConf {
	if f.cfg == nil {
		return gomavlib.EndpointUDPClient{Address: "127.0.0.1:14550"}
	}
	return createSenderEndpoint(f.cfg.GetAddress())
}

func (f *Forwarder) monitorIPChange() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// networkWasDown tracks whether IP was empty (network down) in the previous cycle.
	// This lets us detect the case where network recovers WITH THE SAME IP —
	// in that case previousIP == currentIP but the UDP conn may be stale.
	networkWasDown := false

	rebuildSender := func(reason string) {
		// Capture old node under lock, close outside lock to avoid blocking writers
		f.senderMu.RLock()
		oldNode := f.senderNode
		f.senderMu.RUnlock()
		oldNode.Close()

		pixhawkSysID := web.GetPixhawkSystemID()
		if pixhawkSysID == 0 {
			pixhawkSysID = 1
		}
		node, err := gomavlib.NewNode(gomavlib.NodeConf{
			Endpoints: []gomavlib.EndpointConf{
				f.getEndpointConf(),
			},
			Dialect:          mavlink_custom.GetCombinedDialect(),
			OutVersion:       gomavlib.V2,
			OutSystemID:      pixhawkSysID,
			HeartbeatDisable: true,
		})
		if err != nil {
			logger.Error("[IP_MONITOR] Error recreating sender node (%s): %v", reason, err)
			return
		}

		f.senderMu.Lock()
		f.senderNode = node
		f.senderMu.Unlock()

		logger.Info("[IP_MONITOR] Sender node rebuilt (%s)", reason)

		// Signal receiveFromServer to re-subscribe to the new node's event channel
		select {
		case f.senderRebuildCh <- struct{}{}:
		default:
		}

		if f.authClient != nil {
			f.authClient.ForceReconnect()
		}

		f.mu.Lock()
		f.isHealthy = true
		f.mu.Unlock()
	}

	checkIP := func() {
		currentIP := f.getCurrentIP()

		// ── Network is DOWN ──────────────────────────────────────────────
		if currentIP == "" {
			if !networkWasDown {
				logger.Warn("[IP_MONITOR] ⚠️  Network lost (no valid IP) - marking unhealthy")
				metrics.Global.AddLog("WARN", "Network lost - no valid IP")
				f.mu.Lock()
				f.isHealthy = false
				f.mu.Unlock()
				networkWasDown = true
			} else {
				logger.Debug("[IP_MONITOR] Network still down, waiting...")
			}
			return
		}

		// ── Network just CAME BACK (was down before) ─────────────────────
		if networkWasDown {
			logger.Info("[IP_MONITOR] ✅ Network restored! IP=%s (prev=%s)", currentIP, f.previousIP)
			metrics.Global.AddLog("INFO", fmt.Sprintf("Network restored: IP=%s", currentIP))
			metrics.Global.SetIP(currentIP)
			networkWasDown = false

			// Always rebuild even if same IP — UDP socket may have been stale
			f.previousIP = currentIP
			rebuildSender("network-restored")
			return
		}

		// ── First cycle: record initial IP ───────────────────────────────
		if f.previousIP == "" {
			f.previousIP = currentIP
			metrics.Global.SetIP(currentIP)
			logger.Info("[IP_MONITOR] Initial IP: %s — Binding sender node", currentIP)
			metrics.Global.AddLog("INFO", fmt.Sprintf("Initial IP: %s (Rebinding)", currentIP))
			rebuildSender("initial-bind")
			return
		}

		// ── IP changed (e.g. 4G roaming, WiFi ↔ 4G switch) ──────────────
		if f.previousIP != currentIP {
			logger.Warn("[IP_MONITOR] IP changed: %s → %s — Reconnecting", f.previousIP, currentIP)
			metrics.Global.AddLog("WARN", fmt.Sprintf("IP changed: %s → %s", f.previousIP, currentIP))
			metrics.Global.SetIP(currentIP)
			f.previousIP = currentIP
			rebuildSender("ip-changed")
		} else {
			f.mu.RLock()
			healthy := f.isHealthy
			f.mu.RUnlock()
			if !healthy {
				logger.Warn("[IP_MONITOR] Marked unhealthy but IP hasn't changed. Forcing rebuild to recover.")
				rebuildSender("unhealthy-recovery")
			}
		}
	}

	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			checkIP()
		case <-f.forceCheckCh:
			checkIP()
		}
	}
}

func (f *Forwarder) reportBandwidth() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			f.byteStatsMu.Lock()
			recv := f.bytesReceived
			sent := f.bytesSent
			f.bytesReceived = 0
			f.bytesSent = 0
			f.byteStatsMu.Unlock()

			msgIn, msgOut, msgSrvIn, msgSrvOut := f.takeMsgStatsSnapshot()
			pktSec, pktTot := f.takePktStatsSnapshot()
			extraOut := subtractPositive(msgOut, msgIn)
			extraToPixhawk := subtractPositive(msgSrvOut, msgSrvIn)

			acceptedDropTotal := uint64(0)
			if pktTot.acceptedIn14540 > pktTot.outServerForward {
				acceptedDropTotal = pktTot.acceptedIn14540 - pktTot.outServerForward
			}
			acceptedDropRate := 0.0
			if pktTot.acceptedIn14540 > 0 {
				acceptedDropRate = float64(acceptedDropTotal) * 100.0 / float64(pktTot.acceptedIn14540)
			}

			if recv > 0 || sent > 0 {
				logger.Info("[BANDWIDTH] In: %d B/s | Out: %d B/s", recv, sent)
			}

			if len(msgIn) > 0 || len(msgOut) > 0 {
				logger.Info("[MSGID][UPLINK] in14540/s top=%s | outServer/s top=%s | extraOut/s=%s",
					formatTopMsgIDs(msgIn, 6),
					formatTopMsgIDs(msgOut, 6),
					formatTopMsgIDs(extraOut, 6),
				)
			}

			logger.Info("[PKT_SEC][UPLINK] rawIn14540/s=%d accepted/s=%d outServerForward/s=%d outServerGenerated/s=%d drop/s={sys255:%d dup:%d noPixhawk:%d unhealthy:%d sendErr:%d}",
				pktSec.rawIn14540,
				pktSec.acceptedIn14540,
				pktSec.outServerForward,
				pktSec.outServerGenerated,
				pktSec.dropSysID255,
				pktSec.dropDuplicate,
				pktSec.dropNoPixhawk,
				pktSec.dropUnhealthy,
				pktSec.dropSendError,
			)

			logger.Info("[PKT_TOTAL][UPLINK] rawIn14540=%d accepted=%d outServerForward=%d outServerGenerated=%d acceptedDrop=%d(%.2f%%) breakdown={sys255:%d dup:%d noPixhawk:%d unhealthy:%d sendErr:%d}",
				pktTot.rawIn14540,
				pktTot.acceptedIn14540,
				pktTot.outServerForward,
				pktTot.outServerGenerated,
				acceptedDropTotal,
				acceptedDropRate,
				pktTot.dropSysID255,
				pktTot.dropDuplicate,
				pktTot.dropNoPixhawk,
				pktTot.dropUnhealthy,
				pktTot.dropSendError,
			)

			if len(msgSrvIn) > 0 || len(msgSrvOut) > 0 {
				logger.Info("[MSGID][DOWNLINK] fromServer/s top=%s | toPixhawk/s top=%s | extraToPixhawk/s=%s",
					formatTopMsgIDs(msgSrvIn, 6),
					formatTopMsgIDs(msgSrvOut, 6),
					formatTopMsgIDs(extraToPixhawk, 6),
				)
			}
		}
	}
}
