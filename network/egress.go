package network

import (
	"encoding/json"
	"os"
	"time"
)

const runtimeNetworkStatusFile = "/run/dronebridge/network_status.json"

func readNetworkStatus() (map[string]interface{}, error) {
	data, err := os.ReadFile(runtimeNetworkStatusFile)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// CloudReady reports whether netmon has a verified cloud uplink (PBR table 100 + probe).
// Chỉ đọc network_status.json — không tự đoán interface (INTERFACE_POLICY.md).
func CloudReady() bool {
	raw, err := readNetworkStatus()
	if err != nil {
		return false
	}
	ready, ok := raw["cloud_ready"].(bool)
	return ok && ready
}

// PhysicalEgress returns the active cloud uplink from netmon output.
func PhysicalEgress() (iface string, ip string, ok bool) {
	raw, err := readNetworkStatus()
	if err != nil {
		return "", "", false
	}

	if cu := cloudUplinkBlock(raw); cu != nil {
		if ready, _ := cu["ready"].(bool); ready {
			iface, _ = cu["interface"].(string)
			ip, _ = cu["ip"].(string)
			if iface != "" && ip != "" {
				return iface, ip, true
			}
		}
	}

	iface, _ = raw["active_interface"].(string)
	if iface == "" {
		return "", "", false
	}
	blockKey := ""
	switch iface {
	case "wwan0":
		blockKey = "4g"
	case "wlan0":
		blockKey = "wifi"
	default:
		return iface, "", false
	}
	block, _ := raw[blockKey].(map[string]interface{})
	if block == nil {
		return iface, "", false
	}
	online, _ := block["online"].(bool)
	for _, key := range []string{"ip", "ip_address"} {
		if v, _ := block[key].(string); v != "" && v != "null" {
			if online {
				return iface, v, true
			}
			return iface, v, false
		}
	}
	return iface, "", false
}

func cloudUplinkBlock(raw map[string]interface{}) map[string]interface{} {
	planes, _ := raw["planes"].(map[string]interface{})
	if planes == nil {
		return nil
	}
	cu, _ := planes["cloud_uplink"].(map[string]interface{})
	return cu
}

// WaitForCloudEgress blocks until netmon sets cloud_ready (auth phải sau PBR probe).
func WaitForCloudEgress(maxWait time.Duration) (iface string, ip string, ok bool) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if CloudReady() {
			return PhysicalEgress()
		}
		time.Sleep(2 * time.Second)
	}
	return "", "", false
}
