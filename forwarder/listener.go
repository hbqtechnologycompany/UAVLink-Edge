package forwarder

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bluenviron/gomavlib/v3"

	"DroneBridge/config"
	"DroneBridge/logger"
)

const (
	mavlinkPathEthernet = "ethernet"
	mavlinkPathSerial   = "serial"

	// Exported aliases for startup / web status reporting.
	MAVLinkPathEthernet = mavlinkPathEthernet
	MAVLinkPathSerial   = mavlinkPathSerial
)

// ensureEthernetReady configures the dedicated PX4 ethernet link when requested.
func ensureEthernetReady(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}

	connType := normalizeConnectionType(cfg.Network.ConnectionType)
	if connType != mavlinkPathEthernet && connType != "prefer_ethernet" {
		return nil
	}

	if cfg.Ethernet.LocalIP == "" && !cfg.Ethernet.AutoSetup {
		return nil
	}

	ifaceName, err := resolveEthernetInterface(cfg)
	if err != nil {
		return err
	}

	if err := bringInterfaceUp(ifaceName); err != nil {
		logger.Warn("[NETWORK] Failed to bring %s up: %v", ifaceName, err)
	}

	if cfg.Ethernet.LocalIP == "" {
		return nil
	}

	if !cfg.Ethernet.AutoSetup {
		logger.Info("[NETWORK] Ethernet interface %s expected at %s (auto_setup=false)", ifaceName, cfg.Ethernet.LocalIP)
		return nil
	}

	if err := setupInterfaceIP(ifaceName, cfg.Ethernet.LocalIP, cfg.Ethernet.Subnet); err != nil {
		return fmt.Errorf("ethernet auto_setup failed on %s: %w", ifaceName, err)
	}

	px4Port := cfg.Ethernet.PixhawkPort
	if px4Port <= 0 {
		px4Port = cfg.Network.LocalListenPort
	}
	logger.Info("[NETWORK] Ethernet ready on %s with IP %s/%s (PX4 %s:%d → CM5 :%d)",
		ifaceName, cfg.Ethernet.LocalIP, cfg.Ethernet.Subnet,
		cfg.Ethernet.PixhawkIP, px4Port, cfg.Network.LocalListenPort)
	return nil
}

func resolveEthernetInterface(cfg *config.Config) (string, error) {
	if cfg.Ethernet.Interface != "" {
		return cfg.Ethernet.Interface, nil
	}

	_, _, ifaceName, err := getEthernetIP(cfg)
	if err != nil {
		return "", err
	}
	if ifaceName == "" {
		return "", fmt.Errorf("no ethernet interface detected")
	}
	return ifaceName, nil
}

func bringInterfaceUp(ifaceName string) error {
	return runIPCommand("link", "set", "dev", ifaceName, "up")
}

