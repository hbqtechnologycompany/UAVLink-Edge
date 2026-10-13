package web

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"DroneBridge/metrics"
)

const runtimeNetworkStatusFile = "/run/dronebridge/network_status.json"
const dataNetworkStatusFile = "/opt/dronebridge/data/network_status.json"

// Ngưỡng MAVLink PX4 trên eth0 (CM5 HB 1Hz → PX4 ~40 msg/s khi stream đủ).
const (
	ethMavlinkStreamThreshold   = 15.0 // luồng telemetry đầy đủ
	ethMavlinkSymbolicThreshold = 1.0  // vài gói tượng trưng khi chưa stream
)

// ReadNetworkStatus loads network monitor output and normalizes it for the web UI.
// connection_manager.py writes {wifi:{online,ip}, 4g:{online,ip}, active_interface}.
// Legacy path data/connection_status.json used {wifi:{status,ip}, ...}.
func ReadNetworkStatus() map[string]interface{} {
	raw, path := readNetworkStatusRaw()
	if raw == nil {
		return defaultNetworkStatus()
	}

	out := map[string]interface{}{
		"source":              path,
		"timestamp":           raw["timestamp"],
		"active_interface":    raw["active_interface"],
		"pbr_table":           raw["pbr_table"],
		"ssh_route":           raw["ssh_route"],
		"cloud_ready":         raw["cloud_ready"],
		"cloud_signal_dbm":    raw["cloud_signal_dbm"],
		"cloud_signal_source": raw["cloud_signal_source"],
		"wifi":                normalizeIfaceBlock(raw["wifi"]),
		"4g":                  normalizeIfaceBlock(raw["4g"]),
		"cellular":            normalizeCellularBlock(raw["cellular"], raw["4g"]),
		"ethernet":            enrichEthernetMavlinkStatus(normalizeIfaceBlock(extractEthernetBlock(raw))),
		"network_type":        "",
		"network_speed":       "",
		"network_speed_bps":   float64(0),
	}

	if speed, ok := raw["bandwidth_bps"].(float64); ok && speed > 0 {
		out["network_speed_bps"] = speed
		out["network_speed"] = formatBandwidth(speed)
	} else if speedStr, ok := raw["bandwidth"].(string); ok && speedStr != "" {
		out["network_speed"] = speedStr
	}

	active, _ := out["active_interface"].(string)
	wifi := out["wifi"].(map[string]interface{})
	fourG := out["4g"].(map[string]interface{})
	cellular, _ := out["cellular"].(map[string]interface{})

	switch active {
	case "wwan0":
		out["network_type"] = cellularNetworkType(cellular, fourG)
		out["physical_egress_iface"] = "wwan0"
		out["physical_egress_ip"] = ifaceIP(fourG)
	case "wlan0":
		out["network_type"] = "WiFi"
		out["physical_egress_iface"] = "wlan0"
		out["physical_egress_ip"] = ifaceIP(wifi)
	default:
		switch {
		case ifaceConnected(fourG):
			out["network_type"] = cellularNetworkType(cellular, fourG)
			out["physical_egress_iface"] = "wwan0"
			out["physical_egress_ip"] = ifaceIP(fourG)
		case ifaceConnected(wifi):
			out["network_type"] = "WiFi"
			out["physical_egress_iface"] = "wlan0"
			out["physical_egress_ip"] = ifaceIP(wifi)
		case ifaceConnected(out["ethernet"]):
			out["network_type"] = "Ethernet"
		default:
			out["network_type"] = "Unknown"
		}
	}

	if out["network_speed"] == "" {
		if bps, ok := raw["mavlink_bps"].(float64); ok && bps > 0 {
			out["network_speed_bps"] = bps
			out["network_speed"] = formatBandwidth(bps)
		}
	}

	return out
}

func extractEthernetBlock(raw map[string]interface{}) interface{} {
	if eth, ok := raw["ethernet"]; ok && eth != nil {
		return eth
	}
	planes, ok := raw["planes"].(map[string]interface{})
	if !ok {
		return nil
	}
	ethPix, ok := planes["eth_pixhawk"].(map[string]interface{})
	if !ok || ethPix == nil {
		return nil
	}
	out := make(map[string]interface{}, len(ethPix)+1)
	for k, v := range ethPix {
		out[k] = v
	}
	out["role"] = "PX4 link"
	return out
}

