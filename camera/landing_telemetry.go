package camera

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"DroneBridge/config"
)

// LandingTelemetry is the latest Hướng 2 detection snapshot from Python.
type LandingTelemetry struct {
	CameraID        int      `json:"camera_id"`
	Detected        bool     `json:"detected"`
	OffsetX         *float64 `json:"offset_x"`
	OffsetY         *float64 `json:"offset_y"`
	Direction       *string  `json:"direction"`
	Similarity      *float64 `json:"similarity"`
	DetectionsCount int      `json:"detections_count"`
	UpdatedAt       float64  `json:"updated_at"`
	AgeSec          float64  `json:"age_sec,omitempty"`
}

// StreamStats is publisher FPS stats from /tmp/camera_stream_stats_{id}.json.
type StreamStats struct {
	CameraID    int     `json:"camera_id"`
	FPSSent     float64 `json:"fps_sent"`
	FPSWindow   float64 `json:"fps_window"`
	FPSCapture  float64 `json:"fps_capture"`
	FramesSent  int     `json:"frames_sent"`
	EncodeDrops int     `json:"encode_drops"`
	UpdatedAt   float64 `json:"updated_at"`
	AgeSec      float64 `json:"age_sec,omitempty"`
}

func landingTelemetryPath(cameraID int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("camera_landing_%d.json", cameraID))
}

func streamStatsPath(cameraID int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("camera_stream_stats_%d.json", cameraID))
}

// ReadLandingTelemetry loads the latest landing file for one camera (nil when missing/stale).
func ReadLandingTelemetry(cameraID int, maxAge time.Duration) *LandingTelemetry {
	data, err := os.ReadFile(landingTelemetryPath(cameraID))
	if err != nil {
		return nil
	}
	var t LandingTelemetry
	if err := json.Unmarshal(data, &t); err != nil {
		return nil
	}
	if t.UpdatedAt > 0 {
		t.AgeSec = time.Since(time.Unix(0, int64(t.UpdatedAt*1e9))).Seconds()
		if maxAge > 0 && t.AgeSec > maxAge.Seconds() {
			return nil
		}
	}
	return &t
}

// ReadStreamStats loads publisher stats for one camera.
func ReadStreamStats(cameraID int, maxAge time.Duration) *StreamStats {
	data, err := os.ReadFile(streamStatsPath(cameraID))
	if err != nil {
		return nil
	}
	var s StreamStats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	if s.UpdatedAt > 0 {
		s.AgeSec = time.Since(time.Unix(0, int64(s.UpdatedAt*1e9))).Seconds()
		if maxAge > 0 && s.AgeSec > maxAge.Seconds() {
			return nil
		}
	}
	return &s
}

// LandingStatusForConfig builds landing + stream stats for every enabled camera stream.
func LandingStatusForConfig(cfg *config.Config, maxAge time.Duration) []map[string]interface{} {
	if cfg == nil {
		return nil
	}
	EnsureDefaultStreams(cfg)
	out := make([]map[string]interface{}, 0, len(cfg.Camera.Streams))
	for _, s := range cfg.Camera.Streams {
		if !s.Enabled {
			continue
		}
		entry := map[string]interface{}{
			"camera_id": s.CameraID,
			"name":      s.Name,
		}
		if lt := ReadLandingTelemetry(s.CameraID, maxAge); lt != nil {
			entry["landing"] = lt
		}
		if st := ReadStreamStats(s.CameraID, maxAge); st != nil {
			entry["stream_stats"] = st
		}
		out = append(out, entry)
	}
	return out
}
