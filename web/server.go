package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"DroneBridge/auth"
	"DroneBridge/camera"
	"DroneBridge/config"
	"DroneBridge/metrics"
)

//go:embed static/*
var staticFiles embed.FS

// XML content cache for parameter editor
var xmlContent []byte
var xmlOnce sync.Once

// Mutex to prevent concurrent 4G module access
var moduleMutex sync.Mutex

var projectRoot = detectProjectRoot()

func detectProjectRoot() string {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, cwd)
	}
	candidates = append(candidates, "/home/pi/Run_serverGo", "/opt/dronebridge")

	for _, root := range candidates {
		if _, err := os.Stat(filepath.Join(root, "Find_landing")); err == nil {
			return root
		}
	}

	if len(candidates) > 0 {
		return candidates[0]
	}
	return "."
}

func projectPath(parts ...string) string {
	all := append([]string{projectRoot}, parts...)
	return filepath.Join(all...)
}

func defaultCameraConfig() map[string]interface{} {
	if cfg, err := loadAppConfig(); err == nil {
		return cameraConfigToUI(cfg, 0)
	}

	return map[string]interface{}{
		"camera_id":         0,
		"size":              []int{640, 480},
		"framerate":         30,
		"format":            "BGR888",
		"mediamtx_host":     "45.117.171.237",
		"mediamtx_port":     8554,
		"bitrate":           5000,
		"overlay_enabled":   true,
		"detection_enabled": true,
		"keyframe_interval": 30,
		"preset":            "veryfast",
		"tune":              "zerolatency",
		"brightness":        0,
		"contrast":          1,
		"sharpness":         1.5,
		"saturation":        1.0,
		"exposure_time":     0,
	}
}

func extractJSONPayload(raw []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty output")
	}

	if json.Valid([]byte(trimmed)) {
		return []byte(trimmed), nil
	}

	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] != '[' && trimmed[i] != '{' {
			continue
		}
		candidate := strings.TrimSpace(trimmed[i:])
		if json.Valid([]byte(candidate)) {
			return []byte(candidate), nil
		}
	}

	return nil, fmt.Errorf("no valid JSON payload found")
}

func signalDBMToBars(dbm float64) int {
	switch {
	case dbm >= -75:
		return 5
	case dbm >= -85:
		return 4
	case dbm >= -95:
		return 3
	case dbm >= -105:
		return 2
	case dbm > -120:
		return 1
	default:
		return 0
	}
}

func signalDBMToQuality(dbm float64) string {
	switch {
	case dbm >= -75:
		return "Excellent"
	case dbm >= -85:
		return "Good"
	case dbm >= -95:
		return "Fair"
	case dbm >= -105:
		return "Weak"
	default:
		return "Poor"
	}
}

