package camera

import (
	"fmt"
	"net/url"

	"DroneBridge/config"
)

// Mediamtx holds resolved MediaMTX endpoints from config.yaml (camera.* + auth.host).
type Mediamtx struct {
	Host       string
	RTSPPort   int
	WebRTCPort int
	HLSPort    int
}

// EnsureDefaultStreams fills camera.streams when empty (cam0 + cam1 defaults).
func EnsureDefaultStreams(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if cfg.Camera.PrimaryCameraID != 0 && cfg.Camera.PrimaryCameraID != 1 {
		cfg.Camera.PrimaryCameraID = 0
	}
	if len(cfg.Camera.Streams) > 0 {
		return
	}
	cfg.Camera.Streams = []config.CameraStreamConfig{
		{Name: "cam0", CameraID: 0, Enabled: true, Size: []int{640, 480}, Framerate: 30, Bitrate: 5000, Format: "RGB888", Brightness: 0, Contrast: 1, Sharpness: 1.5, Saturation: 1.0, DetectionEnabled: true, OverlayEnabled: true, KeyframeInterval: 30, Preset: "veryfast", Tune: "zerolatency"},
		{Name: "cam1", CameraID: 1, Enabled: true, Size: []int{640, 480}, Framerate: 30, Bitrate: 5000, Format: "RGB888", Brightness: 0, Contrast: 1, Sharpness: 1.5, Saturation: 1.0, DetectionEnabled: true, OverlayEnabled: true, KeyframeInterval: 30, Preset: "veryfast", Tune: "zerolatency"},
	}
}

// ResolveMediamtx reads host/ports from config.yaml.
func ResolveMediamtx(cfg *config.Config) Mediamtx {
	host := cfg.Camera.MediamtxHost
	if host == "" {
		host = cfg.Auth.Host
	}
	m := Mediamtx{
		Host:       host,
		RTSPPort:   8554,
		WebRTCPort: 8889,
		HLSPort:    8888,
	}
	if cfg.Camera.MediamtxPort > 0 {
		m.RTSPPort = cfg.Camera.MediamtxPort
	}
	if cfg.Camera.MediamtxWebrtcPort > 0 {
		m.WebRTCPort = cfg.Camera.MediamtxWebrtcPort
	}
	if cfg.Camera.MediamtxHlsPort > 0 {
		m.HLSPort = cfg.Camera.MediamtxHlsPort
	}
	return m
}

// DroneID returns auth.uuid used in RTSP publish paths.
func DroneID(cfg *config.Config) string {
	return cfg.Auth.UUID
}

// PrimaryCameraID returns which physical camera_id maps to the /cam0 publish path.
func PrimaryCameraID(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	if cfg.Camera.PrimaryCameraID == 0 || cfg.Camera.PrimaryCameraID == 1 {
		return cfg.Camera.PrimaryCameraID
	}
	return 0
}

// StreamRole returns "primary" (/cam0) or "secondary" (/cam1) for a physical camera_id.
func StreamRole(cfg *config.Config, cameraID int) string {
	if cameraID == PrimaryCameraID(cfg) {
		return "primary"
	}
	return "secondary"
}

// PublishPath returns the MediaMTX RTSP path for a camera.
// Primary camera → /{uuid}/cam0 ; secondary → /{uuid}/cam1 (suffixes configurable).
func PublishPath(cfg *config.Config, cameraID int) (string, error) {
	id := DroneID(cfg)
	if id == "" {
		return "", fmt.Errorf("auth.uuid is required for camera publish path")
	}
	escaped := url.PathEscape(id)
	slot := 1
	if StreamRole(cfg, cameraID) == "primary" {
		slot = 0
	}
	suffix := publishPathSuffix(cfg, slot)
	return "/" + escaped + "/" + suffix, nil
}

func publishPathSuffix(cfg *config.Config, cameraID int) string {
	switch cameraID {
	case 0:
		if s := cfg.Camera.PublishPathCam0Suffix; s != "" {
			return s
		}
		return "cam0"
	case 1:
		if s := cfg.Camera.PublishPathCam1Suffix; s != "" {
			return s
		}
		return "cam1"
	default:
		return fmt.Sprintf("cam%d", cameraID)
	}
}

// RTSPURL builds rtsp://host:port/{path}.
func RTSPURL(m Mediamtx, publishPath string) string {
	return fmt.Sprintf("rtsp://%s:%d%s", m.Host, m.RTSPPort, publishPath)
}

// WebRTCURL builds the WHEP viewer URL.
func WebRTCURL(m Mediamtx, publishPath string) string {
	return fmt.Sprintf("http://%s:%d%s/whep", m.Host, m.WebRTCPort, publishPath)
}

// HLSURL builds the HLS playlist URL.
func HLSURL(m Mediamtx, publishPath string) string {
	return fmt.Sprintf("http://%s:%d%s/index.m3u8", m.Host, m.HLSPort, publishPath)
}

// StreamEndpoints returns RTSP/WebRTC/HLS targets per enabled camera (API/status).
func StreamEndpoints(cfg *config.Config) ([]map[string]interface{}, error) {
	EnsureDefaultStreams(cfg)
	m := ResolveMediamtx(cfg)

	out := make([]map[string]interface{}, 0, len(cfg.Camera.Streams))
	for _, stream := range cfg.Camera.Streams {
		if !stream.Enabled {
			continue
		}
		pub, err := PublishPath(cfg, stream.CameraID)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"camera_id":    stream.CameraID,
			"name":         stream.Name,
			"stream_role":  StreamRole(cfg, stream.CameraID),
			"publish_path": pub,
			"rtsp":         RTSPURL(m, pub),
			"webrtc_whep":  WebRTCURL(m, pub),
			"hls":          HLSURL(m, pub),
		})
	}
	return out, nil
}
