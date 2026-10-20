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
	Cellular CellularConfig `yaml:"cellular"`
	Network  NetworkConfig  `yaml:"network"`
	Ethernet EthernetConfig `yaml:"ethernet"`
	Web      WebConfig      `yaml:"web"`
	Camera   CameraConfig   `yaml:"camera"`
	Landing  LandingConfig  `yaml:"landing"`
	LCD      LCDConfig      `yaml:"lcd"`
	VPN      VPNConfig      `yaml:"vpn"`
}

// LogConfig contains logging settings
type LogConfig struct {
	Level                  string `yaml:"level"`                     // debug, info, warn, error
	Verbose                bool   `yaml:"verbose"`                   // Enable verbose parsing of received messages
	TimestampFormat        string `yaml:"timestamp_format"`          // "time" or "unix"
	ServerConnectionOnly   bool   `yaml:"server_connection_only"`    // Show only server connection logs
	ShowWebInteractionLogs bool   `yaml:"show_web_interaction_logs"` // Show logs/errors for local web interactions (:8080)
	ShowPacketStats        bool   `yaml:"show_packet_stats"`         // Show periodic packet statistics (MSGID/PKT/BANDWIDTH)
}

// EthernetConfig contains ethernet interface settings for Pixhawk connection
type EthernetConfig struct {
	Interface                string `yaml:"interface"`                  // Interface name (eth0, end0, etc.) - empty for auto-detect
	LocalIP                  string `yaml:"local_ip"`                   // Local IP - empty for auto-detect
	BroadcastIP              string `yaml:"broadcast_ip"`               // Broadcast IP - empty for auto-detect
	PixhawkIP                string `yaml:"pixhawk_ip"`                 // Pixhawk IP address for filtering
	PixhawkPort              int    `yaml:"pixhawk_port"`               // PX4 MAVLink UDP port (default: local_listen_port)
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
	VehicleType               uint8   `yaml:"vehicle_type"`                // MAVLink HEARTBEAT.type for REGISTER_INIT v2
	Model                     string  `yaml:"model"`                       // Custom model label (≤32 chars) for REGISTER_INIT v2
	KeepaliveInterval         int     `yaml:"keepalive_interval"`          // seconds
	ReconnectInterval         int     `yaml:"reconnect_interval"`          // max seconds between reconnect attempts (backoff 10s, 20s, … up to this)
	StartupRetryInterval      int     `yaml:"startup_retry_interval"`      // seconds between boot-time auth retries
	SessionHeartbeatFrequency float64 `yaml:"session_heartbeat_frequency"` // Hz
}

// CellularConfig selects the modem driver (Python modem_init layer).
type CellularConfig struct {
	Enabled        bool           `yaml:"enabled"`
	Driver         string         `yaml:"driver"` // sim7600_qmi | sim8262_pcie
	Iface          string         `yaml:"iface"`
	APN            string         `yaml:"apn"`
	QmiDev         string         `yaml:"qmi_dev"`
	NormalizeIface bool           `yaml:"normalize_iface"`
	Sim7600        map[string]any `yaml:"sim7600"`
	Sim8262        map[string]any `yaml:"sim8262"`
}