func ethLinkUp(block map[string]interface{}) bool {
	if up, ok := block["link_up"].(bool); ok {
		return up
	}
	return ifaceIP(block) != ""
}

// enrichEthernetMavlinkStatus đánh giá eth0 theo MAVLink nhận từ PX4 (không chỉ có IP).
func enrichEthernetMavlinkStatus(eth map[string]interface{}) map[string]interface{} {
	if eth == nil {
		return map[string]interface{}{"status": "unavailable"}
	}
	if eth["role"] == nil {
		eth["role"] = "PX4 link"
	}

	accepted := float64(0)
	if snap := metrics.Global.GetSnapshot(); snap != nil {
		if v, ok := snap["udp_accepted_per_sec"].(float64); ok {
			accepted = v
		}
	}
	telem := GetTelemetrySnapshot()
	eth["mavlink_rx_per_sec"] = accepted

	if !ethLinkUp(eth) {
		eth["status"] = "unavailable"
		eth["mavlink_quality"] = "link_down"
		return eth
	}

	switch {
	case accepted >= ethMavlinkStreamThreshold:
		eth["status"] = "connected"
		eth["mavlink_quality"] = "stream"
	case accepted >= ethMavlinkSymbolicThreshold:
		eth["status"] = "degraded"
		eth["mavlink_quality"] = "symbolic"
	case telem.Connected:
		eth["status"] = "available"
		eth["mavlink_quality"] = "heartbeat_only"
	default:
		eth["status"] = "waiting"
		eth["mavlink_quality"] = "no_rx"
	}
	return eth
}

