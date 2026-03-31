package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Log      LogConfig      `yaml:"log"`
	Auth     AuthConfig     `yaml:"auth"`
	Network  NetworkConfig  `yaml:"network"`
	Ethernet EthernetConfig `yaml:"ethernet"`
	Web      WebConfig      `yaml:"web"`
}

// LogConfig contains logging settings
type LogConfig struct {
	Level           string `yaml:"level"`            // debug, info, warn, error
	Verbose         bool   `yaml:"verbose"`          // Enable verbose parsing of received messages
	TimestampFormat string `yaml:"timestamp_format"` // "time" or "unix"
	ServerConnectionOnly bool `yaml:"server_connection_only"`  // Show only server connection logs
	ShowWebInteractionLogs bool `yaml:"show_web_interaction_logs"` // Show logs/errors for local web interactions (:8080)
	ShowPacketStats     bool `yaml:"show_packet_stats"`      // Show periodic packet statistics (MSGID/PKT/BANDWIDTH)
}

// EthernetConfig contains ethernet interface settings for Pixhawk connection
type EthernetConfig struct {
	Interface                string `yaml:"interface"`                  // Interface name (eth0, end0, etc.) - empty for auto-detect
	LocalIP                  string `yaml:"local_ip"`                   // Local IP - empty for auto-detect
	BroadcastIP              string `yaml:"broadcast_ip"`               // Broadcast IP - empty for auto-detect
	PixhawkIP                string `yaml:"pixhawk_ip"`                 // Pixhawk IP address for filtering
	AutoSetup                bool   `yaml:"auto_setup"`                 // Auto configure IP if not set
	Subnet                   string `yaml:"subnet"`                     // Subnet mask (e.g., "24" for /24)
	AllowMissingPixhawk      bool   `yaml:"allow_missing_pixhawk"`      // DEBUG: Allow auth without Pixhawk connection (for testing)
	PixhawkConnectionTimeout int    `yaml:"pixhawk_connection_timeout"` // Timeout in seconds to wait for Pixhawk connection (default: 30s)
}

// AuthConfig contains authentication settings
type AuthConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	UUID         string `yaml:"uuid"`          // Drone UUID from drones_v2.id
	SharedSecret string `yaml:"shared_secret"` // Shared secret for registration (REPLACES Secret)
	// Secret field removed - secret key is now stored in .drone_secret file
	KeepaliveInterval         int     `yaml:"keepalive_interval"`          // seconds
	SessionHeartbeatFrequency float64 `yaml:"session_heartbeat_frequency"` // Hz
}

// NetworkConfig contains network settings
type NetworkConfig struct {
	LocalListenPort int    `yaml:"local_listen_port"`
	TargetHost      string `yaml:"target_host"`
	TargetPort      int    `yaml:"target_port"`
	Protocol        string `yaml:"protocol"`
	ConnectionType  string `yaml:"connection_type"` // "serial" or "ethernet" (default: "ethernet")
	SerialPort      string `yaml:"serial_port"`     // e.g., "/dev/ttyUSB0" or "/dev/ttyACM0"
	SerialBaud      int    `yaml:"serial_baud"`     // e.g., 57600, 115200
	Mode            string `yaml:"mode"`            // "4g_only", "wifi_only", "prefer_4g"
	FallbackDelay   int    `yaml:"fallback_delay"`  // seconds to wait before falling back to WiFi (default: 300)
}

// WebConfig contains web server settings
type WebConfig struct {
	Port int `yaml:"port"`
}

// Config represents the application configuration

// FrequencyConfig contains message sending frequencies in Hz
type FrequencyConfig struct {
	Heartbeat      float64 `yaml:"heartbeat"`
	Attitude       float64 `yaml:"attitude"`
	GlobalPosition float64 `yaml:"global_position"`
	GPSRaw         float64 `yaml:"gps_raw"`
	VFRHUD         float64 `yaml:"vfr_hud"`
	SysStatus      float64 `yaml:"sys_status"`
}

// PositionConfig contains initial position data
type PositionConfig struct {
	Latitude         float64 `yaml:"latitude"`
	Longitude        float64 `yaml:"longitude"`
	Altitude         float64 `yaml:"altitude"`
	RelativeAltitude float64 `yaml:"relative_altitude"`
}

// MovementConfig contains movement simulation settings
type MovementConfig struct {
	Enabled       bool    `yaml:"enabled"`
	Speed         float64 `yaml:"speed"`
	HeadingChange float64 `yaml:"heading_change"`
}

// VehicleConfig contains vehicle state information
type VehicleConfig struct {
	Armed            bool    `yaml:"armed"`
	CustomMode       uint32  `yaml:"custom_mode"`
	BatteryVoltage   float64 `yaml:"battery_voltage"`
	BatteryRemaining int     `yaml:"battery_remaining"`
}

// SimulationConfig contains simulation-specific configuration
type SimulationConfig struct {
	Frequencies     FrequencyConfig `yaml:"frequencies"`
	Position        PositionConfig  `yaml:"position"`
	Movement        MovementConfig  `yaml:"movement"`
	Vehicle         VehicleConfig   `yaml:"vehicle"`
	UpdateFrequency float64         `yaml:"update_frequency"`
	IPCheckInterval float64         `yaml:"ip_check_interval"`
	SysStatus       SysStatusConfig `yaml:"sys_status"`
	GPSRaw          GPSRawConfig    `yaml:"gps_raw"`
	VFRHUD          VFRHUDConfig    `yaml:"vfr_hud"`
}