// NetworkConfig contains network settings
type NetworkConfig struct {
	LocalListenPort      int    `yaml:"local_listen_port"`
	TargetHost           string `yaml:"target_host"`
	TargetPort           int    `yaml:"target_port"`
	Protocol             string `yaml:"protocol"`
	ConnectionType       string `yaml:"connection_type"`        // "serial", "ethernet", or "prefer_ethernet"
	SerialPort           string `yaml:"serial_port"`            // legacy alias of telem2_port
	SerialBaud           int    `yaml:"serial_baud"`            // legacy; telem2_baud wins for TELEM2
	Telem2Port           string `yaml:"telem2_port"`            // CM5 UART2 4-wire TX4 RX5 CTS6 RTS7
	Telem2Baud           int    `yaml:"telem2_baud"`            // 1500000 8N1, no RTS/CTS
	Telem3Port           string `yaml:"telem3_port"`            // CM5 UART3 4-wire TX8 RX9 CTS10 RTS11
	Telem3Baud           int    `yaml:"telem3_baud"`            // 1500000 8N1, no RTS/CTS
	LinkLossTimeoutMs    int    `yaml:"link_loss_timeout_ms"`   // PX4 HEARTBEAT loss before leaving a link
	EthRecoverHeartbeats int    `yaml:"eth_recover_heartbeats"` // PX4 HEARTBEATs required before returning to Ethernet
	Mode                 string `yaml:"mode"`                   // "4g_only", "wifi_only", "prefer_4g"
	CloudWifiFallback    *bool  `yaml:"cloud_wifi_fallback"`    // false = never PBR wlan0 for server
	ForwardGpsRawInt     *bool  `yaml:"forward_gps_raw_int"`    // true = uplink GPS_RAW_INT with EKF lat/lon/alt
	FallbackDelay        int    `yaml:"fallback_delay"`         // seconds to wait before falling back to WiFi (default: 300)
}

// WebConfig contains web server settings
type WebConfig struct {
	Port int `yaml:"port"`
}

// VPNConfig contains WireGuard VPN settings
type VPNConfig struct {
	Enabled        bool   `yaml:"enabled"`         // Enable VPN provisioning
	ConfigFile     string `yaml:"config_file"`     // Path to file
	ServerEndpoint string `yaml:"server_endpoint"` // Server endpoint
	ServerPubKey   string `yaml:"server_pub_key"`  // Server pub key
	VPNPort        int    `yaml:"vpn_port"`        // VPN inside tunnel
	RouterVPNIP    string `yaml:"router_vpn_ip"`   // Router IP
}

// CameraConfig contains camera streaming settings
type CameraConfig struct {
	Enabled               bool                 `yaml:"enabled"`
	AutoStart             bool                 `yaml:"auto_start"`
	AutoSetup             bool                 `yaml:"auto_setup"`
	ConfigFile            string               `yaml:"config_file"` // legacy fallback if streams empty
	StreamPath            string               `yaml:"stream_path"`
	MediamtxHost          string               `yaml:"mediamtx_host"` // empty → auth.host
	MediamtxPort          int                  `yaml:"mediamtx_port"` // default 8554
	MediamtxWebrtcPort    int                  `yaml:"mediamtx_webrtc_port"`
	MediamtxHlsPort       int                  `yaml:"mediamtx_hls_port"`
	PublishPathCam0Suffix string               `yaml:"publish_path_cam0_suffix"`
	PublishPathCam1Suffix string               `yaml:"publish_path_cam1_suffix"`
	PrimaryCameraID       int                  `yaml:"primary_camera_id"`      // physical cam → /cam0 path (other → /cam1)
	AutoMemoryProfile     bool                 `yaml:"auto_memory_profile"`    // auto-tune buffers/CV for 2/4/8 GB CM5
	LowMemoryMode         bool                 `yaml:"low_memory_mode"`        // HW stream: buffer 2 + cap encode resolution
	StreamBufferCount     int                  `yaml:"stream_buffer_count"`    // rpicam-vid --buffer-count (default 2)
	SkipMissingSensors    bool                 `yaml:"skip_missing_sensors"`   // I-8: do not start stream when CSI empty
	MediamtxRelayHost     string               `yaml:"mediamtx_relay_host"`    // I-7: optional remote relay hop (documented)
	MavlinkStatusEnabled  *bool                `yaml:"mavlink_status_enabled"` // VIDEO_STREAM_STATUS → QCC CAM LED (default on)
	MavlinkStatusHz       float64              `yaml:"mavlink_status_hz"`      // Hz (default 1)
	Streams               []CameraStreamConfig `yaml:"streams"`
}