func readNetworkStatusRaw() (map[string]interface{}, string) {
	paths := []string{
		runtimeNetworkStatusFile,
		dataNetworkStatusFile,
		projectPath("data", "connection_status.json"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		return raw, path
	}
	return nil, ""
}

func defaultNetworkStatus() map[string]interface{} {
	return map[string]interface{}{
		"4g":               map[string]interface{}{"status": "unavailable"},
		"cellular":         map[string]interface{}{"status": "unavailable"},
		"wifi":             map[string]interface{}{"status": "unavailable"},
		"ethernet":         map[string]interface{}{"status": "unavailable"},
		"active_interface": nil,
		"network_type":     "Unknown",
		"network_speed":    "N/A",
		"timestamp":        0,
	}
}

func normalizeCellularBlock(cellular interface{}, legacyFourG interface{}) map[string]interface{} {
	block, ok := cellular.(map[string]interface{})
	if !ok || block == nil {
		block = map[string]interface{}{}
	}
	legacy := normalizeIfaceBlock(legacyFourG)
	for k, v := range legacy {
		if _, exists := block[k]; !exists {
			block[k] = v
		}
	}
	if block["driver"] == nil {
		block["driver"] = "sim7600_qmi"
	}
	if block["generation"] == nil {
		block["generation"] = 4
	}
	if block["radio_access"] == nil {
		block["radio_access"] = "LTE"
	}
	if _, ok := block["status"]; !ok {
		block = normalizeIfaceBlock(block)
	}
	return block
}

func cellularNetworkType(cellular, fourG map[string]interface{}) string {
	generation := 4
	if v, ok := cellular["generation"].(float64); ok && v >= 5 {
		generation = int(v)
	} else if v, ok := cellular["generation"].(int); ok && v >= 5 {
		generation = v
	}
	radio, _ := cellular["radio_access"].(string)
	if generation >= 5 || strings.EqualFold(radio, "NR") || strings.Contains(strings.ToUpper(radio), "5G") {
		return "5G/NR"
	}
	if ifaceConnected(fourG) {
		return "4G/LTE"
	}
	return "Cellular"
}

func normalizeIfaceBlock(v interface{}) map[string]interface{} {
	block, ok := v.(map[string]interface{})
	if !ok || block == nil {
		return map[string]interface{}{"status": "unavailable"}
	}

	out := make(map[string]interface{}, len(block)+2)
	for k, val := range block {
		out[k] = val
	}

	if status, ok := out["status"].(string); ok && status != "" {
		return out
	}

	if online, ok := out["online"].(bool); ok {
		if online {
			out["status"] = "connected"
		} else {
			out["status"] = "unavailable"
		}
		return out
	}

	if ip := ifaceIP(out); ip != "" {
		out["status"] = "connected"
	} else {
		out["status"] = "unavailable"
	}
	return out
}

func ifaceConnected(v interface{}) bool {
	block, ok := v.(map[string]interface{})
	if !ok || block == nil {
		return false
	}
	if status, ok := block["status"].(string); ok {
		return status == "connected" || status == "available" || status == "degraded"
	}
	if online, ok := block["online"].(bool); ok {
		return online
	}
	return ifaceIP(block) != ""
}

func ifaceIP(block map[string]interface{}) string {
	for _, key := range []string{"ip", "ip_address"} {
		if ip, ok := block[key].(string); ok && ip != "" && ip != "null" {
			return ip
		}
	}
	return ""
}

func formatBandwidth(bps float64) string {
	switch {
	case bps >= 1_000_000_000:
		return fmt.Sprintf("%.2f Gbps", bps/1_000_000_000)
	case bps >= 1_000_000:
		return fmt.Sprintf("%.2f Mbps", bps/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.2f Kbps", bps/1_000)
	default:
		return fmt.Sprintf("%.0f bps", bps)
	}
}

// NetworkStatusSummary returns dashboard-friendly fields from the runtime file.
func NetworkStatusSummary() (networkType, networkSpeed string) {
	info := ReadNetworkStatus()
	networkType, _ = info["network_type"].(string)
	networkSpeed, _ = info["network_speed"].(string)
	if networkType == "" {
		networkType = "Unknown"
	}
	if networkSpeed == "" {
		networkSpeed = "N/A"
	}
	return networkType, networkSpeed
}

func mergeNetworkIntoSnapshot(snap map[string]interface{}) {
	netInfo := ReadNetworkStatus()
	netType, _ := netInfo["network_type"].(string)
	netSpeed, _ := netInfo["network_speed"].(string)
	if netType == "" {
		netType = "Unknown"
	}
	if netSpeed == "" {
		netSpeed = "N/A"
	}
	snap["network_type"] = netType
	snap["network_speed"] = netSpeed
	if v, ok := netInfo["physical_egress_iface"].(string); ok && v != "" {
		snap["physical_egress_iface"] = v
	}
	if v, ok := netInfo["physical_egress_ip"].(string); ok && v != "" {
		snap["physical_egress_ip"] = v
	}
	if v, ok := netInfo["active_interface"].(string); ok && v != "" {
		snap["active_interface"] = v
	}

	linkName, linkAge := PX4LinkStatus()
	snap["px4_link"] = linkName
	snap["px4_link_hb_age_ms"] = linkAge

	telem := GetTelemetrySnapshot()
	snap["pixhawk_connected"] = telem.Connected
	snap["telemetry_valid"] = telem.Valid
	snap["satellites"] = telem.Satellites
	snap["gps_fix"] = telem.GpsFix
	snap["gps_lock"] = telem.GpsLock
	snap["gps_valid"] = telem.GpsFix >= 3 && telem.Satellites > 0
	if telem.Valid {
		snap["flight_mode"] = telem.FlightMode
		snap["battery_pct"] = telem.BatteryPct
		snap["altitude_m"] = telem.AltitudeM
		snap["speed_ms"] = telem.SpeedMs
		if telem.LatitudeDeg != 0 || telem.LongitudeDeg != 0 {
			snap["latitude_deg"] = telem.LatitudeDeg
			snap["longitude_deg"] = telem.LongitudeDeg
		}
		if telem.Hdop > 0 {
			snap["hdop"] = telem.Hdop
		}
		if telem.HeadingDeg != 0 {
			snap["heading_deg"] = telem.HeadingDeg
		}
	}

	msgRate, _ := snap["udp_msg_per_sec"].(float64)
	byteRate, _ := snap["udp_bytes_per_sec"].(float64)
	if msgRate > 0 {
		snap["mavlink_uplink_msg_per_sec"] = msgRate
	}
	snap["mavlink_uplink_bytes_per_sec"] = byteRate
	if byteRate <= 0 && msgRate > 0 {
		est := msgRate * 120
		snap["mavlink_uplink_bytes_per_sec"] = est
		if netSpeed == "" || netSpeed == "N/A" {
			snap["network_speed"] = formatBandwidth(est)
		}
	}

	if cfg, err := loadAppConfig(); err == nil && cfg != nil {
		snap["mavlink_server_target"] = cfg.GetAddress()
	}
}
