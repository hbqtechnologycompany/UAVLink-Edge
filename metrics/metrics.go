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
	CurrentIP  string
	AuthStatus string
	LastAuth   time.Time
	StartTime  time.Time
	
	// Session info
	SessionExpiresAt time.Time
	RefreshInterval  time.Duration

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
	}
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
		"current_ip":        m.CurrentIP,
		"auth_status":       m.AuthStatus,
		"last_auth":         m.LastAuth,
		"uptime":            time.Since(m.StartTime).String(),
		"session_expires":   m.SessionExpiresAt,
		"refresh_interval":  m.RefreshInterval.Seconds(),
		"logs":              logsCopy,
	}
}
