package camera

import (
	"sync"
	"time"

	"DroneBridge/config"
	"DroneBridge/logger"
)

const (
	streamWatchdogInterval = 12 * time.Second
	streamStatsStaleAfter  = 18 * time.Second
	streamRestartCooldown  = 20 * time.Second
	streamLowFPSThreshold  = 16.0
	streamLowFPSWindows    = 3
)

var (
	watchdogMu           sync.Mutex
	watchdogCfg          *config.Config
	watchdogStop         chan struct{}
	watchdogRunning      bool
	lastRestartAt        = map[int]time.Time{}
	lowFPSStreak         = map[int]int{}
	suppressRestartUntil time.Time
)

// StartStreamWatchdog monitors publisher health and restarts dead or stalled streams.
func StartStreamWatchdog(cfg *config.Config) {
	if cfg == nil || !cfg.Camera.Enabled {
		return
	}
	watchdogMu.Lock()
	defer watchdogMu.Unlock()
	watchdogCfg = cfg
	if watchdogRunning {
		return
	}
	watchdogStop = make(chan struct{})
	watchdogRunning = true
	go streamWatchdogLoop()
	logger.Info("[CAMERA] Stream watchdog started (interval %s)", streamWatchdogInterval)
}

// StopStreamWatchdog stops the background health monitor.
func StopStreamWatchdog() {
	watchdogMu.Lock()
	defer watchdogMu.Unlock()
	if !watchdogRunning {
		return
	}
	close(watchdogStop)
	watchdogRunning = false
}

func streamWatchdogLoop() {
	ticker := time.NewTicker(streamWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-watchdogStop:
			return
		case <-ticker.C:
			runStreamHealthCheck()
		}
	}
}

func runStreamHealthCheck() {
	watchdogMu.Lock()
	cfg := watchdogCfg
	watchdogMu.Unlock()
	if cfg == nil || !cfg.Camera.Enabled {
		return
	}

	pids := ListStreamerPIDs()
	pidSet := map[int]bool{}
	for _, p := range pids {
		pidSet[p] = true
	}
	_ = pidSet

	for _, stream := range enabledStreamsSorted(cfg) {
		if cfg.Camera.SkipMissingSensors {
			filtered := filterStreamsWithSensors([]config.CameraStreamConfig{stream})
			if len(filtered) == 0 {
				continue
			}
		}
		camID := stream.CameraID
		if !isStreamerRunningForCamera(camID) {
			logger.Warn("[CAMERA] Watchdog: cam%d publisher not running — restarting", camID)
			restartOneStream(cfg, camID)
			continue
		}
		st := ReadStreamStats(camID, 0)
		if st == nil {
			if time.Since(lastRestartAt[camID]) > streamRestartCooldown {
				logger.Warn("[CAMERA] Watchdog: cam%d stats missing — restarting", camID)
				restartOneStream(cfg, camID)
			}
			continue
		}
		if st.AgeSec > streamStatsStaleAfter.Seconds() {
			logger.Warn("[CAMERA] Watchdog: cam%d stats stale (%.0fs) — restarting", camID, st.AgeSec)
			restartOneStream(cfg, camID)
			continue
		}
		if st.FPSWindow > 0 && st.FPSWindow < streamLowFPSThreshold {
			lowFPSStreak[camID]++
			if lowFPSStreak[camID] >= streamLowFPSWindows {
				logger.Warn("[CAMERA] Watchdog: cam%d sustained low FPS (%.1f) — restarting", camID, st.FPSWindow)
				restartOneStream(cfg, camID)
			}
		} else {
			lowFPSStreak[camID] = 0
		}
	}
}

func isStreamerRunningForCamera(camID int) bool {
	streamerMu.Lock()
	defer streamerMu.Unlock()
	for _, sp := range streamerProcs {
		if sp != nil && sp.cameraID == camID && sp.cmd != nil && sp.cmd.Process != nil {
			if sp.cmd.ProcessState == nil {
				return true
			}
		}
	}
	return false
}

func restartOneStream(cfg *config.Config, camID int) {
	if since, ok := lastRestartAt[camID]; ok && time.Since(since) < streamRestartCooldown {
		return
	}
	lastRestartAt[camID] = time.Now()
	lowFPSStreak[camID] = 0

	var stream config.CameraStreamConfig
	found := false
	for _, s := range cfg.Camera.Streams {
		if s.CameraID == camID && s.Enabled {
			stream = s
			found = true
			break
		}
	}
	if !found {
		return
	}
	stopStreamerForCamera(camID)
	time.Sleep(800 * time.Millisecond)
	if err := SyncStreamerConfigFiles(cfg); err != nil {
		logger.Warn("[CAMERA] Watchdog sync config cam%d: %v", camID, err)
	}
	multiCam := EnabledStreamCount(cfg) > 1
	if _, err := startStreamForConfig(cfg, stream, multiCam); err != nil {
		logger.Warn("[CAMERA] Watchdog restart cam%d failed: %v", camID, err)
	} else {
		logger.Info("[CAMERA] Watchdog restarted cam%d", camID)
	}
}

func stopStreamerForCamera(camID int) {
	streamerMu.Lock()
	var keep []*streamerProc
	for _, sp := range streamerProcs {
		if sp == nil || sp.cameraID != camID {
			keep = append(keep, sp)
			continue
		}
		terminateStreamerProc(sp)
	}
	streamerProcs = keep
	streamerMu.Unlock()
}

func terminateStreamerProc(sp *streamerProc) {
	if sp == nil || sp.cmd == nil || sp.cmd.Process == nil {
		return
	}
	_ = sp.cmd.Process.Kill()
}

func onStreamerExited(camID int) {
	watchdogMu.Lock()
	cfg := watchdogCfg
	watchdogMu.Unlock()
	if cfg == nil || !cfg.Camera.Enabled {
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		watchdogMu.Lock()
		suppressed := time.Now().Before(suppressRestartUntil)
		watchdogMu.Unlock()
		if suppressed || isStreamerRunningForCamera(camID) {
			return
		}
		logger.Warn("[CAMERA] Streamer cam%d exited — watchdog restart", camID)
		restartOneStream(cfg, camID)
	}()
}
