package metrics

import (
	"sync"
	"time"
)

// Metrics holds the application state and statistics
type Metrics struct {
	mu sync.RWMutex

	// Packet statistics
	SentPackets      map[string]int64
	FailedPackets    map[string]int64
	FailedUnhealthy  map[string]int64 // Failed due to unhealthy state
	FailedSend       map[string]int64 // Failed due to send error

	// System status
	CurrentIP        string
	AuthStatus       string
	DisconnectReason string
	ServerReachable  bool
	LastAuth         time.Time
	LastRefreshOK    time.Time
	StartTime        time.Time
	
	// Session info
	SessionExpiresAt time.Time
	RefreshInterval  time.Duration

	// UDP MAVLink rates (forwarder.reportBandwidth, cập nhật mỗi 1s)
	UdpRawInPerSec    float64 // mọi frame MAVLink vào :14550, trước lọc SysID
	UdpAcceptedPerSec float64 // sau lọc SysID / dedup
	UdpMsgPerSec      float64 // uplink lên server (forward + generated)
	UdpBytesPerSec    float64 // byte/s uplink

	// Logs
	RecentLogs []LogEntry
}

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

var Global *Metrics

func init() {
	Global = New()
}

func New() *Metrics {
	return &Metrics{
		SentPackets:     make(map[string]int64),
		FailedPackets:   make(map[string]int64),
		FailedUnhealthy: make(map[string]int64),
		FailedSend:      make(map[string]int64),
		StartTime:       time.Now(),
		RecentLogs:      make([]LogEntry, 0, 100),
		AuthStatus:      "Initializing",
	}
}

func (m *Metrics) IncSent(msgType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentPackets[msgType]++
}

func (m *Metrics) IncFailed(msgType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FailedPackets[msgType]++
}

func (m *Metrics) IncFailedUnhealthy(msgType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FailedPackets[msgType]++
	m.FailedUnhealthy[msgType]++
}

func (m *Metrics) IncFailedSend(msgType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FailedPackets[msgType]++
	m.FailedSend[msgType]++
}

func (m *Metrics) SetIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CurrentIP = ip
}

func (m *Metrics) SetAuthStatus(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AuthStatus = status
	if status == "Authenticated" {
		m.LastAuth = time.Now()
		m.ServerReachable = true
		m.DisconnectReason = ""
	} else if status == "Disconnected" || status == "Reconnecting" {
		m.ServerReachable = false
	}
}

func (m *Metrics) SetDisconnectReason(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DisconnectReason = reason
	if reason != "" {
		m.ServerReachable = false
	}
}

func (m *Metrics) SetLastRefreshOK(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastRefreshOK = t
}

func (m *Metrics) AddLog(level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	entry := LogEntry{
		Time:    time.Now(),
		Level:   level,
		Message: msg,
	}
	
	// Keep last 100 logs
	if len(m.RecentLogs) >= 100 {
		m.RecentLogs = m.RecentLogs[1:]
	}
	m.RecentLogs = append(m.RecentLogs, entry)
}

func (m *Metrics) SetSessionInfo(expiresAt time.Time, interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SessionExpiresAt = expiresAt
	m.RefreshInterval = interval
}

func (m *Metrics) SetUdpRates(rawIn, accepted, msgOut, bytesOut float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UdpRawInPerSec = rawIn
	m.UdpAcceptedPerSec = accepted
	m.UdpMsgPerSec = msgOut
	m.UdpBytesPerSec = bytesOut
}

func (m *Metrics) GetSnapshot() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyInt64Map := func(src map[string]int64) map[string]int64 {
		dst := make(map[string]int64, len(src))
		for k, v := range src {
			dst[k] = v
		}
		return dst
	}

	logsCopy := make([]LogEntry, len(m.RecentLogs))
	copy(logsCopy, m.RecentLogs)

	return map[string]interface{}{
		"sent_packets":      copyInt64Map(m.SentPackets),
		"failed_packets":    copyInt64Map(m.FailedPackets),
		"failed_unhealthy":  copyInt64Map(m.FailedUnhealthy),
		"failed_send":       copyInt64Map(m.FailedSend),
		"current_ip":          m.CurrentIP,
		"auth_status":         m.AuthStatus,
		"server_reachable":    m.ServerReachable,
		"disconnect_reason":   m.DisconnectReason,
		"last_auth":           m.LastAuth,
		"last_refresh_ok":     m.LastRefreshOK,
		"uptime":            time.Since(m.StartTime).String(),
		"session_expires":      m.SessionExpiresAt,
		"refresh_interval":     m.RefreshInterval.Seconds(),
		"udp_raw_in_per_sec":   m.UdpRawInPerSec,
		"udp_accepted_per_sec": m.UdpAcceptedPerSec,
		"udp_msg_per_sec":      m.UdpMsgPerSec,
		"udp_bytes_per_sec":    m.UdpBytesPerSec,
		"logs":                 logsCopy,
	}
}
