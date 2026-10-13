package web

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"DroneBridge/config"
)

type cellularDriverInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Generation  int    `json:"generation"`
	Interface   string `json:"interface_hint"`
	Description string `json:"description"`
}

var cellularDriverCatalog = []cellularDriverInfo{
	{
		ID:          "sim7600_qmi",
		Label:       "SIMCom SIM7600 (4G USB)",
		Generation:  4,
		Interface:   "wwan0",
		Description: "Module 4G USB QMI — /dev/cdc-wdm0",
	},
	{
		ID:          "sim8262_pcie",
		Label:       "SIMCom SIM8262-E (5G PCIe)",
		Generation:  5,
		Interface:   "wwan0",
		Description: "Module 5G M.2 PCIe MHI — /dev/wwan0qmi0",
	},
}

var validCellularDrivers = func() map[string]cellularDriverInfo {
	m := make(map[string]cellularDriverInfo, len(cellularDriverCatalog))
	for _, d := range cellularDriverCatalog {
		m[d.ID] = d
	}
	return m
}()

func normalizeCellularConfig(cfg *config.Config) {
	if cfg.Cellular.Driver == "" {
		cfg.Cellular.Driver = "sim7600_qmi"
	}
	if cfg.Cellular.Iface == "" {
		cfg.Cellular.Iface = "wwan0"
	}
	if cfg.Cellular.Sim7600 == nil {
		cfg.Cellular.Sim7600 = map[string]any{"gpio_power": true}
	}
	if cfg.Cellular.Sim8262 == nil {
		cfg.Cellular.Sim8262 = map[string]any{
			"parent_iface": "mhi_hwip0",
			"data_iface":   "rmnet0",
			"rat_profile":  "stability",
		}
	}
}

func readModemRuntimeState() map[string]interface{} {
	paths := []string{
		"/run/dronebridge/modem_net.json",
		projectPath("data", "modem_net.json"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err == nil {
			raw["source"] = path
			return raw
		}
	}
	return map[string]interface{}{}
}

func buildCellularModulePayload(cfg *config.Config) map[string]interface{} {
	normalizeCellularConfig(cfg)
	modem := readModemRuntimeState()
	netStatus := ReadNetworkStatus()

	cellBlock, _ := netStatus["cellular"].(map[string]interface{})
	if cellBlock == nil {
		cellBlock = map[string]interface{}{}
	}

	driver := cfg.Cellular.Driver
	if v, ok := modem["driver"].(string); ok && v != "" {
		driver = v
	} else if v, ok := cellBlock["driver"].(string); ok && v != "" {
		driver = v
	}

	info := validCellularDrivers[driver]
	generation := info.Generation
	if g, ok := modem["generation"].(float64); ok && g > 0 {
		generation = int(g)
	} else if g, ok := cellBlock["generation"].(float64); ok && g > 0 {
		generation = int(g)
	}

	radioAccess := "LTE"
	if v, ok := modem["radio_access"].(string); ok && v != "" {
		radioAccess = v
	} else if v, ok := cellBlock["radio_access"].(string); ok && v != "" {
		radioAccess = v
	}

	ratProfile := "stability"
	if cfg.Cellular.Sim8262 != nil {
		if v, ok := cfg.Cellular.Sim8262["rat_profile"].(string); ok && v != "" {
			ratProfile = v
		}
	}

	return map[string]interface{}{
		"success":       true,
		"enabled":       cfg.Cellular.Enabled,
		"driver":        driver,
		"driver_label":  info.Label,
		"generation":    generation,
		"radio_access":  radioAccess,
		"iface":         cfg.Cellular.Iface,
		"apn":           cfg.Cellular.APN,
		"rat_profile":   ratProfile,
		"modem_ready":   modem["ready"],
		"modem_ip":      modem["ip"],
		"modem_phase":   modem["phase"],
		"drivers":       cellularDriverCatalog,
		"runtime":       modem,
		"cellular_link": cellBlock,
	}
}

func applyCellularModule(driver, apn, ratProfile string) (*config.Config, bool, error) {
	driver = strings.TrimSpace(driver)
	if driver == "" {
		return nil, false, errInvalidCellularDriver
	}
	if _, ok := validCellularDrivers[driver]; !ok {
		return nil, false, errInvalidCellularDriver
	}

	cfg, err := loadAppConfig()
	if err != nil {
		return nil, false, err
	}

	changed := cfg.Cellular.Driver != driver
	if apn != "" && cfg.Cellular.APN != apn {
		cfg.Cellular.APN = apn
		changed = true
	}
	if driver == "sim8262_pcie" && ratProfile != "" {
		normalizeCellularConfig(cfg)
		old := ""
		if v, ok := cfg.Cellular.Sim8262["rat_profile"].(string); ok {
			old = v
		}
		if old != ratProfile {
			cfg.Cellular.Sim8262["rat_profile"] = ratProfile
			changed = true
		}
	}

	if cfg.Cellular.Driver != driver {
		cfg.Cellular.Driver = driver
		cfg.Cellular.Enabled = true
		changed = true
	}

	if !changed {
		return cfg, false, nil
	}

	if err := saveAppConfig(cfg); err != nil {
		return nil, false, err
	}

	triggerCellularModuleApply(driver)
	return cfg, true, nil
}

var errInvalidCellularDriver = &cellularModuleError{msg: "Invalid driver. Use: sim7600_qmi, sim8262_pcie"}

type cellularModuleError struct{ msg string }

func (e *cellularModuleError) Error() string { return e.msg }

func triggerCellularModuleApply(driver string) {
	go func() {
		if err := exec.Command("sudo", "-n", "systemctl", "restart", "dronebridge-4g-init.service").Run(); err == nil {
			log.Printf("[WEB][CELL] cellular.driver=%s → modem init restarted", driver)
			time.Sleep(2 * time.Second)
			_ = exec.Command("sudo", "-n", "systemctl", "restart", "dronebridge-netmon.service").Run()
			return
		}

		cmd := exec.Command("python3", projectPath("Module_Cellular", "modem_init.py"))
		if err := cmd.Run(); err != nil {
			log.Printf("[WEB][CELL] cellular.driver=%s saved; modem init failed: %v", driver, err)
			return
		}
		_ = exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "once").Run()
		log.Printf("[WEB][CELL] cellular.driver=%s → modem_init.py OK", driver)
	}()
}

func registerCellularModuleHandlers() {
	http.HandleFunc("/api/network/cellular/module", handleCellularModule)
	http.HandleFunc("/api/network/4g/module", handleCellularModule) // legacy alias
}

func handleCellularModule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	if r.Method == http.MethodOptions {
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := loadAppConfig()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to load config: "+err.Error())
			return
		}
		json.NewEncoder(w).Encode(buildCellularModulePayload(cfg))

	case http.MethodPost:
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		driver, _ := req["driver"].(string)
		apn, _ := req["apn"].(string)
		ratProfile, _ := req["rat_profile"].(string)

		cfg, applied, err := applyCellularModule(driver, strings.TrimSpace(apn), strings.TrimSpace(ratProfile))
		if err != nil {
			if err == errInvalidCellularDriver {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		payload := buildCellularModulePayload(cfg)
		payload["applied"] = applied
		if applied {
			payload["message"] = "Cellular module updated — modem reinitializing"
		} else {
			payload["message"] = "No change"
		}
		json.NewEncoder(w).Encode(payload)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Method not allowed",
		})
	}
}

func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": message,
	})
}
