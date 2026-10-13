package network

import (
	"encoding/json"
	"os"
	"time"
)

// Manager is the single Go entry for network state (orchestrator).
// Kernel routing is executed by root netmon daemon; Go reads the contract JSON only.
type Manager struct {
	statusPath string
}

func NewManager() *Manager {
	return &Manager{statusPath: runtimeNetworkStatusFile}
}

func (m *Manager) ReadStatus() (*Status, error) {
	data, err := os.ReadFile(m.statusPath)
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ReadStatus is a package-level helper for handlers that do not need a Manager instance.
func ReadStatus() (*Status, error) {
	return NewManager().ReadStatus()
}

func (m *Manager) CloudReady() bool {
	s, err := m.ReadStatus()
	if err != nil {
		return false
	}
	return s.CloudReady
}

func (m *Manager) EthPixhawkUp() bool {
	s, err := m.ReadStatus()
	if err != nil {
		return false
	}
	return s.Planes.EthPixhawk.LinkUp && s.Planes.EthPixhawk.IP != ""
}

func (m *Manager) WaitForCloudEgress(maxWait time.Duration) (iface string, ip string, ok bool) {
	return WaitForCloudEgress(maxWait)
}
