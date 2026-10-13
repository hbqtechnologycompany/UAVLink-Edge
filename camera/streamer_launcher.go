package camera

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"DroneBridge/config"
	"DroneBridge/logger"
)

const (
	streamerStaggerFirst    = 800 * time.Millisecond
	streamerStaggerFollower = 2500 * time.Millisecond
)

type streamerProc struct {
	cameraID   int
	cmd        *exec.Cmd
	helperCmds []*exec.Cmd
	mode       string // "python" or "hw"
}

var (
	streamerMu    sync.Mutex
	streamerProcs []*streamerProc
)

// EnabledStreamCount returns how many camera streams are enabled in config.
func EnabledStreamCount(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	n := 0
	for _, s := range cfg.Camera.Streams {
		if s.Enabled {
			n++
		}
	}
	return n
}

// RTSPBackendName returns the publisher backend for one stream (single policy for all cameras).
func RTSPBackendName(stream config.CameraStreamConfig, multiCam bool) string {
	if StreamUsesFFmpegBackend(stream, multiCam) {
		return "ffmpeg"
	}
	return "gstreamer"
}

// StreamerEnv builds process environment for camera_streamer.py (same rules for cam0 and cam1).
func StreamerEnv(stream config.CameraStreamConfig, multiCam bool) []string {
	env := append(os.Environ(), fmt.Sprintf("DRONEBRIDGE_MULTI_CAMERA=%d", boolToInt(multiCam)))
	backend := RTSPBackendName(stream, multiCam)
	env = append(env, "DRONEBRIDGE_RTSP_BACKEND="+backend)
	return env
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// SyncStreamerConfigFiles writes camera_config_*.json for every stream in config.yaml.
func SyncStreamerConfigFiles(cfg *config.Config) error {
	EnsureDefaultStreams(cfg)
	dir := FindLandingDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir Find_landing: %w", err)
	}

	m := ResolveMediamtx(cfg)
	droneID := DroneID(cfg)
	if droneID == "" {
		return fmt.Errorf("auth.uuid is required to publish camera streams")
	}

	multiCam := EnabledStreamCount(cfg) > 1

	for _, stream := range cfg.Camera.Streams {
		pub, err := PublishPath(cfg, stream.CameraID)
		if err != nil {
			return err
		}

		size := stream.Size
		if len(size) < 2 {
			size = []int{640, 480}
		}
		format := stream.Format
		if format == "" {
			format = "BGR888"
		}
		framerate := stream.Framerate
		if framerate <= 0 {
			framerate = 30
		}
		preset := stream.Preset
		if preset == "" {
			preset = "veryfast"
		}
		tune := stream.Tune
		if tune == "" {
			tune = "zerolatency"
		}
		keyframe := stream.KeyframeInterval
		if keyframe <= 0 {
			keyframe = 15
		}
		if keyframe > 30 {
			keyframe = 30
		}

		encodeSize := hwEncodeSize(cfg, stream)
		memProf := BuildStreamMemoryProfile(cfg, stream)
		bufCount := memProf.BufferCount
		NormalizeCameraStream(&stream)
		loresSize := memProf.LoresSize
		if len(loresSize) < 2 {
			loresSize = []int{320, 240}
		}
		effectiveSize := memProf.Size
		if len(effectiveSize) < 2 {
			effectiveSize = size
		}
		detectSkip := memProf.DetectFrameSkip
		overlaySkip := memProf.OverlayFrameSkip
		effectiveBitrate := memProf.Bitrate

		payload := streamerJSON{
			CameraID:                 stream.CameraID,
			Size:                     effectiveSize,
			Framerate:                framerate,
			Format:                   format,
			MediamtxHost:             m.Host,
			MediamtxPort:             m.RTSPPort,
			MediamtxWebRTCPort:       m.WebRTCPort,
			MediamtxHLSPort:          m.HLSPort,
			PublishPath:              pub,
			DroneID:                  droneID,
			Bitrate:                  effectiveBitrate,
			OverlayEnabled:           stream.OverlayEnabled,
			OverlayBurnEnabled:       StreamOverlayBurnEnabled(stream),
			DetectionEnabled:         stream.DetectionEnabled,
			GateTimeoutMs:            stream.GateTimeoutMs,
			DetectFrameSkip:          detectSkip,
			DetectionThreshold:       streamDetectionThreshold(stream),
			LandingDetectionMode:     streamLandingDetectionMode(stream),
			ArucoMarkerId:            streamArucoMarkerId(stream),
			ArucoDictionary:          streamArucoDictionary(stream),
			LandingFullframeFallback: streamLandingFullframeFallback(stream),
			OverlayFrameSkip:         overlaySkip,
			LoresSize:                loresSize,
			ExternalLanding:          os.Getenv("DRONEBRIDGE_EXTERNAL_LANDING") == "1",
			RTSPBackend:              RTSPBackendName(stream, multiCam),
			MultiCamera:              multiCam,
			KeyframeInterval:         keyframe,
			Preset:                   preset,
			Tune:                     tune,
			Brightness:               stream.Brightness,
			Contrast:                 stream.Contrast,
			Sharpness:                stream.Sharpness,
			Saturation:               stream.Saturation,
			ExposureTime:             stream.ExposureTime,
			BufferCount:              bufCount,
			LowMemoryMode:            memProf.LowMemoryMode,
			EncodeSize:               encodeSize,
			MemoryTierGB:             int(memProf.Tier),
			MemoryProfileApplied:     memProf.Applied,
			StreamRole:               memProf.StreamRole,
			PhysicalCameraID:         stream.CameraID,
		}

		name := streamConfigFilename(stream.CameraID)
		fullPath := filepath.Join(dir, name)
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, append(data, '\n'), 0644); err != nil {
			return fmt.Errorf("write %s: %w", fullPath, err)
		}
	}
	return nil
}

