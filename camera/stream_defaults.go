package camera

import (
	"DroneBridge/config"
)

// NormalizeCameraStream fills Hướng 2 defaults for one stream entry.
func NormalizeCameraStream(s *config.CameraStreamConfig) {
	if s == nil {
		return
	}
	if s.DetectFrameSkip <= 0 {
		s.DetectFrameSkip = 3
	}
	if s.OverlayFrameSkip <= 0 {
		s.OverlayFrameSkip = 5
	}
	if s.GateTimeoutMs < 0 {
		s.GateTimeoutMs = 0
	}
	if len(s.LoresSize) < 2 {
		s.LoresSize = []int{320, 240}
	}
}

// StreamOverlayBurnEnabled returns whether overlay pixels are burned on the encoded stream.
func StreamOverlayBurnEnabled(s config.CameraStreamConfig) bool {
	if !s.OverlayEnabled {
		return false
	}
	if s.OverlayBurnEnabled != nil {
		return *s.OverlayBurnEnabled
	}
	return true
}

func streamDetectionThreshold(s config.CameraStreamConfig) float64 {
	if s.DetectionThreshold > 0 {
		return s.DetectionThreshold
	}
	return 0.8
}

func streamLandingFullframeFallback(s config.CameraStreamConfig) bool {
	if s.LandingFullframeFallback != nil {
		return *s.LandingFullframeFallback
	}
	return true
}

func streamLandingDetectionMode(s config.CameraStreamConfig) string {
	if m := s.LandingDetectionMode; m != "" {
		return m
	}
	return "contour_h"
}

func streamArucoMarkerId(s config.CameraStreamConfig) int {
	if s.ArucoMarkerId >= 0 {
		return s.ArucoMarkerId
	}
	return 0
}

func streamArucoDictionary(s config.CameraStreamConfig) string {
	if d := s.ArucoDictionary; d != "" {
		return d
	}
	return "DICT_4X4_50"
}
