package web

import "DroneBridge/config"

func applyNetworkConfigUpdates(cfg *config.Config, req map[string]interface{}) {
	if connType, ok := req["connection_type"].(string); ok {
		cfg.Network.ConnectionType = connType
	}
	if serialPort, ok := req["serial_port"].(string); ok {
		cfg.Network.SerialPort = serialPort
	}
	if serialBaud, ok := req["serial_baud"].(float64); ok {
		cfg.Network.SerialBaud = int(serialBaud)
	}
	if localPort, ok := req["local_listen_port"].(float64); ok {
		cfg.Network.LocalListenPort = int(localPort)
	}
}

func applyEthernetConfigUpdates(cfg *config.Config, req map[string]interface{}) {
	if v, ok := req["interface"].(string); ok {
		cfg.Ethernet.Interface = v
	}
	if v, ok := req["local_ip"].(string); ok {
		cfg.Ethernet.LocalIP = v
	}
	if v, ok := req["broadcast_ip"].(string); ok {
		cfg.Ethernet.BroadcastIP = v
	}
	if v, ok := req["pixhawk_ip"].(string); ok {
		cfg.Ethernet.PixhawkIP = v
	}
	if v, ok := req["pixhawk_port"].(float64); ok {
		cfg.Ethernet.PixhawkPort = int(v)
	}
	if v, ok := req["subnet"].(string); ok {
		cfg.Ethernet.Subnet = v
	}
	if v, ok := req["auto_setup"].(bool); ok {
		cfg.Ethernet.AutoSetup = v
	}
	if v, ok := req["allow_missing_pixhawk"].(bool); ok {
		cfg.Ethernet.AllowMissingPixhawk = v
	}
	if v, ok := req["pixhawk_connection_timeout"].(float64); ok {
		cfg.Ethernet.PixhawkConnectionTimeout = int(v)
	}
}

func applyLCDConfigUpdates(cfg *config.Config, req map[string]interface{}) {
	if v, ok := req["enabled"].(bool); ok {
		cfg.LCD.Enabled = v
	}
	if v, ok := req["auto_setup"].(bool); ok {
		cfg.LCD.AutoSetup = v
	}
	if v, ok := req["overlay"].(string); ok {
		cfg.LCD.Overlay = v
	}
	if v, ok := req["pins"].(string); ok {
		cfg.LCD.Pins = v
	}
	if v, ok := req["bus"].(float64); ok {
		cfg.LCD.Bus = int(v)
	}
	if v, ok := req["address"].(float64); ok {
		cfg.LCD.Address = int(v)
	}
	if v, ok := req["interval"].(float64); ok {
		cfg.LCD.Interval = int(v)
	}
	if v, ok := req["screen_hold"].(float64); ok {
		cfg.LCD.ScreenHold = int(v)
	}
	if v, ok := req["screen"].(string); ok {
		cfg.LCD.Screen = v
	}
}

func applyHardwareConfigUpdates(cfg *config.Config, req map[string]interface{}) {
	if net, ok := req["network"].(map[string]interface{}); ok {
		applyNetworkConfigUpdates(cfg, net)
	}
	applyNetworkConfigUpdates(cfg, req)

	if eth, ok := req["ethernet"].(map[string]interface{}); ok {
		applyEthernetConfigUpdates(cfg, eth)
	}
	applyEthernetConfigUpdates(cfg, req)

	if lcd, ok := req["lcd"].(map[string]interface{}); ok {
		applyLCDConfigUpdates(cfg, lcd)
	}
}
