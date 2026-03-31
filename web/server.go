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
	"regexp"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"UAVLink-Edge/auth"
	"UAVLink-Edge/config"
	"UAVLink-Edge/metrics"
)

//go:embed static/*
var staticFiles embed.FS

// XML content cache for parameter editor
var xmlContent []byte
var xmlOnce sync.Once

// Mutex to prevent concurrent 4G module access
var moduleMutex sync.Mutex

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// processLogWriter forwards external process output through stdlib logger
// so logger scope filters can decide whether to emit these lines.
type processLogWriter struct {
	prefix string
	mu     sync.Mutex
	carry  string
}

func (w *processLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	chunk := w.carry + string(p)
	lines := strings.Split(chunk, "\n")
	for i := 0; i < len(lines)-1; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		log.Printf("%s %s", w.prefix, line)
	}

	w.carry = lines[len(lines)-1]
	return len(p), nil
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

// startCameraStreamer removed

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

func (b *MAVLinkBridge) processParamValues() {
	for msg := range b.paramValueCh {
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

		b.paramCache[msg.ParamId] = CachedParameter{
			ParamId:    msg.ParamId,
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

	err := b.node.WriteMessageAll(msg)
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

	param, exists := b.paramCache[paramName]
	return param, exists
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

	err := b.node.WriteMessageAll(paramMsg)
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
			param, exists := b.paramCache[paramName]
			lastUpdate := b.paramLastUpdate
			b.paramCacheMutex.RUnlock()

			// Check if we got an update after sending the request
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
		// Set cache headers for static files
		w.Header().Set("Cache-Control", "public, max-age=3600")
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
		json.NewEncoder(w).Encode(metrics.Global.GetSnapshot())
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

	// Health check endpoint
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
		})
	})

	// API endpoint to get real-time network info
	http.HandleFunc("/api/network/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		// Read connection status file
		statusFile := projectPath("data", "connection_status.json")
		statusData, err := os.ReadFile(statusFile)

		var networkInfo map[string]interface{}

		if err == nil {
			json.Unmarshal(statusData, &networkInfo)
		} else {
			// Return default structure if file doesn't exist
			networkInfo = map[string]interface{}{
				"4g":               map[string]interface{}{"status": "unavailable"},
				"wifi":             map[string]interface{}{"status": "unavailable"},
				"ethernet":         map[string]interface{}{"status": "unavailable"},
				"active_interface": nil,
				"timestamp":        time.Now().Unix(),
			}
		}

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

	// API endpoint to set/get network priority
	http.HandleFunc("/api/network/priority", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setCORSHeaders(w)

		if r.Method == http.MethodOptions {
			return
		}

		if r.Method == http.MethodPost {
			// Set priority
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

			// Run connection_manager.py to set priority
			cmd := exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "priority", priority)
			if err := cmd.Run(); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": "Failed to set priority",
				})
				return
			}

			// Trigger reconnect with new priority
			go func() {
				time.Sleep(1 * time.Second)
				exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "once").Run()
			}()

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"message":  "Priority set to " + priority,
				"priority": priority,
			})
		} else {
			// Get priority
			configData, err := os.ReadFile("/home/pi/connection_config.json")
			var config map[string]interface{}

			if err == nil {
				json.Unmarshal(configData, &config)
			} else {
				config = map[string]interface{}{"priority": "4g"}
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"priority": config["priority"],
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
			cmd := exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "once")
			if err := cmd.Run(); err != nil {
				log.Printf("[WEB][4G] Failed to trigger reconnection: %v", err)
			}
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

		go func(targetNet string) {
			cmdPriority := exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "priority", targetNet)
			if err := cmdPriority.Run(); err != nil {
				log.Printf("[WEB][NET] Failed to set priority to %s: %v", targetNet, err)
				return
			}

			time.Sleep(300 * time.Millisecond)

			cmdReconnect := exec.Command("python3", projectPath("Module_4G", "connection_manager.py"), "once")
			if err := cmdReconnect.Run(); err != nil {
				log.Printf("[WEB][NET] Failed to switch network to %s: %v", targetNet, err)
			}
		}(target)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Network switch triggered",
			"target":  target,
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
// setCORSHeaders sets standard CORS headers for API responses
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
}

// projectPath resolves a path relative to the project root
func projectPath(parts ...string) string {
	return filepath.Join(append([]string{"."}, parts...)...)
}

// get4GSignalInfo returns current 4G signal strength and quality metrics
func get4GSignalInfo() map[string]interface{} {
	// Default mock values if no module is found or error occurs
	info := map[string]interface{}{
		"signal": 0,
		"rsrp":   0,
		"rsrq":   0,
		"rssi":   0,
		"mode":   "unknown",
	}

	// Try to get real signal info using Python script if it exists
	cmd := exec.Command("python3", projectPath("Module_4G", "set_4g_mode.py"), "get")
	output, err := cmd.Output()
	if err == nil {
		var result map[string]interface{}
		if err := json.Unmarshal(output, &result); err == nil {
			if success, ok := result["success"].(bool); ok && success {
				if signal, ok := result["signal"].(float64); ok {
					info["signal"] = int(signal)
				}
				if mode, ok := result["mode"].(string); ok {
					info["mode"] = mode
				}
				// Extract other metrics if available
				for _, k := range []string{"rsrp", "rsrq", "rssi"} {
					if v, ok := result[k].(float64); ok {
						info[k] = int(v)
					}
				}
			}
		}
	}

	return info
}