// CameraStreamConfig is one camera stream (cam0, cam1, …)
type CameraStreamConfig struct {
	Name                     string  `yaml:"name"`
	CameraID                 int     `yaml:"camera_id"`
	Enabled                  bool    `yaml:"enabled"`
	Size                     []int   `yaml:"size"`
	Framerate                int     `yaml:"framerate"`
	Bitrate                  int     `yaml:"bitrate"`
	Format                   string  `yaml:"format"`
	Brightness               float64 `yaml:"brightness"`
	Contrast                 float64 `yaml:"contrast"`
	Sharpness                float64 `yaml:"sharpness"`
	Saturation               float64 `yaml:"saturation"`
	ExposureTime             int     `yaml:"exposure_time"`
	DetectionEnabled         bool    `yaml:"detection_enabled"`
	OverlayEnabled           bool    `yaml:"overlay_enabled"`
	OverlayBurnEnabled       *bool   `yaml:"overlay_burn_enabled"`
	GateTimeoutMs            int     `yaml:"gate_timeout_ms"`
	DetectFrameSkip          int     `yaml:"detect_frame_skip"`
	LandingDetectionMode     string  `yaml:"landing_detection_mode"`
	ArucoMarkerId            int     `yaml:"aruco_marker_id"`
	ArucoDictionary          string  `yaml:"aruco_dictionary"`
	DetectionThreshold       float64 `yaml:"detection_threshold"`
	LandingFullframeFallback *bool   `yaml:"landing_fullframe_fallback"`
	OverlayFrameSkip         int     `yaml:"overlay_frame_skip"`
	LoresSize                []int   `yaml:"lores_size"`
	KeyframeInterval         int     `yaml:"keyframe_interval"`
	Preset                   string  `yaml:"preset"`
	Tune                     string  `yaml:"tune"`
}

// LandingConfig contains auto-landing detection settings
type LandingConfig struct {
	Environment    string  `yaml:"environment"`
	MarkerType     string  `yaml:"marker_type"`
	Mode           string  `yaml:"mode"`
	SafetyLevel    int     `yaml:"safety_level"`
	Template       string  `yaml:"template"`
	MavlinkEnabled bool    `yaml:"mavlink_enabled"`
	MavlinkHz      float64 `yaml:"mavlink_hz"`
	MavlinkCamera  int     `yaml:"mavlink_camera_id"`
}

// LCDConfig contains SH1107 display settings
type LCDConfig struct {
	Enabled    bool   `yaml:"enabled"`
	AutoSetup  bool   `yaml:"auto_setup"`
	Overlay    string `yaml:"overlay"`
	Pins       string `yaml:"pins"`
	Bus        int    `yaml:"bus"`
	Address    int    `yaml:"address"`
	Interval   int    `yaml:"interval"`
	ScreenHold int    `yaml:"screen_hold"`
	Screen     string `yaml:"screen"`
}

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

	// VPN defaults — runtime file from auth provisioning is vpn_config.json
	if cfg.VPN.ConfigFile == "" {
		cfg.VPN.ConfigFile = "vpn_config.json"
	}
	if cfg.VPN.VPNPort == 0 {
		cfg.VPN.VPNPort = 14550
	}
	if cfg.VPN.RouterVPNIP == "" {
		cfg.VPN.RouterVPNIP = "10.8.0.1"
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	normalizeCameraConfig(&cfg)

	return &cfg, nil
}

func normalizeCameraConfig(cfg *Config) {
	if cfg.Landing.MavlinkHz <= 0 {
		cfg.Landing.MavlinkHz = 10
	}
	if cfg.Camera.MavlinkStatusHz <= 0 {
		cfg.Camera.MavlinkStatusHz = 1
	}
	for i := range cfg.Camera.Streams {
		s := &cfg.Camera.Streams[i]
		if s.DetectFrameSkip <= 0 {
			s.DetectFrameSkip = 3
		}
		if s.OverlayFrameSkip <= 0 {
			s.OverlayFrameSkip = 5
		}
		if s.GateTimeoutMs < 0 {
			s.GateTimeoutMs = 0
		}
		if len(s.LoresSize) < 2 {
			s.LoresSize = []int{320, 240}
		}
		if s.DetectionThreshold <= 0 {
			s.DetectionThreshold = 0.8
		}
	}
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
		if len(c.Auth.Model) > 32 {
			return fmt.Errorf("auth.model must be at most 32 characters")
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