// ListStreamerPIDs returns PIDs of tracked camera publisher processes.
func ListStreamerPIDs() []int {
	streamerMu.Lock()
	defer streamerMu.Unlock()
	var pids []int
	for _, sp := range streamerProcs {
		if sp != nil && sp.cmd != nil && sp.cmd.Process != nil {
			pids = append(pids, sp.cmd.Process.Pid)
		}
	}
	if len(pids) > 0 {
		return pids
	}
	out, err := exec.Command("pgrep", "-f", "camera_streamer.py").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if pid, convErr := strconv.Atoi(line); convErr == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// StopStreamers terminates all camera publisher processes (HW or Python).
func StopStreamers() {
	watchdogMu.Lock()
	suppressRestartUntil = time.Now().Add(5 * time.Second)
	watchdogMu.Unlock()
	StopStreamWatchdog()
	stopTrackedStreamers()
	_ = exec.Command("pkill", "-f", "camera_streamer.py").Run()
	_ = exec.Command("pkill", "-f", "rpicam-vid").Run()
	_ = exec.Command("pkill", "-f", "ffmpeg.*8554/").Run()
	time.Sleep(500 * time.Millisecond)
}

// StartStreamers syncs config and launches each enabled stream with its own pipeline (H1 HW or Python H1+H2).
func StartStreamers(cfg *config.Config) ([]int, error) {
	if cfg == nil {
		return nil, fmt.Errorf("camera config is nil")
	}
	if !cfg.Camera.Enabled {
		return nil, fmt.Errorf("camera disabled in config")
	}
	if err := SyncStreamerConfigFiles(cfg); err != nil {
		return nil, err
	}

	StopStreamers()

	streams := enabledStreamsSorted(cfg)
	if cfg.Camera.SkipMissingSensors {
		streams = filterStreamsWithSensors(streams)
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("no enabled camera streams in config")
	}

	streams = sortStreamsForStartup(streams)

	logger.Info("[CAMERA] Starting unified Hướng 1 streamers (Python HW H264 per camera)")
	started, err := startMixedStreamers(cfg, streams)
	if err != nil {
		return started, err
	}
	StartStreamWatchdog(cfg)
	return started, nil
}

func startMixedStreamers(cfg *config.Config, streams []config.CameraStreamConfig) ([]int, error) {
	multiCam := len(streams) > 1
	var started []int
	for i, stream := range streams {
		pid, err := startStreamForConfig(cfg, stream, multiCam)
		if err != nil {
			logger.Warn("[CAMERA] Failed to start streamer cam%d: %v", stream.CameraID, err)
			continue
		}
		started = append(started, pid)
		if i < len(streams)-1 {
			time.Sleep(streamerStaggerAfter(stream, streams[i+1], multiCam))
		}
	}
	if len(started) == 0 {
		return nil, fmt.Errorf("no camera streamer started (check Python deps and logs)")
	}
	return started, nil
}

func startStreamForConfig(cfg *config.Config, stream config.CameraStreamConfig, multiCam bool) (int, error) {
	return startOnePythonStreamer(cfg, stream, multiCam)
}

// sortStreamsForStartup — cả hai camera cùng pipeline; khởi động theo camera_id.
func sortStreamsForStartup(streams []config.CameraStreamConfig) []config.CameraStreamConfig {
	out := make([]config.CameraStreamConfig, len(streams))
	copy(out, streams)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CameraID < out[j].CameraID
	})
	return out
}

