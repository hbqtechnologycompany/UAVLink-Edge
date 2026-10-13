package web

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"DroneBridge/camera"
	"DroneBridge/config"
)

func syncAllStreamerConfigs(cfg *config.Config) error {
	return camera.SyncStreamerConfigFiles(cfg)
}

func listRunningStreamerPIDs() []int {
	return camera.ListStreamerPIDs()
}

func stopCameraStreamers() {
	camera.StopStreamers()
}

func startCameraStreamersFromConfig() ([]int, error) {
	cfg, err := loadAppConfig()
	if err != nil {
		return nil, err
	}
	return camera.StartStreamers(cfg)
}

func restartCameraStreamers() ([]int, error) {
	cfg, err := loadAppConfig()
	if err != nil {
		return nil, err
	}
	return camera.RestartStreamers(cfg)
}

func cameraScript(name string) string {
	p := projectPath("install", "camera", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return projectPath(name)
}

func runSetupCameraScript() (bool, string, error) {
	cfgPath := resolveAppConfigPath()
	script := cameraScript("setup_camera.sh")
	if _, err := os.Stat(script); err != nil {
		return false, "", fmt.Errorf("setup_camera.sh not found: %w", err)
	}

	if _, err := exec.LookPath("sudo"); err != nil {
		out, checkErr := exec.Command("bash", script, "--check", cfgPath).CombinedOutput()
		text := string(out)
		if len(text) > 0 && text[len(text)-1] == '\n' {
			text = text[:len(text)-1]
		}
		needsReboot := checkErr != nil
		return needsReboot, text, nil
	}

	out, err := exec.Command("sudo", "-n", script, cfgPath).CombinedOutput()
	text := string(out)
	if len(text) > 0 && text[len(text)-1] == '\n' {
		text = text[:len(text)-1]
	}
	if err != nil {
		return false, text, fmt.Errorf("%s: %w", text, err)
	}
	needsReboot := strings.Contains(text, "đã cập nhật") ||
		fileExists(projectPath(".need_host_reboot")) ||
		fileExists(projectPath(".need_camera_reboot"))
	return needsReboot, text, nil
}

func requestHostOverlayApply() error {
	trigger := "/run/dronebridge/apply-camera-overlay.request"
	if err := os.MkdirAll("/run/dronebridge", 0o755); err != nil {
		return err
	}
	return os.WriteFile(trigger, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

func applyCameraOverlayHost() (string, error) {
	script := cameraScript("apply_camera_overlay.sh")
	if _, err := os.Stat(script); err != nil {
		return "", err
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		if err := requestHostOverlayApply(); err != nil {
			return "", err
		}
		return "Overlay/reboot request sent to host", nil
	}
	out, err := exec.Command("sudo", "-n", script, resolveAppConfigPath()).CombinedOutput()
	text := string(out)
	if len(text) > 0 && text[len(text)-1] == '\n' {
		text = text[:len(text)-1]
	}
	if err != nil {
		return text, fmt.Errorf("%s: %w", text, err)
	}
	return text, nil
}

func runHostRebootScript() error {
	script := cameraScript("apply_host_reboot.sh")
	if _, err := os.Stat(script); err != nil {
		return err
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return requestHostOverlayApply()
	}
	out, err := exec.Command("sudo", "-n", script).CombinedOutput()
	if err != nil {
		text := string(out)
		if len(text) > 0 && text[len(text)-1] == '\n' {
			text = text[:len(text)-1]
		}
		return fmt.Errorf("%s: %w", text, err)
	}
	return nil
}

func previewFile(cameraID int) string {
	return fmt.Sprintf("/tmp/dronebridge_preview_%d.jpg", cameraID)
}

func cameraSideReport(cameraID int) map[string]interface{} {
	out := map[string]interface{}{
		"camera_id": cameraID,
		"side":      "camera",
		"verdict":   "Camera chưa đẩy khung hình.",
	}
	cfg, err := loadAppConfig()
	if err != nil {
		out["verdict"] = "Không đọc được cấu hình camera trên máy."
		return out
	}
	if pub, err := camera.PublishPath(cfg, cameraID); err == nil {
		out["publish_path"] = pub
	}
	if st := camera.ReadStreamStats(cameraID, 0); st != nil && st.AgeSec < 15 && st.FramesSent == 0 && st.FPSWindow == 0 {
		out["verdict"] = "Cổng đã nhận cảm biến nhưng CSI không đưa khung hình."
		return out
	}
	info, err := os.Stat(previewFile(cameraID))
	if err != nil || time.Since(info.ModTime()) > 2*time.Second || info.Size() == 0 {
		return out
	}
	out["side"] = "ok"
	out["age_ms"] = time.Since(info.ModTime()).Milliseconds()
	if pub, ok := out["publish_path"].(string); ok && pub != "" {
		out["verdict"] = "Đúng khung hình camera đang đẩy tới " + pub + "."
	} else {
		out["verdict"] = "Đúng khung hình camera đang đẩy."
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func scheduleHostReboot(reason string) {
	go func() {
		time.Sleep(2 * time.Second)
		if err := runHostRebootScript(); err != nil {
			log.Printf("[WEB][CAMERA] host reboot request failed: %v", err)
		}
	}()
	log.Printf("[WEB][CAMERA] Scheduled host reboot (%s)", reason)
}

func StopCameraStreamersPublic() {
	camera.StopStreamers()
}

func BootstrapCameraStreams() ([]int, error) {
	cfg, err := loadAppConfig()
	if err != nil {
		return nil, err
	}
	return camera.StartStreamers(cfg)
}

func cameraStatusPayload() map[string]interface{} {
	pids := camera.ListStreamerPIDs()
	cfg, _ := loadAppConfig()
	endpoints := []map[string]interface{}{}
	if cfg != nil {
		m := camera.ResolveMediamtx(cfg)
		for _, s := range cfg.Camera.Streams {
			if !s.Enabled {
				continue
			}
			pub, err := camera.PublishPath(cfg, s.CameraID)
			if err != nil {
				continue
			}
			endpoints = append(endpoints, map[string]interface{}{
				"name":         s.Name,
				"camera_id":    s.CameraID,
				"stream_role":  camera.StreamRole(cfg, s.CameraID),
				"publish_path": pub,
				"rtsp":         camera.RTSPURL(m, pub),
				"webrtc_whep":  fmt.Sprintf("http://%s:%d%s/whep", m.Host, m.WebRTCPort, pub),
				"hls":          fmt.Sprintf("http://%s:%d%s/index.m3u8", m.Host, m.HLSPort, pub),
			})
		}
	}
	pid := 0
	if len(pids) > 0 {
		pid = pids[0]
	}
	streams := make([]map[string]interface{}, 0, len(pids))
	for _, p := range pids {
		streams = append(streams, map[string]interface{}{
			"pid":     p,
			"running": true,
		})
	}
	return map[string]interface{}{
		"running":   len(pids) > 0,
		"pid":       pid,
		"pids":      pids,
		"streams":   streams,
		"endpoints": endpoints,
		"landing":   camera.LandingStatusForConfig(cfg, 10*time.Second),
		"primary_camera_id": func() int {
			if cfg != nil {
				return cfg.Camera.PrimaryCameraID
			}
			return 0
		}(),
		"memory_tier_gb": int(camera.DetectMemoryTier()),
		"auto_memory_profile": func() bool {
			if cfg != nil {
				return cfg.Camera.AutoMemoryProfile
			}
			return false
		}(),
	}
}