// SysStatusConfig contains SYS_STATUS message parameters
type SysStatusConfig struct {
	Load           int    `yaml:"load"`
	CurrentBattery int    `yaml:"current_battery"`
	DropRateComm   uint16 `yaml:"drop_rate_comm"`
	ErrorsComm     uint16 `yaml:"errors_comm"`
	ErrorsCount1   uint16 `yaml:"errors_count1"`
	ErrorsCount2   uint16 `yaml:"errors_count2"`
	ErrorsCount3   uint16 `yaml:"errors_count3"`
	ErrorsCount4   uint16 `yaml:"errors_count4"`
}

// GPSRawConfig contains GPS_RAW_INT message parameters
type GPSRawConfig struct {
	Eph               uint16 `yaml:"eph"`
	Epv               uint16 `yaml:"epv"`
	SatellitesVisible uint8  `yaml:"satellites_visible"`
}

// VFRHUDConfig contains VFR_HUD message parameters
type VFRHUDConfig struct {
	Throttle uint16 `yaml:"throttle"`
}

// Load reads configuration from a YAML file
func Load(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Preserve legacy behavior: if show_packet_stats is omitted, keep stats visible.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err == nil {
		showPacketStatsSet := false
		if logSection, ok := raw["log"].(map[string]any); ok {
			_, showPacketStatsSet = logSection["show_packet_stats"]
		}
		if !showPacketStatsSet {
			cfg.Log.ShowPacketStats = true
		}
	}

	// Set defaults
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Ethernet.Subnet == "" {
		cfg.Ethernet.Subnet = "24"
	}
	if cfg.Ethernet.PixhawkConnectionTimeout <= 0 {
		cfg.Ethernet.PixhawkConnectionTimeout = 30 // Default 30 seconds
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// LoadSimulation reads simulation configuration from a YAML file
func LoadSimulation(filename string) (*SimulationConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read simulation config file: %w", err)
	}

	var cfg SimulationConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse simulation config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid simulation configuration: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Auth.Enabled {
		if c.Auth.Host == "" {
			return fmt.Errorf("auth.host cannot be empty when auth is enabled")
		}
		if c.Auth.Port <= 0 || c.Auth.Port > 65535 {
			return fmt.Errorf("auth.port must be between 1 and 65535")
		}
		// UUID and SharedSecret can be empty if drone is already registered (read from .drone_secret)
		// Or UUID/SharedSecret must be present for first-time registration
		if c.Auth.KeepaliveInterval <= 0 {
			return fmt.Errorf("auth.keepalive_interval must be greater than 0 when auth is enabled")
		}
		if c.Auth.SessionHeartbeatFrequency < 0 {
			return fmt.Errorf("auth.session_heartbeat_frequency must be >= 0 when auth is enabled")
		}
	}
	if c.Network.LocalListenPort <= 0 || c.Network.LocalListenPort > 65535 {
		return fmt.Errorf("local_listen_port must be between 1 and 65535")
	}
	if c.Network.TargetHost == "" {
		return fmt.Errorf("target_host cannot be empty")
	}
	if c.Network.TargetPort <= 0 || c.Network.TargetPort > 65535 {
		return fmt.Errorf("target_port must be between 1 and 65535")
	}
	if c.Web.Port <= 0 || c.Web.Port > 65535 {
		return fmt.Errorf("web.port must be between 1 and 65535")
	}
	return nil
}

// Validate checks if the simulation configuration is valid
func (c *SimulationConfig) Validate() error {
	if c.Frequencies.Heartbeat <= 0 {
		return fmt.Errorf("frequencies.heartbeat must be greater than 0")
	}
	if c.Frequencies.Attitude <= 0 {
		return fmt.Errorf("frequencies.attitude must be greater than 0")
	}
	if c.Frequencies.GlobalPosition <= 0 {
		return fmt.Errorf("frequencies.global_position must be greater than 0")
	}
	if c.Frequencies.GPSRaw <= 0 {
		return fmt.Errorf("frequencies.gps_raw must be greater than 0")
	}
	if c.Frequencies.VFRHUD <= 0 {
		return fmt.Errorf("frequencies.vfr_hud must be greater than 0")
	}
	if c.Frequencies.SysStatus <= 0 {
		return fmt.Errorf("frequencies.sys_status must be greater than 0")
	}
	if c.UpdateFrequency <= 0 {
		return fmt.Errorf("update_frequency must be greater than 0")
	}
	if c.IPCheckInterval <= 0 {
		return fmt.Errorf("ip_check_interval must be greater than 0")
	}
	return nil
}

// GetAddress returns the full network address
func (c *Config) GetAddress() string {
	return fmt.Sprintf("%s:%d", c.Network.TargetHost, c.Network.TargetPort)
}

// Save writes the configuration to a YAML file
func (c *Config) Save(filename string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
