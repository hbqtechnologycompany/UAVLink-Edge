package web

import (
	"fmt"
	"log"
	"os"

	"DroneBridge/camera"
	"DroneBridge/config"
)

var appConfigFile string

// InitAppConfig sets the single config.yaml path (same as main --config).
func InitAppConfig(path string) {
	if path != "" {
		appConfigFile = path
	}
}

func resolveAppConfigPath() string {
	if appConfigFile != "" {
		return appConfigFile
	}
	for _, p := range []string{
		projectPath("config.yaml"),
		"/opt/dronebridge/config.yaml",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return projectPath("config.yaml")
}

func loadAppConfig() (*config.Config, error) {
	return config.Load(resolveAppConfigPath())
}

func saveAppConfig(cfg *config.Config) error {
	return cfg.Save(resolveAppConfigPath())
}

func mustLoadAppConfig() *config.Config {
	cfg, err := loadAppConfig()
	if err != nil {
		return &config.Config{}
	}
	return cfg
}

func findStreamIndex(cfg *config.Config, cameraID int) int {
	for i, s := range cfg.Camera.Streams {
		if s.CameraID == cameraID {
			return i
		}
	}
	return -1
}

func defaultStream(id int, name string, enabled, detection, overlay bool) config.CameraStreamConfig {
	burn := true
	return config.CameraStreamConfig{
		Name:               name,
		CameraID:           id,
		Enabled:            enabled,
		Size:               []int{640, 480},
		Framerate:          30,
		Bitrate:            5000,
		Format:             "BGR888",
		Brightness:         0,
		Contrast:           1,
		Sharpness:          1.5,
		Saturation:         1.0,
		ExposureTime:       0,
		DetectionEnabled:   detection,
		OverlayEnabled:     overlay,
		OverlayBurnEnabled: &burn,
		GateTimeoutMs:      0,
		DetectFrameSkip:    3,
		OverlayFrameSkip:   5,
		LoresSize:          []int{320, 240},
		KeyframeInterval:   30,
		Preset:             "veryfast",
		Tune:               "zerolatency",
	}
}

func ensureDefaultStreams(cfg *config.Config) {
	camera.EnsureDefaultStreams(cfg)
}

func mediamtxForUI(cfg *config.Config) camera.Mediamtx {
	return camera.ResolveMediamtx(cfg)
}

func cvLoresSizeForMain(main []int) []int {
	if len(main) < 2 {
		return []int{320, 240}
	}
	w, h := main[0], main[1]
	const maxW, maxH = 320, 240
	if w*h <= maxW*maxH {
		return []int{w, h}
	}
	scaleW := float64(maxW) / float64(w)
	scaleH := float64(maxH) / float64(h)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	lw := int(float64(w)*scale + 0.5)
	lh := int(float64(h)*scale + 0.5)
	if lw < 1 {
		lw = 1
	}
	if lh < 1 {
		lh = 1
	}
	return []int{lw, lh}
}

func loresSizeForUI(main []int) []int {
	return cvLoresSizeForMain(main)
}

func boolPtrFromMap(m map[string]interface{}, key string, fallback bool) *bool {
	v, ok := m[key]
	if !ok {
		b := fallback
		return &b
	}
	if b, ok := v.(bool); ok {
		return &b
	}
	b := fallback
	return &b
}

func cameraConfigToUI(cfg *config.Config, cameraID int) map[string]interface{} {
	ensureDefaultStreams(cfg)

	stream := defaultStream(cameraID, fmt.Sprintf("cam%d", cameraID), true, true, true)
	if idx := findStreamIndex(cfg, cameraID); idx >= 0 {
		stream = cfg.Camera.Streams[idx]
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
	bitrate := stream.Bitrate
	if bitrate <= 0 {
		bitrate = 5000
	}

	m := mediamtxForUI(cfg)
	pub, _ := camera.PublishPath(cfg, cameraID)

	return map[string]interface{}{
		"camera_id":         cameraID,
		"name":              stream.Name,
		"enabled":           stream.Enabled,
		"stream_role":       camera.StreamRole(cfg, cameraID),
		"primary_camera_id": cfg.Camera.PrimaryCameraID,
		"memory_tier_gb":    int(camera.DetectMemoryTier()),
		"auto_memory_profile": cfg.Camera.AutoMemoryProfile,
		"size":              size,
		"framerate":         framerate,
		"format":            format,
		"mediamtx_host":     m.Host,
		"mediamtx_port":     m.RTSPPort,
		"mediamtx_webrtc_port": m.WebRTCPort,
		"mediamtx_hls_port": m.HLSPort,
		"publish_path":      pub,
		"drone_id":          cfg.Auth.UUID,
		"bitrate":           bitrate,
		"detection_enabled": stream.DetectionEnabled,
		"overlay_enabled":   stream.OverlayEnabled,
		"overlay_burn_enabled": camera.StreamOverlayBurnEnabled(stream),
		"gate_timeout_ms":   stream.GateTimeoutMs,
		"detect_frame_skip": stream.DetectFrameSkip,
		"overlay_frame_skip": stream.OverlayFrameSkip,
		"lores_size":        loresSizeForUI(size),
		"keyframe_interval": stream.KeyframeInterval,
		"preset":            stream.Preset,
		"tune":              stream.Tune,
		"brightness":        stream.Brightness,
		"contrast":          stream.Contrast,
		"sharpness":         stream.Sharpness,
		"saturation":        stream.Saturation,
		"exposure_time":     stream.ExposureTime,
		"landing_detection_mode": stream.LandingDetectionMode,
		"aruco_marker_id":       stream.ArucoMarkerId,
		"aruco_dictionary":      stream.ArucoDictionary,
		"detection_threshold":   stream.DetectionThreshold,
		"landing_fullframe_fallback": stream.LandingFullframeFallback != nil && *stream.LandingFullframeFallback,
	}
}

func cameraStreamsSummary(cfg *config.Config) []map[string]interface{} {
	ensureDefaultStreams(cfg)
	out := make([]map[string]interface{}, 0, len(cfg.Camera.Streams))
	for _, s := range cfg.Camera.Streams {
		size := s.Size
		if len(size) < 2 {
			size = []int{640, 480}
		}
		out = append(out, map[string]interface{}{
			"camera_id": s.CameraID,
			"name":      s.Name,
			"enabled":   s.Enabled,
			"size":      size,
			"format":    s.Format,
		})
	}
	return out
}

func intFromMap(m map[string]interface{}, key string, fallback int) int {
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return fallback
	}
}

func floatFromMap(m map[string]interface{}, key string, fallback float64) float64 {
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return fallback
	}
}

