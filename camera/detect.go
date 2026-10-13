package camera

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

// PortConfig is the saved overlay assignment for one physical CSI port.
type PortConfig struct {
	Overlay string `json:"overlay"`
	Sensor  string `json:"sensor"`
	Enabled bool   `json:"enabled"`
}

// PortStatus is live + cached status for one physical port.
type PortStatus struct {
	PortConfig
	PhysicalPort   string `json:"physical_port"`
	Connector      string `json:"connector"`
	I2CBus         string `json:"i2c_bus,omitempty"`
	LibcameraIndex *int   `json:"libcamera_index,omitempty"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
}

// ConnectedCamera is one camera currently visible to libcamera.
type ConnectedCamera struct {
	LibcameraIndex int    `json:"libcamera_index"`
	Sensor         string `json:"sensor"`
	SensorName     string `json:"sensor_name"`
	PhysicalPort   string `json:"physical_port"`
	Connector      string `json:"connector"`
	I2CBus         string `json:"i2c_bus"`
	Resolution     string `json:"resolution"`
	Status         string `json:"status"`
}

// DetectedState is persisted to camera_detected.json for fast reboot.
type DetectedState struct {
	UpdatedAt     string                `json:"updated_at"`
	Ports         map[string]PortConfig `json:"ports"`
	LastConnected []ConnectedCamera     `json:"last_connected"`
}

// RegistrySensor describes a supported camera type.
type RegistrySensor struct {
	ID            string         `json:"id"`
	Overlay       string         `json:"overlay"`
	Name          string         `json:"name"`
	Module        string         `json:"module"`
	I2CAddr       string         `json:"i2c_addr"`
	Megapixel     float64        `json:"megapixel"`
	StreamProfile *StreamProfile `json:"stream_profile,omitempty"`
}

// StreamProfile lists recommended stream resolutions for a sensor (UI hints).
type StreamProfile struct {
	Recommended string            `json:"recommended"`
	Hint        string            `json:"hint"`
	Groups      []ResolutionGroup `json:"groups"`
}

// ResolutionGroup is one optgroup in the resolution dropdown.
type ResolutionGroup struct {
	Label   string             `json:"label"`
	Options []ResolutionOption `json:"options"`
}

// ResolutionOption is one resolution choice.
type ResolutionOption struct {
	Value string `json:"value"`
	Text  string `json:"text"`
}

// Registry is camera_registry.json
type Registry struct {
	Description          string           `json:"description"`
	DefaultStreamProfile *StreamProfile   `json:"default_stream_profile,omitempty"`
	Sensors              []RegistrySensor `json:"sensors"`
}

// DetectResult is returned by Scan/API.
type DetectResult struct {
	Success        bool                  `json:"success"`
	ConnectedCount int                   `json:"connected_count"`
	ExpectedCount  int                   `json:"expected_count"`
	SupportedCount int                   `json:"supported_types"`
	UpdatedAt      string                `json:"updated_at"`
	Cached         bool                  `json:"cached"`
	Connected      []ConnectedCamera     `json:"connected"`
	Ports          map[string]PortStatus `json:"ports"`
	Message        string                `json:"message,omitempty"`
}

var (
	camLineRe = regexp.MustCompile(`(?m)^\s*(\d+)\s*:\s*(\S+)\s*\[([^\]]+)\].*i2c@([0-9a-f]+)/(\w+)@`)
	i2cToPort = map[string]struct{ port, conn string }{
		"88000": {"cam0", "J30"},
		"70000": {"cam1", "J29"},
	}
)

func registryPath() string {
	return filepath.Join(FindLandingDir(), "camera_registry.json")
}

func detectedPath() string {
	return filepath.Join(FindLandingDir(), "camera_detected.json")
}

// LoadRegistry reads supported camera types.
func LoadRegistry() (*Registry, error) {
	data, err := os.ReadFile(registryPath())
	if err != nil {
		return nil, err
	}
	var reg Registry
	return &reg, json.Unmarshal(data, &reg)
}

func sensorDisplayName(reg *Registry, sensorID string) string {
	if reg != nil {
		for _, s := range reg.Sensors {
			if s.ID == sensorID {
				return fmt.Sprintf("%s (%s)", s.Name, strings.ToUpper(s.ID))
			}
		}
	}
	return strings.ToUpper(sensorID)
}

// LoadDetected reads saved detection state.
func LoadDetected() (*DetectedState, error) {
	path := detectedPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultDetectedState(), nil
		}
		return nil, err
	}
	var st DetectedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	if st.Ports == nil {
		st.Ports = defaultDetectedState().Ports
	}
	return &st, nil
}

func defaultDetectedState() *DetectedState {
	return &DetectedState{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Ports: map[string]PortConfig{
			"cam0": {Overlay: "imx219", Sensor: "imx219", Enabled: true},
			"cam1": {Overlay: "imx219", Sensor: "imx219", Enabled: true},
		},
	}
}

// SaveDetected persists detection state.
func SaveDetected(st *DetectedState) error {
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(detectedPath(), data, 0644)
}

// ScanLive runs rpicam-hello --list-cameras and merges with saved port config.
func ScanLive(refresh bool) (*DetectResult, error) {
	reg, _ := LoadRegistry()
	saved, err := LoadDetected()
	if err != nil {
		saved = defaultDetectedState()
	}

	connected, scanErr := parseRpicamList()
	result := buildDetectResult(reg, saved, connected)
	result.Cached = !refresh

	if refresh || len(connected) > 0 {
		saved.LastConnected = connected
		_ = SaveDetected(saved)
		result.UpdatedAt = saved.UpdatedAt
	}

	if scanErr != nil {
		result.Message = scanErr.Error()
	}
	return result, scanErr
}

func cameraBinary() (string, error) {
	for _, name := range []string{"rpicam-hello", "libcamera-hello"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("rpicam-hello/libcamera-hello not found in PATH")
}

func parseRpicamList() ([]ConnectedCamera, error) {
	bin, err := cameraBinary()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(bin, "--list-cameras").CombinedOutput()
	text := string(out)
	if strings.Contains(text, "No cameras available") {
		return nil, nil
	}
	if err != nil && len(text) == 0 {
		return nil, fmt.Errorf("rpicam list failed: %v", err)
	}

	reg, _ := LoadRegistry()
	var cams []ConnectedCamera
	for _, m := range camLineRe.FindAllStringSubmatch(text, -1) {
		idx := atoi(m[1])
		sensor := m[2]
		res := strings.Fields(m[3])[0]
		i2c := m[4]
		portInfo, ok := i2cToPort[i2c]
		phys, conn := "unknown", "?"
		if ok {
			phys, conn = portInfo.port, portInfo.conn
		}
		cams = append(cams, ConnectedCamera{
			LibcameraIndex: idx,
			Sensor:         sensor,
			SensorName:     sensorDisplayName(reg, sensor),
			PhysicalPort:   phys,
			Connector:      conn,
			I2CBus:         i2c,
			Resolution:     res,
			Status:         "ok",
		})
	}
	return cams, nil
}

func buildDetectResult(reg *Registry, saved *DetectedState, connected []ConnectedCamera) *DetectResult {
	ports := map[string]PortStatus{}
	indexByI2C := map[string]int{}
	for _, c := range connected {
		indexByI2C[c.I2CBus] = c.LibcameraIndex
	}

	expected := 0
	for _, portName := range []string{"cam0", "cam1"} {
		cfg := saved.Ports[portName]
		if !cfg.Enabled {
			continue
		}
		expected++
		i2c := "88000"
		conn := "J30"
		if portName == "cam1" {
			i2c, conn = "70000", "J29"
		}
		ps := PortStatus{
			PortConfig:   cfg,
			PhysicalPort: portName,
			Connector:    conn,
			I2CBus:       i2c,
		}
		if idx, ok := indexByI2C[i2c]; ok {
			ps.Status = "ok"
			ps.LibcameraIndex = &idx
		} else {
			ps.Status = "error"
			ps.Message = "Không probe được sensor trên cổng này (kiểm tra cáp / loại module / overlay)"
		}
		ports[portName] = ps
	}

	supported := 0
	if reg != nil {
		supported = len(reg.Sensors)
	}

	return &DetectResult{
		Success:        true,
		ConnectedCount: len(connected),
		ExpectedCount:  expected,
		SupportedCount: supported,
		UpdatedAt:      saved.UpdatedAt,
		Connected:      connected,
		Ports:          ports,
	}
}

// UpdatePortConfig saves overlay choice per port (when user swaps camera type).
func UpdatePortConfig(ports map[string]PortConfig) error {
	st, err := LoadDetected()
	if err != nil {
		st = defaultDetectedState()
	}
	for k, v := range ports {
		if v.Sensor == "" {
			v.Sensor = v.Overlay
		}
		st.Ports[k] = v
	}
	return SaveDetected(st)
}

// BootConfigLines generates config.txt camera lines from saved state.
func BootConfigLines() ([]string, error) {
	st, err := LoadDetected()
	if err != nil {
		st = defaultDetectedState()
	}
	lines := []string{
		"",
		"# DroneBridge: camera auto-config from Find_landing/camera_detected.json",
		"camera_auto_detect=0",
		"dtparam=cam0_reg=on",
		"dtparam=cam1_reg=on",
		"dtparam=i2c_csi_dsi=on",
		"dtparam=i2c_csi_dsi0=on",
	}
	for _, portName := range []string{"cam0", "cam1"} {
		cfg, ok := st.Ports[portName]
		if !ok || !cfg.Enabled || cfg.Overlay == "" {
			continue
		}
		// Official overlays only define the cam0 parameter. CAM1 is the default (Unicam 1).
		if portName == "cam0" {
			lines = append(lines, fmt.Sprintf("dtoverlay=%s,cam0", cfg.Overlay))
		} else {
			lines = append(lines, fmt.Sprintf("dtoverlay=%s", cfg.Overlay))
		}
	}
	return lines, nil
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
