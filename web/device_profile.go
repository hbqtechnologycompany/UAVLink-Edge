//go:build ignore

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var droneNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func deviceProfilePath() string {
	return projectPath("data", "device_profile.json")
}

func defaultDeviceNameFromConfig() string {
	cfg, err := loadAppConfig()
	if err == nil && strings.TrimSpace(cfg.Device.Name) != "" {
		return strings.TrimSpace(cfg.Device.Name)
	}
	return "DroneBridge"
}

func readDeviceProfileFile() (string, bool) {
	data, err := os.ReadFile(deviceProfilePath())
	if err != nil {
		return "", false
	}
	var doc map[string]interface{}
	if json.Unmarshal(data, &doc) != nil {
		return "", false
	}
	name, _ := doc["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}

// GetDeviceName returns dashboard display name (JSON override, then config.yaml).
func GetDeviceName() string {
	if name, ok := readDeviceProfileFile(); ok {
		return name
	}
	return defaultDeviceNameFromConfig()
}

func deviceUUID() string {
	cfg, err := loadAppConfig()
	if err != nil || strings.TrimSpace(cfg.Auth.UUID) == "" {
		return ""
	}
	return strings.TrimSpace(cfg.Auth.UUID)
}

func systemHostname() string {
	out, err := exec.Command("hostname").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func validateDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if !droneNamePattern.MatchString(name) {
		return "", fmt.Errorf("use 1–64 chars: letters, digits, . _ - (must start with letter or digit)")
	}
	return name, nil
}

func saveDeviceProfile(name string) error {
	path := deviceProfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	doc := map[string]interface{}{
		"name":       name,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func deviceProfileResponse() map[string]interface{} {
	uuid := deviceUUID()
	shortUUID := uuid
	if len(shortUUID) > 8 {
		shortUUID = shortUUID[:8] + "…"
	}
	return map[string]interface{}{
		"name":         GetDeviceName(),
		"uuid":         uuid,
		"uuid_short":   shortUUID,
		"hostname":     systemHostname(),
		"config_default": defaultDeviceNameFromConfig(),
	}
}

func mergeDeviceIntoSnapshot(snap map[string]interface{}) {
	if snap == nil {
		return
	}
	snap["drone_name"] = GetDeviceName()
	snap["drone_uuid"] = deviceUUID()
	snap["hostname"] = systemHostname()
}