func stringFromMap(m map[string]interface{}, key string, fallback string) string {
	v, ok := m[key]
	if !ok {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

func boolFromMap(m map[string]interface{}, key string, fallback bool) bool {
	v, ok := m[key]
	if !ok {
		return fallback
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

func sizeFromMap(m map[string]interface{}, key string, fallback []int) []int {
	v, ok := m[key]
	if !ok {
		return fallback
	}
	arr, ok := v.([]interface{})
	if !ok || len(arr) < 2 {
		return fallback
	}
	w := intFromMap(map[string]interface{}{"v": arr[0]}, "v", fallback[0])
	h := intFromMap(map[string]interface{}{"v": arr[1]}, "v", fallback[1])
	return []int{w, h}
}

func saveCameraStreamFromUI(incoming map[string]interface{}) error {
	cfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	ensureDefaultStreams(cfg)

	cameraID := intFromMap(incoming, "camera_id", 0)
	stream := defaultStream(cameraID, fmt.Sprintf("cam%d", cameraID), true, true, true)
	if idx := findStreamIndex(cfg, cameraID); idx >= 0 {
		stream = cfg.Camera.Streams[idx]
	}

	if v := stringFromMap(incoming, "name", ""); v != "" {
		stream.Name = v
	}
	stream.Enabled = boolFromMap(incoming, "enabled", stream.Enabled)
	stream.Format = stringFromMap(incoming, "format", stream.Format)
	stream.Size = sizeFromMap(incoming, "size", stream.Size)
	if _, hasLores := incoming["lores_size"]; hasLores {
		stream.LoresSize = sizeFromMap(incoming, "lores_size", cvLoresSizeForMain(stream.Size))
	} else {
		stream.LoresSize = cvLoresSizeForMain(stream.Size)
	}
	stream.Framerate = intFromMap(incoming, "framerate", stream.Framerate)
	stream.Bitrate = intFromMap(incoming, "bitrate", stream.Bitrate)
	stream.Brightness = floatFromMap(incoming, "brightness", stream.Brightness)
	stream.Contrast = floatFromMap(incoming, "contrast", stream.Contrast)
	stream.Sharpness = floatFromMap(incoming, "sharpness", stream.Sharpness)
	stream.Saturation = floatFromMap(incoming, "saturation", stream.Saturation)
	stream.ExposureTime = intFromMap(incoming, "exposure_time", stream.ExposureTime)
	stream.DetectionEnabled = boolFromMap(incoming, "detection_enabled", stream.DetectionEnabled)
	stream.OverlayEnabled = boolFromMap(incoming, "overlay_enabled", stream.OverlayEnabled)
	stream.OverlayBurnEnabled = boolPtrFromMap(incoming, "overlay_burn_enabled", camera.StreamOverlayBurnEnabled(stream))
	stream.GateTimeoutMs = intFromMap(incoming, "gate_timeout_ms", stream.GateTimeoutMs)
	stream.DetectFrameSkip = intFromMap(incoming, "detect_frame_skip", stream.DetectFrameSkip)
	stream.OverlayFrameSkip = intFromMap(incoming, "overlay_frame_skip", stream.OverlayFrameSkip)
	stream.LandingDetectionMode = stringFromMap(incoming, "landing_detection_mode", stream.LandingDetectionMode)
	stream.ArucoMarkerId = intFromMap(incoming, "aruco_marker_id", stream.ArucoMarkerId)
	stream.ArucoDictionary = stringFromMap(incoming, "aruco_dictionary", stream.ArucoDictionary)
	stream.DetectionThreshold = floatFromMap(incoming, "detection_threshold", stream.DetectionThreshold)
	if v, ok := incoming["landing_fullframe_fallback"]; ok {
		_ = v
		b := boolFromMap(incoming, "landing_fullframe_fallback", true)
		stream.LandingFullframeFallback = &b
	}
	stream.KeyframeInterval = intFromMap(incoming, "keyframe_interval", stream.KeyframeInterval)
	stream.Preset = stringFromMap(incoming, "preset", stream.Preset)
	stream.Tune = stringFromMap(incoming, "tune", stream.Tune)

	if idx := findStreamIndex(cfg, cameraID); idx >= 0 {
		cfg.Camera.Streams[idx] = stream
	} else {
		cfg.Camera.Streams = append(cfg.Camera.Streams, stream)
	}

	if err := saveAppConfig(cfg); err != nil {
		return err
	}
	refreshStreamerConfigs()
	return nil
}

// saveCameraGlobalFromUI updates camera-wide settings (primary camera, memory profile).
func saveCameraGlobalFromUI(incoming map[string]interface{}) error {
	cfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	ensureDefaultStreams(cfg)

	if v, ok := incoming["primary_camera_id"]; ok {
		id := intFromMap(map[string]interface{}{"v": v}, "v", cfg.Camera.PrimaryCameraID)
		if id == 0 || id == 1 {
			cfg.Camera.PrimaryCameraID = id
		}
	}
	if _, ok := incoming["auto_memory_profile"]; ok {
		cfg.Camera.AutoMemoryProfile = boolFromMap(incoming, "auto_memory_profile", true)
	}

	if err := saveAppConfig(cfg); err != nil {
		return err
	}
	refreshStreamerConfigs()
	return nil
}

// refreshStreamerConfigs writes camera_config_*.json from current config.yaml.
func refreshStreamerConfigs() {
	cfg, err := loadAppConfig()
	if err != nil {
		return
	}
	if err := camera.SyncStreamerConfigFiles(cfg); err != nil {
		log.Printf("[CAMERA] Failed to refresh streamer configs: %v", err)
	}
}

// saveCameraStreamsEnable updates enabled flags for one or more streams.
func saveCameraStreamsEnable(streams []map[string]interface{}) error {
	cfg, err := loadAppConfig()
	if err != nil {
		return err
	}
	ensureDefaultStreams(cfg)

	for _, item := range streams {
		cameraID := intFromMap(item, "camera_id", -1)
		if cameraID < 0 {
			continue
		}
		enabled := boolFromMap(item, "enabled", true)
		stream := defaultStream(cameraID, fmt.Sprintf("cam%d", cameraID), true, true, true)
		if idx := findStreamIndex(cfg, cameraID); idx >= 0 {
			stream = cfg.Camera.Streams[idx]
		}
		stream.Enabled = enabled
		if idx := findStreamIndex(cfg, cameraID); idx >= 0 {
			cfg.Camera.Streams[idx] = stream
		} else {
			cfg.Camera.Streams = append(cfg.Camera.Streams, stream)
		}
	}

	if err := saveAppConfig(cfg); err != nil {
		return err
	}
	refreshStreamerConfigs()
	return nil
}