func get4GSignalInfo() map[string]interface{} {
	result := map[string]interface{}{
		"signal_dbm":     nil,
		"signal_quality": "Unknown",
		"signal_bars":    0,
	}

	netInfo := ReadNetworkStatus()
	fourG, ok := netInfo["4g"].(map[string]interface{})
	if !ok || fourG == nil {
		return result
	}
	if dbm, ok := fourG["signal_dbm"].(float64); ok {
		result["signal_dbm"] = dbm
		result["signal_quality"] = signalDBMToQuality(dbm)
		result["signal_bars"] = signalDBMToBars(dbm)
		return result
	}
	if dbm, ok := fourG["signal_dbm"].(int); ok {
		f := float64(dbm)
		result["signal_dbm"] = f
		result["signal_quality"] = signalDBMToQuality(f)
		result["signal_bars"] = signalDBMToBars(f)
	}
	if quality, ok := fourG["signal_quality"].(string); ok && quality != "" {
		result["signal_quality"] = quality
	}
	return result
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ParamSetRequest represents a request to set a parameter
type ParamSetRequest struct {
	ParamName  string  `json:"paramName"`
	ParamValue float64 `json:"paramValue"`
	ParamType  string  `json:"paramType"`
}

// ParamSetResponse represents the response from setting a parameter
type ParamSetResponse struct {
	Success   bool    `json:"success"`
	Message   string  `json:"message"`
	ParamName string  `json:"paramName"`
	NewValue  float64 `json:"newValue,omitempty"`
}

// ConnectionStatus represents the current connection state
type ConnectionStatus struct {
	Connected bool   `json:"connected"`
	SystemID  uint8  `json:"systemId"`
	Message   string `json:"message"`
}

// CachedParameter represents a parameter with its current value from Pixhawk
type CachedParameter struct {
	ParamId    string  `json:"paramId"`
	ParamValue float64 `json:"paramValue"`
	ParamType  int     `json:"paramType"`
	ParamIndex uint16  `json:"paramIndex"`
}

// ParameterListStatus represents the status of parameter loading
type ParameterListStatus struct {
	Loading       bool              `json:"loading"`
	TotalCount    int               `json:"totalCount"`
	ReceivedCount int               `json:"receivedCount"`
	Progress      float64           `json:"progress"`
	Parameters    []CachedParameter `json:"parameters,omitempty"`
	LastUpdated   string            `json:"lastUpdated,omitempty"`
}

// MAVLinkBridge handles MAVLink communication for parameter setting
type MAVLinkBridge struct {
	node            *gomavlib.Node
	pixhawkSysID    uint8
	connected       bool
	mutex           sync.RWMutex
	responseTimeout time.Duration

	// Parameter cache
	paramCache      map[string]CachedParameter
	paramCacheMutex sync.RWMutex
	paramTotal      int
	paramReceived   int
	paramLoading    bool
	paramLastUpdate time.Time

	// Channel to receive PARAM_VALUE messages from forwarder
	paramValueCh chan *common.MessageParamValue
}

var bridge *MAVLinkBridge
var bridgeOnce sync.Once

// InitMAVLinkBridge initializes the MAVLink bridge with the given node
func InitMAVLinkBridge(node *gomavlib.Node) {
	bridgeOnce.Do(func() {
		bridge = &MAVLinkBridge{
			node:            node,
			responseTimeout: 5 * time.Second,
			paramCache:      make(map[string]CachedParameter),
			paramValueCh:    make(chan *common.MessageParamValue, 100),
		}
		go bridge.processParamValues()
	})
}

// HandleParamValue receives PARAM_VALUE message from forwarder
func HandleParamValue(msg *common.MessageParamValue) {
	if bridge != nil && bridge.paramValueCh != nil {
		select {
		case bridge.paramValueCh <- msg:
		default:
			// Channel full, skip
		}
	}
}

// HandleHeartbeat receives heartbeat from forwarder
func HandleHeartbeat(sysID uint8) {
	if bridge != nil {
		bridge.mutex.Lock()
		if !bridge.connected {
			bridge.pixhawkSysID = sysID
			bridge.connected = true
			log.Printf("[WEB] Connected to Pixhawk (System ID: %d)", sysID)
		}
		bridge.mutex.Unlock()
	}
}

func trimParamID(id string) string {
	if i := strings.IndexByte(id, 0); i >= 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

func (b *MAVLinkBridge) lookupCachedParamLocked(paramName string) (CachedParameter, bool) {
	if param, ok := b.paramCache[paramName]; ok {
		return param, true
	}
	want := trimParamID(paramName)
	for k, param := range b.paramCache {
		if trimParamID(k) == want {
			return param, true
		}
	}
	return CachedParameter{}, false
}

func (b *MAVLinkBridge) processParamValues() {
	for msg := range b.paramValueCh {
		paramID := trimParamID(msg.ParamId)
		// Decode value based on type
		var decodedValue float64
		if msg.ParamType == common.MAV_PARAM_TYPE_INT32 ||
			msg.ParamType == common.MAV_PARAM_TYPE_UINT32 ||
			msg.ParamType == common.MAV_PARAM_TYPE_INT16 ||
			msg.ParamType == common.MAV_PARAM_TYPE_UINT16 ||
			msg.ParamType == common.MAV_PARAM_TYPE_INT8 ||
			msg.ParamType == common.MAV_PARAM_TYPE_UINT8 {
			decodedValue = float64(int32(math.Float32bits(msg.ParamValue)))
		} else {
			decodedValue = float64(msg.ParamValue)
		}

		b.paramCacheMutex.Lock()

		b.paramCache[paramID] = CachedParameter{
			ParamId:    paramID,
			ParamValue: decodedValue,
			ParamType:  int(msg.ParamType),
			ParamIndex: msg.ParamIndex,
		}

		b.paramTotal = int(msg.ParamCount)
		b.paramReceived = len(b.paramCache)
		b.paramLastUpdate = time.Now()

		// Check if loading complete
		if b.paramReceived >= b.paramTotal && b.paramLoading {
			b.paramLoading = false
			log.Printf("[WEB] Parameter loading complete: %d/%d parameters", b.paramReceived, b.paramTotal)
		}

		b.paramCacheMutex.Unlock()
	}
}

func (b *MAVLinkBridge) IsConnected() bool {
	if b == nil {
		return false
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.connected
}

func (b *MAVLinkBridge) GetSystemID() uint8 {
	if b == nil {
		return 0
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.pixhawkSysID
}

// GetPixhawkSystemID returns the actual Pixhawk system ID after connection is established
// This function should be used instead of hardcoding system IDs (like 1) because:
// 1. The actual system ID is detected from the Pixhawk heartbeat
// 2. Returns 1 as fallback if not yet connected (standard PX4 default)
// 3. Ensures all MAVLink operations use the correct, dynamic system ID
//
// Flow:
// - Forwarder receives heartbeat from Pixhawk -> captures sysID
// - Forwarder calls HandleHeartbeat(sysID) -> web bridge stores the ID
// - Web server can retrieve it via GetPixhawkSystemID() for parameter operations
// - Forwarder logs actual sysID for verification
func GetPixhawkSystemID() uint8 {
	if bridge == nil {
		return 1 // Fallback to default if bridge not initialized
	}
	return bridge.GetSystemID()
}

// RequestParameterList sends PARAM_REQUEST_LIST to Pixhawk
func (b *MAVLinkBridge) RequestParameterList() error {
	if b == nil || b.node == nil {
		return fmt.Errorf("MAVLink bridge not initialized")
	}

	b.mutex.RLock()
	connected := b.connected
	sysID := b.pixhawkSysID
	b.mutex.RUnlock()

	if !connected {
		return fmt.Errorf("not connected to Pixhawk")
	}

	// Clear cache and start loading
	b.paramCacheMutex.Lock()
	b.paramCache = make(map[string]CachedParameter)
	b.paramReceived = 0
	b.paramTotal = 0
	b.paramLoading = true
	b.paramCacheMutex.Unlock()

	// Create PARAM_REQUEST_LIST message
	msg := &common.MessageParamRequestList{
		TargetSystem:    sysID,
		TargetComponent: 1, // MAV_COMP_ID_AUTOPILOT1
	}

	log.Printf("[WEB] Sending PARAM_REQUEST_LIST to system %d", sysID)

	err := b.writeToPixhawk(msg)
	if err != nil {
		b.paramCacheMutex.Lock()
		b.paramLoading = false
		b.paramCacheMutex.Unlock()
		return fmt.Errorf("failed to send PARAM_REQUEST_LIST: %w", err)
	}

	return nil
}

// GetParameterListStatus returns the current status of parameter loading
func (b *MAVLinkBridge) GetParameterListStatus(includeParams bool) *ParameterListStatus {
	if b == nil {
		return &ParameterListStatus{Loading: false}
	}

	b.paramCacheMutex.RLock()
	defer b.paramCacheMutex.RUnlock()

	status := &ParameterListStatus{
		Loading:       b.paramLoading,
		TotalCount:    b.paramTotal,
		ReceivedCount: b.paramReceived,
	}

	if b.paramTotal > 0 {
		status.Progress = float64(b.paramReceived) / float64(b.paramTotal) * 100
	}

	if !b.paramLastUpdate.IsZero() {
		status.LastUpdated = b.paramLastUpdate.Format(time.RFC3339)
	}

	if includeParams && len(b.paramCache) > 0 {
		status.Parameters = make([]CachedParameter, 0, len(b.paramCache))
		for _, p := range b.paramCache {
			status.Parameters = append(status.Parameters, p)
		}
	}

	return status
}

// GetCachedParameter returns a single cached parameter value
func (b *MAVLinkBridge) GetCachedParameter(paramName string) (CachedParameter, bool) {
	if b == nil {
		return CachedParameter{}, false
	}

	b.paramCacheMutex.RLock()
	defer b.paramCacheMutex.RUnlock()

	return b.lookupCachedParamLocked(paramName)
}

func (b *MAVLinkBridge) SetParameter(paramName string, paramValue float64, paramType string) *ParamSetResponse {
	if b == nil || b.node == nil {
		return &ParamSetResponse{
			Success:   false,
			Message:   "MAVLink bridge not initialized",
			ParamName: paramName,
		}
	}

	b.mutex.RLock()
	connected := b.connected
	sysID := b.pixhawkSysID
	b.mutex.RUnlock()

	if !connected {
		return &ParamSetResponse{
			Success:   false,
			Message:   "Not connected to Pixhawk",
			ParamName: paramName,
		}
	}

	// Convert param type string to MAVLink type
	mavParamType := getMavParamType(paramType)

	// Encode the value based on type
	var encodedValue float32
	if mavParamType == common.MAV_PARAM_TYPE_INT32 || mavParamType == common.MAV_PARAM_TYPE_UINT32 ||
		mavParamType == common.MAV_PARAM_TYPE_INT16 || mavParamType == common.MAV_PARAM_TYPE_UINT16 ||
		mavParamType == common.MAV_PARAM_TYPE_INT8 || mavParamType == common.MAV_PARAM_TYPE_UINT8 {
		encodedValue = math.Float32frombits(uint32(int32(paramValue)))
	} else {
		encodedValue = float32(paramValue)
	}

	// Create PARAM_SET message
	paramMsg := &common.MessageParamSet{
		TargetSystem:    sysID,
		TargetComponent: 1,
		ParamId:         paramName,
		ParamValue:      encodedValue,
		ParamType:       mavParamType,
	}

	log.Printf("[WEB] Sending PARAM_SET: %s = %v (type: %s)", paramName, paramValue, paramType)

	err := b.writeToPixhawk(paramMsg)
	if err != nil {
		return &ParamSetResponse{
			Success:   false,
			Message:   fmt.Sprintf("Failed to send PARAM_SET: %v", err),
			ParamName: paramName,
		}
	}

	// Wait for PARAM_VALUE response
	return b.waitForParamResponse(paramName)
}

func (b *MAVLinkBridge) waitForParamResponse(paramName string) *ParamSetResponse {
	timeout := time.After(b.responseTimeout)

	// Poll the cache for the updated value
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case <-ticker.C:
			b.paramCacheMutex.RLock()
			param, exists := b.lookupCachedParamLocked(paramName)
			lastUpdate := b.paramLastUpdate
			b.paramCacheMutex.RUnlock()

			if exists && lastUpdate.After(startTime) {
				log.Printf("[WEB] PARAM_VALUE received: %s = %v", paramName, param.ParamValue)
				return &ParamSetResponse{
					Success:   true,
					Message:   fmt.Sprintf("Parameter %s successfully set", paramName),
					ParamName: paramName,
					NewValue:  param.ParamValue,
				}
			}

		case <-timeout:
			return &ParamSetResponse{
				Success:   false,
				Message:   "Timeout waiting for parameter confirmation",
				ParamName: paramName,
			}
		}
	}
}

func getMavParamType(typeStr string) common.MAV_PARAM_TYPE {
	switch typeStr {
	case "FLOAT", "float", "REAL32":
		return common.MAV_PARAM_TYPE_REAL32
	case "INT32", "int":
		return common.MAV_PARAM_TYPE_INT32
	case "UINT32":
		return common.MAV_PARAM_TYPE_UINT32
	case "INT16":
		return common.MAV_PARAM_TYPE_INT16
	case "UINT16":
		return common.MAV_PARAM_TYPE_UINT16
	case "INT8":
		return common.MAV_PARAM_TYPE_INT8
	case "UINT8", "bool":
		return common.MAV_PARAM_TYPE_UINT8
	default:
		return common.MAV_PARAM_TYPE_INT32
	}
}

// formatUnixTimestamp converts Unix timestamp to ISO 8601 format
func formatUnixTimestamp(ts uint64) interface{} {
	if ts == 0 {
		return nil
	}
	return time.Unix(int64(ts), 0).Format(time.RFC3339)
}

func StartServer(port int, authClient *auth.Client, droneUUID string) {
	registerHostPowerRoutes()
	// Pre-load XML file into memory cache for faster serving
	loadXMLCache()

	// Serve static files with caching headers
	fsys, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Create a custom file server with caching headers
	fileServer := http.FileServer(http.FS(fsys))
	fileServerWithCache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		fileServer.ServeHTTP(w, r)
	})

	// Redirect root to dashboard
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/dashboard.html", http.StatusFound)
			return
		}
		// Set CORS and cache headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		if r.Method == http.MethodOptions {
			return
		}
		fileServerWithCache.ServeHTTP(w, r)
	})

	// API endpoint for status
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		snap := metrics.Global.GetSnapshot()
		if diag := getMavlinkDiagnostics(); diag != nil {
			snap["mavlink_diagnostics"] = diag
		}
		mergeNetworkIntoSnapshot(snap)
		snap["host"] = hostThermalStatus()
		json.NewEncoder(w).Encode(snap)
	})

	http.HandleFunc("/api/preflight", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(BuildPreflightReport())
	})

	// API endpoint for connection status
	http.HandleFunc("/api/connection", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		status := ConnectionStatus{
			Connected: false,
			SystemID:  0,
			Message:   "MAVLink bridge not initialized",
		}

		if bridge != nil {
			status.Connected = bridge.IsConnected()
			status.SystemID = bridge.GetSystemID()
			if status.Connected {
				status.Message = fmt.Sprintf("Connected to Pixhawk (System ID: %d)", status.SystemID)
			} else {
				status.Message = "Waiting for Pixhawk connection..."
			}
		}

		json.NewEncoder(w).Encode(status)
	})

	// API endpoint for setting parameters
	http.HandleFunc("/api/param/set", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req ParamSetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}

		log.Printf("[WEB] Received param set request: %+v", req)

		var response *ParamSetResponse
		if bridge != nil {
			response = bridge.SetParameter(req.ParamName, req.ParamValue, req.ParamType)
		} else {
			response = &ParamSetResponse{
				Success:   false,
				Message:   "MAVLink bridge not initialized",
				ParamName: req.ParamName,
			}
		}

		json.NewEncoder(w).Encode(response)
	})

	// API endpoint for health check
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// API endpoint to request parameter list from Pixhawk
	http.HandleFunc("/api/param/request-list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if bridge == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "MAVLink bridge not initialized",
			})
			return
		}

		err := bridge.RequestParameterList()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Parameter list request sent",
		})
	})

	// API endpoint to get parameter loading status and cached values
	http.HandleFunc("/api/param/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		includeParams := r.URL.Query().Get("include") == "params"

		if bridge == nil {
			json.NewEncoder(w).Encode(&ParameterListStatus{Loading: false})
			return
		}

		status := bridge.GetParameterListStatus(includeParams)
		json.NewEncoder(w).Encode(status)
	})

	// API endpoint to get all cached parameters
	http.HandleFunc("/api/param/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		if bridge == nil {
			json.NewEncoder(w).Encode([]CachedParameter{})
			return
		}

		status := bridge.GetParameterListStatus(true)
		json.NewEncoder(w).Encode(status.Parameters)
	})

	// API endpoint to get a single cached parameter
	http.HandleFunc("/api/param/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")

		paramName := r.URL.Query().Get("name")
		if paramName == "" {
			http.Error(w, "Missing 'name' parameter", http.StatusBadRequest)
			return
		}

		if bridge == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"found": false,
			})
			return
		}

		param, exists := bridge.GetCachedParameter(paramName)
		if !exists {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"found": false,
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"found": true,
			"param": param,
		})
	})

	// Helper function to set CORS headers
	setCORSHeaders := func(w http.ResponseWriter) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
	}

	// GET /api/v1/drone/session — live auth session from registration.
	// An API key is a separate object; this stays visible when no key exists.
	http.HandleFunc("/api/v1/drone/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"authenticated": false,
				"error":         "Auth client not initialized",
			})
			return
		}
		token, localIP, expires := authClient.GetSessionInfo()
		snap := metrics.Global.GetSnapshot()
		status, _ := snap["auth_status"].(string)
		authenticated := token != "" && !expires.IsZero() && time.Now().Before(expires)
		var expiresOut interface{}
		if !expires.IsZero() {
			expiresOut = expires.Format(time.RFC3339)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated": authenticated,
			"status":        status,
			"token":         token,
			"expires_at":    expiresOut,
			"local_ip":      localIP,
			"drone_uuid":    droneUUID,
		})
	})

	// API Key Management Endpoints (compatible with HBQCONNECT format)
	// GET /api/v1/drone/api-key/status - Get current API key status
	http.HandleFunc("/api/v1/drone/api-key/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Auth client not initialized",
			})
			return
		}

		// Try to get API key status with retries
		var state *auth.APIKeyStatusResponse
		var err error
		maxRetries := 3
		retryDelay := 500 * time.Millisecond

		for attempt := 0; attempt < maxRetries; attempt++ {
			state, err = authClient.GetAPIKeyStatus()
			if err == nil {
				break
			}
			if attempt < maxRetries-1 {
				time.Sleep(retryDelay)
			}
		}

		if err != nil {
			// Return a "no key" response instead of error if session is not ready
			// This allows frontend to gracefully show "no key" state
			json.NewEncoder(w).Encode(map[string]interface{}{
				"has_active_key": false,
				"status":         "none",
				"api_key":        nil,
				"error":          err.Error(),
			})
			return
		}

		// Convert response to frontend format
		json.NewEncoder(w).Encode(map[string]interface{}{
			"has_active_key": state.HasActiveKey == 0x01,
			"status":         state.Status,
			"api_key":        state.APIKey,
			"created_at":     formatUnixTimestamp(state.CreatedAt),
			"expires_at":     formatUnixTimestamp(state.ExpiresAt),
			"user_uuid":      state.UserUUID,
			"username":       nil, // TODO: Fetch username from backend DB if needed
			"user_active_at": formatUnixTimestamp(state.UserActivatedAt),
		})
	})

	// POST /api/v1/drone/api-key/request - Request new API key
	http.HandleFunc("/api/v1/drone/api-key/request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Auth client not initialized",
			})
			return
		}

		// Parse request body for expiration hours (optional)
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		expirationHours := 24 // Default
		if exp, ok := req["expiration_hours"]; ok {
			if v, ok := exp.(float64); ok {
				expirationHours = int(v)
			}
		}

		// Validate expiration range
		if expirationHours < 1 {
			expirationHours = 1
		}
		if expirationHours > 720 { // Max 30 days
			expirationHours = 720
		}

		if status, statusErr := authClient.GetAPIKeyStatus(); statusErr == nil && status != nil && status.HasActiveKey == 0x01 {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "drone already has an active API key — revoke current key first",
			})
			return
		}

		state, err := authClient.RequestAPIKey(expirationHours)
		if err != nil {
			if err.Error() == "drone already has an active API key" {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		// Convert response to frontend format
		json.NewEncoder(w).Encode(map[string]interface{}{
			"api_key":        state.APIKey,
			"created_at":     time.Now().Format(time.RFC3339),
			"expires_at":     formatUnixTimestamp(state.ExpiresAt),
			"user_uuid":      nil,
			"username":       nil,
			"user_active_at": nil,
		})
	})

	// DELETE /api/v1/drone/api-key/revoke - Revoke current API key
	http.HandleFunc("/api/v1/drone/api-key/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Auth client not initialized",
			})
			return
		}

		if err := authClient.RevokeAPIKey(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "API key revoked successfully",
		})
	})

	// DELETE /api/v1/drone/api-key/delete - Delete API key completely
	http.HandleFunc("/api/v1/drone/api-key/delete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Auth client not initialized",
			})
			return
		}

		if err := authClient.DeleteAPIKey(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "API key deleted successfully",
		})
	})

	// API endpoint to list available landing templates
	http.HandleFunc("/api/landing/templates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		templatesDir := projectPath("Find_landing", "templates")
		files, err := os.ReadDir(templatesDir)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		var templates []string
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(strings.ToLower(file.Name()), ".png") {
				// Remove .png extension to get template name
				name := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
				templates = append(templates, name)
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"templates": templates,
		})
	})

	// Serve template images
	http.HandleFunc("/templates/", func(w http.ResponseWriter, r *http.Request) {
		templateName := strings.TrimPrefix(r.URL.Path, "/templates/")
		templatePath := filepath.Join(projectPath("Find_landing", "templates"), templateName)

		// Security check - prevent directory traversal
		if strings.Contains(templateName, "..") {
			http.Error(w, "Invalid template name", http.StatusBadRequest)
			return
		}

		// Check if file exists
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, templatePath)
	})

	// API endpoint to start camera streamer
	http.HandleFunc("/api/camera/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if len(listRunningStreamerPIDs()) > 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Camera streamer is already running",
				"pids":    listRunningStreamerPIDs(),
			})
			return
		}

		pids, err := startCameraStreamersFromConfig()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Failed to start camera: %v", err),
			})
			return
		}

		log.Printf("[CAMERA] Camera streamer started (PIDs: %v)", pids)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Camera streamer started",
			"pid":     pids[0],
			"pids":    pids,
		})
	})

	// API endpoint to stop camera streamer
	http.HandleFunc("/api/camera/stop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		pids := listRunningStreamerPIDs()
		stopCameraStreamers()

		if len(pids) == 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Camera streamer is not running",
			})
			return
		}

		log.Printf("[CAMERA] Camera streamer stopped (PIDs: %v)", pids)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Camera streamer stopped",
			"pids":    pids,
		})
	})

	// API: restart all enabled camera streams (reload camera_config_*.json)
	http.HandleFunc("/api/camera/restart", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		pids, err := restartCameraStreamers()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		log.Printf("[CAMERA] Camera streamer restarted via API (PIDs: %v)", pids)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Camera stream restarted",
			"pids":    pids,
		})
	})

	http.HandleFunc("/api/camera/test-report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("camera_id"))
		if err != nil || id < 0 || id > 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"verdict": "camera_id phải là 0 hoặc 1"})
			return
		}
		json.NewEncoder(w).Encode(cameraSideReport(id))
	})

	http.HandleFunc("/api/camera/preview", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("camera_id"))
		if err != nil || id < 0 || id > 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		info, err := os.Stat(previewFile(id))
		if err != nil || time.Since(info.ModTime()) > 2*time.Second || info.Size() == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, previewFile(id))
	})

	// API: apply camera boot overlay + schedule CM5 reboot (needs install/camera/install_camera_sudoers.sh)
	http.HandleFunc("/api/camera/apply-overlay", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		text, err := applyCameraOverlayHost()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Không thể áp dụng overlay/reboot. Chạy: sudo bash install/camera/install_camera_sudoers.sh",
				"error":   err.Error(),
				"output":  text,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Đã áp dụng boot overlay — CM5 reboot trong ~2s nếu config đổi",
			"output":  text,
		})
	})

	// API endpoint to check camera status
	http.HandleFunc("/api/camera/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		json.NewEncoder(w).Encode(cameraStatusPayload())
	})

	http.HandleFunc("/api/camera/landing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg, _ := loadAppConfig()
		cameraID := -1
		if q := r.URL.Query().Get("camera_id"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				cameraID = n
			}
		}
		maxAge := 10 * time.Second
		if q := r.URL.Query().Get("max_age_sec"); q != "" {
			if sec, err := strconv.ParseFloat(q, 64); err == nil && sec > 0 {
				maxAge = time.Duration(sec * float64(time.Second))
			}
		}
		if cameraID >= 0 {
			entry := map[string]interface{}{"camera_id": cameraID}
			if lt := camera.ReadLandingTelemetry(cameraID, maxAge); lt != nil {
				entry["landing"] = lt
			}
			if st := camera.ReadStreamStats(cameraID, maxAge); st != nil {
				entry["stream_stats"] = st
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "cameras": []map[string]interface{}{entry}})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"cameras": camera.LandingStatusForConfig(cfg, maxAge),
		})
	})

	// API: live camera scan + cached port status (rpicam-hello)
	http.HandleFunc("/api/camera/detect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		refresh := r.URL.Query().Get("refresh") == "1"
		result, err := camera.ScanLive(refresh)
		if err != nil && result == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	})

	// API: supported camera sensor catalog
	http.HandleFunc("/api/camera/registry", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		reg, err := camera.LoadRegistry()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "registry": reg})
	})

	// API: save overlay/sensor per port (camera_detected.json)
	http.HandleFunc("/api/camera/ports", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Ports map[string]camera.PortConfig `json:"ports"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		if err := camera.UpdatePortConfig(body.Ports); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		log.Printf("[WEB][CAMERA] Port config saved to camera_detected.json")

		needsReboot, setupOut, setupErr := runSetupCameraScript()
		resp := map[string]interface{}{
			"success":      true,
			"needs_reboot": needsReboot,
			"setup_output": setupOut,
		}
		if setupErr != nil {
			resp["setup_error"] = setupErr.Error()
			if strings.Contains(setupErr.Error(), "password") || strings.Contains(setupErr.Error(), "sudo") {
				resp["message"] = "Đã lưu loại CAM. Cần: sudo bash install/camera/install_camera_sudoers.sh rồi nhấn «Reboot CM5»."
			} else {
				resp["message"] = "Đã lưu loại CAM. setup_camera lỗi — xem setup_output"
			}
		} else if needsReboot {
			resp["message"] = "Đã lưu loại CAM. Boot overlay đã đổi — nhấn «Reboot CM5» trên UI để áp dụng."
		} else {
			resp["message"] = "Đã lưu loại CAM."
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Backward compatible alias — uses rpicam scan
	http.HandleFunc("/api/camera/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			return
		}
		result, _ := camera.ScanLive(true)
		if result == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "scan failed"})
			return
		}
		legacy := make([]map[string]interface{}, 0, len(result.Connected))
		for _, c := range result.Connected {
			legacy = append(legacy, map[string]interface{}{
				"id":   c.LibcameraIndex,
				"info": fmt.Sprintf("%s @ %s (%s)", c.SensorName, c.Connector, c.Resolution),
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": result.ConnectedCount > 0,
			"cameras": legacy,
			"detail":  result,
		})
	})

	// API endpoint to load camera config used by settings page
	http.HandleFunc("/api/camera/config/load", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		cameraID := 0
		if idStr := r.URL.Query().Get("camera_id"); idStr != "" {
			if parsed, err := strconv.Atoi(idStr); err == nil {
				cameraID = parsed
			}
		}

		cfg, err := loadAppConfig()
		if err != nil {
			log.Printf("[WEB][CAMERA] Failed to load app config: %v", err)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"config":  defaultCameraConfig(),
				"streams": []map[string]interface{}{},
				"warning": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"config":  cameraConfigToUI(cfg, cameraID),
			"streams": cameraStreamsSummary(cfg),
			"path":    resolveAppConfigPath(),
		})
	})

	// API endpoint to save camera config from settings page
	http.HandleFunc("/api/camera/config/save", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		var incoming map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			log.Printf("[WEB][CAMERA] Invalid save camera payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid JSON payload",
				"error":   err.Error(),
			})
			return
		}

		if err := saveCameraStreamFromUI(incoming); err != nil {
			log.Printf("[WEB][CAMERA] Failed to save camera config: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to save camera config",
				"error":   err.Error(),
			})
			return
		}

		cfg := mustLoadAppConfig()
		if err := syncAllStreamerConfigs(cfg); err != nil {
			log.Printf("[WEB][CAMERA] Failed to write camera_config_*.json: %v", err)
		}

		restart := boolFromMap(incoming, "restart", false)
		var pids []int
		var restartErr error
		if restart {
			pids, restartErr = restartCameraStreamers()
			if restartErr != nil {
				log.Printf("[WEB][CAMERA] Stream restart after save failed: %v", restartErr)
			}
		}

		cfgPath := resolveAppConfigPath()
		cameraID := intFromMap(incoming, "camera_id", 0)
		log.Printf("[WEB][CAMERA] Camera stream saved: camera_id=%d path=%s restart=%v", cameraID, cfgPath, restart)
		resp := map[string]interface{}{
			"success": true,
			"path":    cfgPath,
			"config":  cameraConfigToUI(cfg, cameraID),
		}
		if restart {
			resp["restarted"] = restartErr == nil
			resp["pids"] = pids
			if restartErr != nil {
				resp["restart_error"] = restartErr.Error()
			}
		}
		json.NewEncoder(w).Encode(resp)
	})

	http.HandleFunc("/api/camera/config/global", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		cfg := mustLoadAppConfig()
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":             true,
				"primary_camera_id":   cfg.Camera.PrimaryCameraID,
				"auto_memory_profile": cfg.Camera.AutoMemoryProfile,
				"memory_tier_gb":      int(camera.DetectMemoryTier()),
			})
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Method not allowed"})
			return
		}

		var incoming map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid JSON"})
			return
		}
		if err := saveCameraGlobalFromUI(incoming); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": err.Error()})
			return
		}
		cfg = mustLoadAppConfig()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":             true,
			"primary_camera_id":   cfg.Camera.PrimaryCameraID,
			"auto_memory_profile": cfg.Camera.AutoMemoryProfile,
			"memory_tier_gb":      int(camera.DetectMemoryTier()),
		})
	})

	// API endpoint to save landing config
	http.HandleFunc("/api/landing/config/save", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var config map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid JSON: " + err.Error(),
			})
			return
		}

		configPath := projectPath("Find_landing", "landing_config.json")
		configData, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to marshal config: " + err.Error(),
			})
			return
		}

		if err := os.WriteFile(configPath, configData, 0644); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to save config: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Landing config saved successfully",
			"path":    configPath,
		})
	})

	// API endpoint to load landing config
	http.HandleFunc("/api/landing/config/load", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		configPath := projectPath("Find_landing", "landing_config.json")
		configData, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Config file not found",
				})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Failed to read config: " + err.Error(),
				})
			}
			return
		}

		var config map[string]interface{}
		if err := json.Unmarshal(configData, &config); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to parse config: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"config":  config,
		})
	})

	// API endpoint to get real-time network info
	// Network status API
	registerCellularModuleHandlers()
	http.HandleFunc("/api/network/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		// ReadNetworkStatus: connection_manager → /run/dronebridge/network_status.json
		networkInfo := ReadNetworkStatus()

		if fourGInfo, ok := networkInfo["4g"].(map[string]interface{}); ok {
			if status, _ := fourGInfo["status"].(string); status == "connected" || status == "available" {
				signalInfo := get4GSignalInfo()
				for k, v := range signalInfo {
					fourGInfo[k] = v
				}
				networkInfo["4g"] = fourGInfo
			}
		}

		// Return network status directly (not wrapped)
		json.NewEncoder(w).Encode(networkInfo)
	})

	// Canonical API: network.mode in config.yaml (prefer_4g | 4g_only | wifi_only)
	http.HandleFunc("/api/network/mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		switch r.Method {
		case http.MethodGet:
			cfg, err := loadAppConfig()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Failed to load config: " + err.Error(),
				})
				return
			}
			json.NewEncoder(w).Encode(buildNetworkModePayload(cfg))

		case http.MethodPost:
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Invalid JSON",
				})
				return
			}

			mode, _ := req["mode"].(string)
			mode = strings.TrimSpace(mode)
			if mode == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "mode required (prefer_4g, 4g_only, wifi_only)",
				})
				return
			}

			var cloudFallback *bool
			if v, ok := req["cloud_wifi_fallback"].(bool); ok {
				cloudFallback = &v
			}
			var fallbackDelay *int
			if v, ok := req["fallback_delay"].(float64); ok {
				d := int(v)
				fallbackDelay = &d
			}

			cfg, err := applyNetworkMode(mode, cloudFallback, fallbackDelay)
			if err != nil {
				if _, ok := err.(*networkModeError); ok {
					w.WriteHeader(http.StatusBadRequest)
				} else {
					w.WriteHeader(http.StatusInternalServerError)
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": err.Error(),
				})
				return
			}

			out := buildNetworkModePayload(cfg)
			out["message"] = "Network mode set to " + mode
			json.NewEncoder(w).Encode(out)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
		}
	})

	// Legacy alias — maps wifi/4g priority to config.yaml network.mode
	http.HandleFunc("/api/network/priority", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method == http.MethodPost {
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Invalid JSON",
				})
				return
			}

			priority := req["priority"]
			if priority != "4g" && priority != "wifi" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Invalid priority. Use: 4g or wifi",
				})
				return
			}

			mode := legacyPriorityToMode(priority)
			cfg, err := applyNetworkMode(mode, nil, nil)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": err.Error(),
				})
				return
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"message":  "Priority set to " + priority + " (mode=" + mode + ")",
				"priority": priority,
				"mode":     cfg.Network.Mode,
			})
		} else {
			cfg, err := loadAppConfig()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			mode := cfg.Network.Mode
			if mode == "" {
				mode = "prefer_4g"
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"priority": networkModeToLegacyPriority(mode),
				"mode":     mode,
			})
		}
	})

	// API endpoint to trigger network reconnection
	http.HandleFunc("/api/network/reconnect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		// Run connection_manager.py to reconnect
		go func() {
			triggerNetmonApply("")
		}()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Reconnection triggered",
		})
	})

	// API endpoint to switch active network from web UI
	http.HandleFunc("/api/network/switch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid JSON",
			})
			return
		}

		target := strings.ToLower(strings.TrimSpace(req["target"]))
		if target != "4g" && target != "wifi" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid target. Use: 4g or wifi",
			})
			return
		}

		mode := switchTargetToMode(target)
		cfg, err := applyNetworkMode(mode, nil, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Switched to " + target + " (mode=" + mode + ")",
			"target":  target,
			"mode":    cfg.Network.Mode,
		})
	})

	// API endpoint to test internet connectivity and basic latency
	http.HandleFunc("/api/network/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "3", "8.8.8.8")
		output, err := cmd.CombinedOutput()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "No internet connectivity",
			})
			return
		}

		latency := "N/A"
		re := regexp.MustCompile(`time[=<]([0-9.]+)\s*ms`)
		if m := re.FindStringSubmatch(string(output)); len(m) > 1 {
			latency = m[1]
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"latency": latency,
			"message": "Connection test successful",
		})
	})

	// API endpoint to get current 4G mode
	http.HandleFunc("/api/network/4g/mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		// Lock to prevent concurrent module access
		moduleMutex.Lock()
		defer moduleMutex.Unlock()

		// Get current mode using Python script
		cmd := exec.Command("python3", projectPath("Module_4G", "set_4g_mode.py"), "get")
		output, err := cmd.Output() // Only capture stdout, not stderr

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to get 4G mode",
			})
			return
		}

		// Parse JSON output from Python script
		var result map[string]interface{}
		if err := json.Unmarshal(output, &result); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to parse mode data",
			})
			return
		}

		// Return the result
		json.NewEncoder(w).Encode(result)
	})

	// API endpoint to set 4G mode
	http.HandleFunc("/api/network/4g/mode/set", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		// Parse request body
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid JSON",
			})
			return
		}

		// Get mode from request
		modeVal, ok := req["mode"]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Mode parameter required",
			})
			return
		}

		// Convert mode to string for Python script
		var modeStr string
		switch v := modeVal.(type) {
		case float64:
			modeStr = fmt.Sprintf("%d", int(v))
		case int:
			modeStr = fmt.Sprintf("%d", v)
		case string:
			// Try to parse string as number
			if num, err := strconv.Atoi(v); err == nil {
				modeStr = fmt.Sprintf("%d", num)
			} else {
				// Invalid string (like "auto", "4G", etc)
				log.Printf("[WEB][4G] Invalid mode string received: %s", v)
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": fmt.Sprintf("Invalid mode: %s. Use numeric values: 2, 13, 14, 38, 51, 71", v),
				})
				return
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid mode type",
			})
			return
		}

		// Lock to prevent concurrent module access
		moduleMutex.Lock()
		defer moduleMutex.Unlock()

		// Set mode using Python script
		cmd := exec.Command("python3", projectPath("Module_4G", "set_4g_mode.py"), "set", modeStr)
		output, err := cmd.Output() // Only capture stdout, not stderr

		if err != nil {
			log.Printf("[WEB][4G] Failed to set 4G mode: %v", err)
		}

		// Parse JSON output from Python script
		var result map[string]interface{}
		if err := json.Unmarshal(output, &result); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to parse response from module",
			})
			return
		}

		// Return the result with appropriate status code
		if success, ok := result["success"].(bool); ok && !success {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(result)
	})

	// API endpoint to get current config
	http.HandleFunc("/api/config/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		cfg, err := config.Load("config.yaml")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to load config: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"config":  cfg,
		})
	})

	// API endpoint to update MAVLink connection settings
	http.HandleFunc("/api/config/network/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Method not allowed",
			})
			return
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Invalid JSON",
			})
			return
		}

		// Load current config
		cfg, err := config.Load("config.yaml")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to load config: " + err.Error(),
			})
			return
		}

		// Update network config
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

		// Save config
		if err := cfg.Save("config.yaml"); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Failed to save config: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Network config updated successfully. Please restart the service for changes to take effect.",
			"config":  cfg.Network,
		})
	})

	// Camera is started from main camera manager based on config.Camera.AutoStart.
	// Do not auto-start here to avoid duplicate camera_streamer.py processes.

	// Create HTTP server with optimized settings
	server := &http.Server{
		Addr: fmt.Sprintf("0.0.0.0:%d", port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			http.DefaultServeMux.ServeHTTP(sw, r)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				log.Printf("[WEB][REQ] %s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
			}
		}),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	log.Printf("Starting web server on http://%s", server.Addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web server error: %v", err)
		}
	}()
}

// loadXMLCache loads the PX4 parameter XML file into memory for faster serving
func loadXMLCache() {
	xmlOnce.Do(func() {
		data, err := staticFiles.ReadFile("static/PX4ParameterFactMetaData.xml")
		if err != nil {
			log.Printf("[WEB] Warning: Failed to pre-load XML cache: %v", err)
			xmlContent = []byte{}
		} else {
			xmlContent = data
			log.Printf("[WEB] Pre-loaded PX4ParameterFactMetaData.xml into cache (%d bytes)", len(xmlContent))
		}
	})
}