func runIPCommand(args ...string) error {
	cmd := exec.Command("ip", args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	if os.Geteuid() != 0 {
		sudoArgs := append([]string{"ip"}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		return fmt.Errorf("%s: %v", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func normalizeConnectionType(connType string) string {
	switch strings.ToLower(strings.TrimSpace(connType)) {
	case "", "ethernet", "udp":
		return mavlinkPathEthernet
	case "serial", "uart":
		return mavlinkPathSerial
	case "prefer_ethernet", "dual", "auto":
		return "prefer_ethernet"
	default:
		return strings.ToLower(strings.TrimSpace(connType))
	}
}

func buildListenerEndpoints(cfg *config.Config) ([]gomavlib.EndpointConf, error) {
	connType := normalizeConnectionType(cfg.Network.ConnectionType)

	if err := ensureEthernetReady(cfg); err != nil {
		logger.Warn("[NETWORK] Ethernet setup skipped: %v", err)
	}

	telem2Dev, telem2Baud, telem3Dev, telem3Baud := configurePX4Links(cfg)
	endpoints := make([]gomavlib.EndpointConf, 0, 4)

	addUDP := func() error {
		pc, err := getOrCreatePixhawkUDP(cfg.Network.LocalListenPort, cfg)
		if err != nil {
			return fmt.Errorf("bind pixhawk UDP :%d: %w", cfg.Network.LocalListenPort, err)
		}
		endpoints = append(endpoints, gomavlib.EndpointCustom{ReadWriteCloser: pc})

		localEthIP, broadcastEthIP, ifaceName, ethErr := getEthernetIP(cfg)
		enableBroadcast := !cfg.VPN.Enabled && ethErr == nil && broadcastEthIP != ""
		if enableBroadcast {
			endpoints = append(endpoints, gomavlib.EndpointUDPBroadcast{
				BroadcastAddress: fmt.Sprintf("%s:%d", broadcastEthIP, cfg.Network.LocalListenPort),
			})
			logger.Info("[NETWORK] UDP Broadcast enabled on %s: Local=%s:%d, Broadcast=%s:%d",
				ifaceName, localEthIP, cfg.Network.LocalListenPort+1, broadcastEthIP, cfg.Network.LocalListenPort)
		} else {
			if cfg.VPN.Enabled {
				logger.Info("[NETWORK] UDP Broadcast disabled in VPN mode (fixed UDP :%d for Pixhawk)", cfg.Network.LocalListenPort)
			} else if ethErr != nil {
				logger.Warn("[NETWORK] UDP Broadcast disabled: %v", ethErr)
			} else {
				logger.Info("[NETWORK] UDP Broadcast disabled by configuration")
			}
			logger.Info("[NETWORK] Pixhawk listener on fixed UDP 0.0.0.0:%d (no ephemeral client)", cfg.Network.LocalListenPort)
		}
		return nil
	}

	addSerial := func(role, device string, baud int) error {
		if device == "" {
			return fmt.Errorf("%s device not configured", role)
		}
		if _, err := os.Stat(device); err != nil {
			return fmt.Errorf("%s device %s not available: %w", role, device, err)
		}
		if baud <= 0 {
			return fmt.Errorf("%s baud not configured", role)
		}
		endpoints = append(endpoints, gomavlib.EndpointSerial{
			Device: device,
			Baud:   baud,
		})
		logger.Info("[MAVLINK] %s listening on %s @ %d 8N1", role, device, baud)
		return nil
	}

	switch connType {
	case mavlinkPathEthernet:
		if err := addUDP(); err != nil {
			return nil, err
		}
	case mavlinkPathSerial:
		if err := addSerial("TELEM2", telem2Dev, telem2Baud); err != nil {
			return nil, err
		}
	case "prefer_ethernet":
		if err := addUDP(); err != nil {
			return nil, err
		}
		if err := addSerial("TELEM2", telem2Dev, telem2Baud); err != nil {
			logger.Warn("[MAVLINK] TELEM2 backup not opened: %v", err)
		}
		if err := addSerial("TELEM3", telem3Dev, telem3Baud); err != nil {
			logger.Warn("[MAVLINK] TELEM3 backup not opened: %v", err)
		}
	default:
		logger.Warn("[MAVLINK] Unknown connection_type %q - falling back to UDP listener", cfg.Network.ConnectionType)
		if err := addUDP(); err != nil {
			return nil, err
		}
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no MAVLink listener endpoints configured")
	}

	return endpoints, nil
}

// ClassifyMAVLinkPath reports whether a MAVLink channel is serial or UDP/ethernet.
func ClassifyMAVLinkPath(channel *gomavlib.Channel) string {
	return classifyMAVLinkPath(channel)
}

func classifyMAVLinkPath(channel *gomavlib.Channel) string {
	if channel == nil {
		return ""
	}

	label := strings.ToLower(strings.TrimSpace(channel.String()))
	switch {
	case strings.Contains(label, "serial"), label == "serial":
		return mavlinkPathSerial
	default:
		// UDP endpoints are labeled with host:port.
		return mavlinkPathEthernet
	}
}
