package camera

import (
	"fmt"

	"DroneBridge/config"
)

type streamerJSON struct {
	CameraID           int    `json:"camera_id"`
	Size               []int  `json:"size"`
	Framerate          int    `json:"framerate"`
	Format             string `json:"format"`
	MediamtxHost       string `json:"mediamtx_host"`
	MediamtxPort       int    `json:"mediamtx_port"`
	MediamtxWebRTCPort int    `json:"mediamtx_webrtc_port"`
	MediamtxHLSPort    int    `json:"mediamtx_hls_port"`
	PublishPath        string `json:"publish_path"`
	DroneID            string `json:"drone_id"`
	Bitrate            int    `json:"bitrate"`
	OverlayEnabled     bool   `json:"overlay_enabled"`
	OverlayBurnEnabled bool   `json:"overlay_burn_enabled"`
	DetectionEnabled   bool   `json:"detection_enabled"`
	GateTimeoutMs      int    `json:"gate_timeout_ms"`
	DetectFrameSkip            int     `json:"detect_frame_skip"`
	DetectionThreshold       float64 `json:"detection_threshold"`
	LandingDetectionMode   string  `json:"landing_detection_mode"`
	ArucoMarkerId            int     `json:"aruco_marker_id"`
	ArucoDictionary          string  `json:"aruco_dictionary"`
	LandingFullframeFallback bool    `json:"landing_fullframe_fallback"`
	OverlayFrameSkip      int     `json:"overlay_frame_skip"`
	LoresSize          []int  `json:"lores_size"`
	ExternalLanding    bool   `json:"external_landing"`
	RTSPBackend        string `json:"rtsp_backend"`
	MultiCamera        bool   `json:"multi_camera"`
	KeyframeInterval   int    `json:"keyframe_interval"`
	Preset             string  `json:"preset"`
	Tune               string  `json:"tune"`
	Brightness         float64 `json:"brightness"`
	Contrast           float64 `json:"contrast"`
	Sharpness          float64 `json:"sharpness"`
	Saturation         float64 `json:"saturation"`
	ExposureTime       int     `json:"exposure_time"`
	BufferCount        int     `json:"buffer_count"`
	LowMemoryMode      bool    `json:"low_memory_mode"`
	EncodeSize         []int   `json:"encode_size,omitempty"`
	MemoryTierGB       int     `json:"memory_tier_gb"`
	MemoryProfileApplied bool  `json:"memory_profile_applied"`
	StreamRole         string  `json:"stream_role"`
	PhysicalCameraID   int     `json:"physical_camera_id"`
}

// StreamUsesFFmpegBackend — Hướng 1 thống nhất luôn dùng FFmpeg RTSP push.
func StreamUsesFFmpegBackend(stream config.CameraStreamConfig, multiCam bool) bool {
	_ = stream
	_ = multiCam
	return true
}

func streamConfigFilename(cameraID int) string {
	return fmt.Sprintf("camera_config_%d.json", cameraID)
}

func streamBufferCount(cfg *config.Config) int {
	n := 3
	if cfg != nil && cfg.Camera.StreamBufferCount > 0 {
		n = cfg.Camera.StreamBufferCount
	}
	if cfg != nil && cfg.Camera.LowMemoryMode && n > 2 {
		n = 2
	}
	if n < 1 {
		n = 1
	}
	if n > 6 {
		n = 6
	}
	return n
}

// hwEncodeSize picks rpicam output size; low_memory_mode and RAM tier cap above 640x480.
func hwEncodeSize(cfg *config.Config, stream config.CameraStreamConfig) []int {
	prof := BuildStreamMemoryProfile(cfg, stream)
	return prof.Size
}
