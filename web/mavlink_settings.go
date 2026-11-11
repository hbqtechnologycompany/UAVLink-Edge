package web

import (
	"net"
	"regexp"
	"strconv"
	"strings"

	"DroneBridge/config"
)

var ifaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// mavlinkSettingsView là dữ liệu trang MAVLink / Pixhawk đọc từ /api/config/get.
// serial_port là cổng TELEM2 thật sự được dùng (telem2_port thắng serial_port như px4_link).
func mavlinkSettingsView(cfg *config.Config) map[string]interface{} {
	n, e := cfg.Network, cfg.Ethernet
	serial := n.Telem2Port
	if serial == "" {
		serial = n.SerialPort
	}
	baud := n.Telem2Baud
	if baud <= 0 {
		baud = n.SerialBaud
	}
	return map[string]interface{}{
		"network": map[string]interface{}{
			"connection_type":   n.ConnectionType,
			"local_listen_port": n.LocalListenPort,
			"target_port":       n.TargetPort,
			"serial_port":       serial,
			"serial_baud":       baud,
			"telem3_port":       n.Telem3Port,
		},
		"ethernet": map[string]interface{}{
			"interface":    e.Interface,
			"local_ip":     e.LocalIP,
			"broadcast_ip": e.BroadcastIP,
			"pixhawk_ip":   e.PixhawkIP,
			"pixhawk_port": e.PixhawkPort,
			"subnet":       e.Subnet,
		},
	}
}

// applyEthernetSettings ghi các trường Ethernet từ trang MAVLink; giá trị sai định dạng bị bỏ qua.
func applyEthernetSettings(cfg *config.Config, req map[string]interface{}) {
	str := func(key string) (string, bool) {
		v, ok := req[key].(string)
		return strings.TrimSpace(v), ok
	}
	ip := func(key string, dst *string) {
		if v, ok := str(key); ok && (v == "" || net.ParseIP(v) != nil) {
			*dst = v
		}
	}
	e := &cfg.Ethernet
	ip("local_ip", &e.LocalIP)
	ip("pixhawk_ip", &e.PixhawkIP)
	ip("broadcast_ip", &e.BroadcastIP)
	if v, ok := str("interface"); ok && (v == "" || ifaceNamePattern.MatchString(v)) {
		e.Interface = v
	}
	if v, ok := str("subnet"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 32 {
			e.Subnet = v
		}
	}
	if v, ok := req["pixhawk_port"].(float64); ok && v > 0 && v < 65536 {
		e.PixhawkPort = int(v)
	}
}