func streamerStaggerAfter(current, next config.CameraStreamConfig, multiCam bool) time.Duration {
	_ = current
	_ = next
	if multiCam {
		return streamerStaggerFollower
	}
	return streamerStaggerFirst
}

func newCameraStreamerCmd(findDir, cfgFile string, env []string) *exec.Cmd {
	cmd := exec.Command("python3", "camera_streamer.py", cfgFile)
	cmd.Dir = findDir
	cmd.Env = env
	return cmd
}

func startOnePythonStreamer(cfg *config.Config, stream config.CameraStreamConfig, multiCam bool) (int, error) {
	findDir := FindLandingDir()
	script := filepath.Join(findDir, "camera_streamer.py")
	if _, err := os.Stat(script); err != nil {
		return 0, fmt.Errorf("camera_streamer.py not found in %s: %w", findDir, err)
	}

	cfgFile := streamConfigFilename(stream.CameraID)
	cmd := newCameraStreamerCmd(findDir, cfgFile, StreamerEnv(stream, multiCam))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	prefix := fmt.Sprintf("[CAMERA][cam%d]", stream.CameraID)
	cmd.Stdout = &processLogWriter{prefix: prefix}
	cmd.Stderr = &processLogWriter{prefix: prefix, isErr: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	pid := cmd.Process.Pid
	streamerMu.Lock()
	streamerProcs = append(streamerProcs, &streamerProc{cameraID: stream.CameraID, cmd: cmd, mode: "python"})
	streamerMu.Unlock()

	logger.Info("[CAMERA] Streamer cam%d started (PID: %d, backend: %s, config: %s)",
		stream.CameraID, pid, RTSPBackendName(stream, multiCam), cfgFile)

	go waitStreamerProc(cmd, stream.CameraID)
	return pid, nil
}

func startPythonStreamers(cfg *config.Config, streams []config.CameraStreamConfig) ([]int, error) {
	multiCam := EnabledStreamCount(cfg) > 1
	var started []int
	for i, stream := range streams {
		pid, err := startOnePythonStreamer(cfg, stream, multiCam)
		if err != nil {
			logger.Warn("[CAMERA] Failed to start streamer cam%d: %v", stream.CameraID, err)
			continue
		}
		started = append(started, pid)
		if i < len(streams)-1 {
			time.Sleep(streamerStaggerAfter(stream, streams[i+1], multiCam))
		}
	}
	if len(started) == 0 {
		return nil, fmt.Errorf("no camera streamer started (check Python deps and logs)")
	}
	return started, nil
}

// RestartStreamers stops then starts streamers from config.
func RestartStreamers(cfg *config.Config) ([]int, error) {
	StopStreamers()
	time.Sleep(1 * time.Second)
	return StartStreamers(cfg)
}

func enabledStreamsSorted(cfg *config.Config) []config.CameraStreamConfig {
	var out []config.CameraStreamConfig
	for _, s := range cfg.Camera.Streams {
		if s.Enabled {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CameraID < out[j].CameraID
	})
	return out
}

// filterStreamsWithSensors drops enabled streams whose CSI port has no connected sensor (I-8).
func filterStreamsWithSensors(streams []config.CameraStreamConfig) []config.CameraStreamConfig {
	result, err := ScanLive(false)
	if err != nil || result == nil {
		return streams
	}
	var out []config.CameraStreamConfig
	for _, s := range streams {
		portName := fmt.Sprintf("cam%d", s.CameraID)
		ps, ok := result.Ports[portName]
		if !ok {
			out = append(out, s)
			continue
		}
		if ps.Status == "ok" {
			out = append(out, s)
			continue
		}
		if ps.Status == "disabled" {
			continue
		}
		logger.Info("[CAMERA] Skipping cam%d — no sensor on %s (%s)", s.CameraID, portName, ps.Status)
	}
	return out
}

func waitStreamerProc(cmd *exec.Cmd, camID int) {
	waitErr := cmd.Wait()
	streamerMu.Lock()
	defer streamerMu.Unlock()
	for i, sp := range streamerProcs {
		if sp != nil && sp.cmd == cmd {
			streamerProcs = append(streamerProcs[:i], streamerProcs[i+1:]...)
			break
		}
	}
	if waitErr != nil {
		logger.Warn("[CAMERA] Streamer cam%d exited: %v", camID, waitErr)
	} else {
		logger.Info("[CAMERA] Streamer cam%d stopped", camID)
	}
	onStreamerExited(camID)
}

type processLogWriter struct {
	prefix string
	isErr  bool
	buf    bytes.Buffer
}

func (w *processLogWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if w.isErr {
			logger.Warn("%s %s", w.prefix, line)
		} else {
			logger.Info("%s %s", w.prefix, line)
		}
	}
	return len(p), nil
}
