package web

import (
	"log"
	"os/exec"
	"strings"
	"time"

	"DroneBridge/config"
)

var validNetworkModes = map[string]struct{}{
	"prefer_4g": {},
	"4g_only":   {},
	"wifi_only": {},
}

func isValidNetworkMode(mode string) bool {
	_, ok := validNetworkModes[mode]
	return ok
}

func networkModeToLegacyPriority(mode string) string {
	if mode == "wifi_only" {
		return "wifi"
	}
	return "4g"
}

func legacyPriorityToMode(priority string) string {
	if strings.ToLower(strings.TrimSpace(priority)) == "wifi" {
		return "wifi_only"
	}
	return "prefer_4g"
}

func switchTargetToMode(target string) string {
	if strings.ToLower(strings.TrimSpace(target)) == "wifi" {
		return "wifi_only"
	}
	return "prefer_4g"
}

func cloudWifiFallbackEnabled(cfg *config.Config) bool {
	if cfg.Network.CloudWifiFallback != nil {
		return *cfg.Network.CloudWifiFallback
	}
	return true
}

func buildNetworkModePayload(cfg *config.Config) map[string]interface{} {
	status := ReadNetworkStatus()
	activeIface, _ := status["active_interface"].(string)
	routeReason := ""
	if raw, path := readNetworkStatusRaw(); raw != nil {
		if rr, ok := raw["route_reason"].(string); ok {
			routeReason = rr
		}
		if nm, ok := raw["network_mode"].(string); ok && nm != "" && cfg.Network.Mode == "" {
			_ = path
		}
	}

	mode := cfg.Network.Mode
	if mode == "" {
		mode = "prefer_4g"
	}

	return map[string]interface{}{
		"success":              true,
		"mode":                 mode,
		"priority":             networkModeToLegacyPriority(mode),
		"cloud_wifi_fallback":  cloudWifiFallbackEnabled(cfg),
		"fallback_delay":       cfg.Network.FallbackDelay,
		"active_interface":     activeIface,
		"route_reason":         routeReason,
		"network_type":         status["network_type"],
		"physical_egress_iface": status["physical_egress_iface"],
		"physical_egress_ip":   status["physical_egress_ip"],
	}
}

func applyNetworkMode(mode string, cloudFallback *bool, fallbackDelay *int) (*config.Config, error) {
	if !isValidNetworkMode(mode) {
		return nil, errInvalidNetworkMode
	}

	cfg, err := loadAppConfig()
	if err != nil {
		return nil, err
	}

	cfg.Network.Mode = mode
	if cloudFallback != nil {
		v := *cloudFallback
		cfg.Network.CloudWifiFallback = &v
	}
	if fallbackDelay != nil && *fallbackDelay >= 0 {
		cfg.Network.FallbackDelay = *fallbackDelay
	}

	if err := saveAppConfig(cfg); err != nil {
		return nil, err
	}

	triggerNetmonApply(mode)
	return cfg, nil
}

var errInvalidNetworkMode = &networkModeError{msg: "Invalid mode. Use: prefer_4g, 4g_only, wifi_only"}

type networkModeError struct{ msg string }

func (e *networkModeError) Error() string { return e.msg }

func triggerNetmonApply(mode string) {
	go func() {
		// Netmon đọc config.yaml mỗi chu kỳ; restart áp dụng ngay nếu có quyền sudo.
		if err := exec.Command("sudo", "-n", "systemctl", "restart", "dronebridge-netmon.service").Run(); err == nil {
			log.Printf("[WEB][NET] network.mode=%s → netmon restarted", mode)
			return
		}

		time.Sleep(200 * time.Millisecond)
		cmd := exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "once")
		if err := cmd.Run(); err != nil {
			log.Printf("[WEB][NET] network.mode=%s saved; netmon will apply within ~30s (once/restart failed: %v)", mode, err)
			return
		}
		log.Printf("[WEB][NET] network.mode=%s → connection_manager once OK", mode)
	}()
}
