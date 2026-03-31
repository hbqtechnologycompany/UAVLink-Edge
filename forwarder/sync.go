package forwarder

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"UAVLink-Edge/logger"
)

// SyncManager handles multi-drone synchronization using filesystem signals
type SyncManager struct {
	syncDir  string // Directory for sync files
	droneID  string // Current drone ID
	enabled  bool   // Whether sync is enabled
	readyCh  chan struct{}
	startedCh chan struct{}
}

// NewSyncManager creates a new sync manager
func NewSyncManager(syncDir string, droneID string) *SyncManager {
	return &SyncManager{
		syncDir:   syncDir,
		droneID:   droneID,
		enabled:   syncDir != "",
		readyCh:   make(chan struct{}),
		startedCh: make(chan struct{}),
	}
}

// IsEnabled returns whether sync mode is enabled
func (sm *SyncManager) IsEnabled() bool {
	return sm.enabled
}

// MarkReady writes a "ready" file to signal that this drone has connected and authenticated
func (sm *SyncManager) MarkReady() error {
	if !sm.enabled {
		return nil
	}

	// Create sync directory if it doesn't exist
	if err := os.MkdirAll(sm.syncDir, 0755); err != nil {
		return fmt.Errorf("failed to create sync dir: %w", err)
	}

	readyFile := filepath.Join(sm.syncDir, fmt.Sprintf("ready_%s", sm.droneID))
	f, err := os.Create(readyFile)
	if err != nil {
		return fmt.Errorf("failed to create ready file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(fmt.Sprintf("%d\n", time.Now().Unix())); err != nil {
		return fmt.Errorf("failed to write ready file: %w", err)
	}

	logger.Info("[SYNC] ✓ Marked ready: %s", readyFile)
	close(sm.readyCh)
	return nil
}

// WaitForStart waits for the "start" signal file to appear
// This blocks until all drones are ready and the coordinator creates the start file
func (sm *SyncManager) WaitForStart(timeout time.Duration) error {
	if !sm.enabled {
		return nil
	}

	logger.Info("[SYNC] ⏳ Waiting for start signal from coordinator...")

	startFile := filepath.Join(sm.syncDir, "start")
	checkInterval := 100 * time.Millisecond
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(startFile); err == nil {
			logger.Info("[SYNC] ✅ Start signal received! Beginning data forwarding...")
			close(sm.startedCh)
			return nil
		}

		time.Sleep(checkInterval)
	}

	return fmt.Errorf("timeout waiting for start signal after %v", timeout)
}

// WaitForReady blocks until MarkReady() has been called
func (sm *SyncManager) WaitForReady() {
	if !sm.enabled {
		return
	}
	<-sm.readyCh
}

// WaitForStarted blocks until WaitForStart() has completed
func (sm *SyncManager) WaitForStarted() {
	if !sm.enabled {
		return
	}
	<-sm.startedCh
}

// Cleanup removes sync files for this drone
func (sm *SyncManager) Cleanup() error {
	if !sm.enabled {
		return nil
	}

	readyFile := filepath.Join(sm.syncDir, fmt.Sprintf("ready_%s", sm.droneID))
	if err := os.Remove(readyFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove ready file: %w", err)
	}

	logger.Info("[SYNC] Cleaned up sync files")
	return nil
}
