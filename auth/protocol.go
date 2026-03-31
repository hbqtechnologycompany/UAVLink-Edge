package auth

import (
	"encoding/binary"
	"fmt"
)

// Message Types
const (
	// UUID-based authentication (PRIMARY)
	MsgAuthInit      = 0x01 // Drone → Server: send UUID only (request challenge)
	MsgAuthChallenge = 0x02 // Server → Drone: send nonce
	MsgAuthResponse  = 0x03 // Drone → Server: send UUID + HMAC (after solving challenge)
	MsgAuthAck       = 0x04 // Server → Drone: auth result (NO session)
	MsgRefresh       = 0x05 // Drone → Server: refresh with UUID (LEGACY)
	MsgRefreshAck    = 0x06 // Server → Drone: refresh result (LEGACY)

	// Session management (separate from auth)
	MsgSessionNew        = 0x10 // Drone → Server: request new/existing session
	MsgSessionAck        = 0x11 // Server → Drone: session token + expires
	MsgSessionRefresh    = 0x12 // Drone → Server: refresh existing session
	MsgSessionRefreshAck = 0x13 // Server → Drone: refresh result

	// API Key management
	MsgAPIKeyRequest    = 0x20 // Drone → Router: request new API key
	MsgAPIKeyResponse   = 0x21 // Router → Drone: API key response
	MsgAPIKeyRevoke     = 0x22 // Drone → Router: revoke API key
	MsgAPIKeyRevokeAck  = 0x23 // Router → Drone: revoke acknowledgement
	MsgAPIKeyStatus     = 0x24 // Drone → Router: get current API key status
	MsgAPIKeyStatusResp = 0x25 // Router → Drone: current API key status
	MsgAPIKeyDelete     = 0x26 // Drone → Router: delete API key completely
	MsgAPIKeyDeleteAck  = 0x27 // Router → Drone: delete acknowledgement

	// Notification messages
	MsgUserConnected    = 0x30 // Router → Drone: user connected
	MsgUserDisconnected = 0x31 // Router → Drone: user disconnected

	// Registration messages
	MsgRegisterInit      uint8 = 0xA0 // 160
	MsgRegisterChallenge uint8 = 0xA1 // 161
	MsgRegisterResponse  uint8 = 0xA2 // 162
	MsgRegisterAck       uint8 = 0xA3 // 163

)

// Result Codes
const (
	ResultSuccess = 0x00
	ResultFailure = 0x01
)

// Error Codes
const (
	ErrInvalidHMAC         = 0x00
	ErrTimestampOutOfRange = 0x01
	ErrUnknownDroneID      = 0x02
	ErrRateLimited         = 0x03
	ErrSessionExpired      = 0x06
	ErrInvalidToken        = 0x07 // Session not found or invalid token
	ErrInternalError       = 0x05
	ErrNotAuthenticated    = 0x10
)

// AuthChallenge represents AUTH_CHALLENGE message from server
type AuthChallenge struct {
	Nonce      []byte
	TimeoutSec uint16
}

// AuthAck represents AUTH_ACK response from server (NO session token in new protocol)
type AuthAck struct {
	Result       byte
	ErrorCode    byte
	WaitSec      uint16
	NewSecretKey string // New field for Auth V2 - secret key provisioning (DEPRECATED by Registration Flow)
	SessionToken string // NEW: Session returned directly in AuthAck
	ExpiresAt    uint64 // NEW: Session expires at
	Interval     uint16 // NEW: Keepalive interval
}

// SessionRequest represents SESSION_NEW message to server
type SessionRequest struct {
	DroneUUID       string // Drone UUID
	OldSessionToken string // Previous session token (optional, for reuse)
}

// SessionAck represents SESSION_ACK response from server
type SessionAck struct {
	Result    byte
	ErrorCode byte
	Token     string // Session token
	ExpiresAt uint64 // Expiration timestamp
	Interval  uint16 // Refresh interval in seconds
}

// SessionRefreshRequest represents SESSION_REFRESH message to server
type SessionRefreshRequest struct {
	SessionToken string
	DroneUUID    string
}

// SessionRefreshAck represents SESSION_REFRESH_ACK response from server
type SessionRefreshAck struct {
	Result    byte
	ErrorCode byte
	ExpiresAt uint64
	Interval  uint16
}

// ============================================================================
// API KEY MANAGEMENT STRUCTURES
// ============================================================================

// APIKeyRequest represents API_KEY_REQUEST message to router
type APIKeyRequest struct {
	DroneUUID       string // Drone UUID
	SessionToken    string // Current session token for verification
	ExpirationHours uint16 // Requested expiration in hours (1-720)
}

// APIKeyResponse represents API_KEY_RESPONSE from router
type APIKeyResponse struct {
	Result    byte   // 0x00 = success, 0x01 = failure
	ErrorCode byte   // Error code if failed
	APIKey    string // Generated API key (only on success)
	ExpiresAt uint64 // Expiration timestamp
}

// APIKeyRevokeRequest represents API_KEY_REVOKE message to router
type APIKeyRevokeRequest struct {
	DroneUUID    string // Drone UUID
	SessionToken string // Current session token for verification
}

// APIKeyRevokeAck represents API_KEY_REVOKE_ACK from router
type APIKeyRevokeAck struct {
	Result    byte // 0x00 = success, 0x01 = failure
	ErrorCode byte // Error code if failed
}

// APIKeyStatusRequest represents API_KEY_STATUS message to router
type APIKeyStatusRequest struct {
	DroneUUID    string // Drone UUID
	SessionToken string // Current session token for verification
}

// APIKeyStatusResponse represents API_KEY_STATUS_RESP from router
type APIKeyStatusResponse struct {
	HasActiveKey    byte   // 0x01 = has active key, 0x00 = no key
	Status          string // "pending", "connected", "expired", "none"
	APIKey          string // Raw API key for display (if has key)
	CreatedAt       uint64 // Creation timestamp (if has key)
	ExpiresAt       uint64 // Expiration timestamp (if has key)
	UserUUID        string // Connected user UUID (if connected)
	UserActivatedAt uint64 // User activation timestamp (if connected)
}

// APIKeyDeleteRequest represents API_KEY_DELETE message to router
type APIKeyDeleteRequest struct {
	DroneUUID    string // Drone UUID
	SessionToken string // Current session token for verification
}

// APIKeyDeleteAck represents API_KEY_DELETE_ACK from router
type APIKeyDeleteAck struct {
	Result    byte // 0x00 = success, 0x01 = failure
	ErrorCode byte // Error code if failed
}

// ============================================================================
// REGISTRATION PROTOCOL STRUCTURES (NEW)
// ============================================================================

// RegisterInit represents REGISTER_INIT packet (UUID only)
type RegisterInit struct {
	DroneUUID string
}

// RegisterChallenge represents REGISTER_CHALLENGE packet (from server)
type RegisterChallenge struct {
	Nonce      []byte
	TimeoutSec uint16
}

// RegisterResponse represents REGISTER_RESPONSE packet (UUID + HMAC with shared_key)
type RegisterResponse struct {
	DroneUUID string
	HMAC      []byte
	Timestamp uint64
}

// RegisterAck represents REGISTER_ACK packet (from server)
type RegisterAck struct {
	Result       byte
	ErrorCode    byte
	SecretKey    string
	SessionToken string
	ExpiresAt    uint64
	Interval     uint16
}

// ============================================================================
// UUID-BASED PROTOCOL STRUCTURES (PRIMARY)
// ============================================================================

// AuthInit represents AUTH_INIT message - just UUID, no HMAC
type AuthInit struct {
	DroneUUID string // UUID string (e.g., "970cbc93-d7df-49dc-8ee0-91c138e7ec98")
}

// AuthResponse represents AUTH_RESPONSE message - UUID + HMAC after challenge
type AuthResponse struct {
	DroneUUID string // UUID string
	HMAC      []byte // HMAC-SHA256 signature
	Timestamp uint64 // Unix timestamp
	IP        string // Optional current IP
}

// ============================================================================
// UUID-BASED SERIALIZATION FUNCTIONS (PRIMARY)
// ============================================================================

// SerializeAuthInit creates AUTH_INIT packet (UUID only, no HMAC)
// Format: [TYPE:1][UUID_LEN:2][UUID:var]
func SerializeAuthInit(init *AuthInit) []byte {
	uuidBytes := []byte(init.DroneUUID)
	packet := make([]byte, 0, 1+2+len(uuidBytes))

	// Message type
	packet = append(packet, MsgAuthInit)

	// UUID length (2 bytes, little-endian)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	return packet
}

// SerializeAuthResponse creates AUTH_RESPONSE packet (after challenge)
// Format: [TYPE:1][UUID_LEN:2][UUID:var][HMAC_LEN:2][HMAC:32][TIMESTAMP:8][IP_LEN:2][IP:var]
func SerializeAuthResponse(resp *AuthResponse) []byte {
	uuidBytes := []byte(resp.DroneUUID)
	ipBytes := []byte(resp.IP)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(resp.HMAC)+8+2+len(ipBytes))

	// Message type
	packet = append(packet, MsgAuthResponse)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// HMAC length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(resp.HMAC)))
	packet = append(packet, buf...)

	// HMAC
	packet = append(packet, resp.HMAC...)

	// Timestamp (8 bytes)
	buf = make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, resp.Timestamp)
	packet = append(packet, buf...)

	// IP length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(ipBytes)))
	packet = append(packet, buf...)

	// IP
	packet = append(packet, ipBytes...)

	return packet
}

// ParseAuthChallenge parses AUTH_CHALLENGE response
func ParseAuthChallenge(data []byte) (*AuthChallenge, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgAuthChallenge {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAuthChallenge)
	}

	offset := 1

	// Nonce length (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for nonce length")
	}
	nonceLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Nonce data
	if len(data) < offset+int(nonceLen) {
		return nil, fmt.Errorf("packet too short for nonce data")
	}
	nonce := make([]byte, nonceLen)
	copy(nonce, data[offset:offset+int(nonceLen)])
	offset += int(nonceLen)

	// Timeout (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for timeout")
	}
	timeoutSec := binary.LittleEndian.Uint16(data[offset : offset+2])

	return &AuthChallenge{
		Nonce:      nonce,
		TimeoutSec: timeoutSec,
	}, nil
}

// ParseAuthAck parses AUTH_ACK response with session token
// Format: [TYPE:1][RESULT:1][SESSION_TOKEN_LEN:2][SESSION_TOKEN:var][EXPIRES_AT:8][INTERVAL:2] (success)
// Or: [TYPE:1][RESULT:1][ERROR_CODE:1] (failure)
func ParseAuthAck(data []byte) (*AuthAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgAuthAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAuthAck)
	}

	offset := 1
	ack := &AuthAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	if ack.Result != ResultSuccess {
		// Error case: [TYPE:1][RESULT:1][ERROR_CODE:1]
		if len(data) >= offset+1 {
			ack.ErrorCode = data[offset]
			offset++
		}

		if len(data) >= offset+2 {
			ack.WaitSec = binary.LittleEndian.Uint16(data[offset : offset+2])
		}
	} else {
		// Success case: [TYPE:1][RESULT:1][SESSION_TOKEN_LEN:2][SESSION_TOKEN:var][EXPIRES_AT:8][INTERVAL:2]

		// SESSION_TOKEN_LEN (2 bytes)
		if len(data) < offset+2 {
			return nil, fmt.Errorf("packet too short for session token length")
		}
		tokenLen := binary.LittleEndian.Uint16(data[offset : offset+2])
		offset += 2

		// SESSION_TOKEN (var)
		if len(data) < offset+int(tokenLen) {
			return nil, fmt.Errorf("packet too short for session token")
		}
		ack.SessionToken = string(data[offset : offset+int(tokenLen)])
		offset += int(tokenLen)

		// EXPIRES_AT (8 bytes)
		if len(data) < offset+8 {
			return nil, fmt.Errorf("packet too short for expires_at")
		}
		ack.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
		offset += 8

		// INTERVAL (2 bytes)
		if len(data) < offset+2 {
			return nil, fmt.Errorf("packet too short for interval")
		}
		ack.Interval = binary.LittleEndian.Uint16(data[offset : offset+2])
	}

	return ack, nil
}

// ============================================================================
// SESSION MANAGEMENT SERIALIZATION/PARSING
// ============================================================================

// SerializeSessionRequest creates SESSION_NEW packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][OLD_TOKEN_LEN:2][OLD_TOKEN:var]
func SerializeSessionRequest(req *SessionRequest) []byte {
	uuidBytes := []byte(req.DroneUUID)
	oldTokenBytes := []byte(req.OldSessionToken)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(oldTokenBytes))

	// Message type
	packet = append(packet, MsgSessionNew)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// Old token length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(oldTokenBytes)))
	packet = append(packet, buf...)

	// Old token
	packet = append(packet, oldTokenBytes...)

	return packet
}

// ParseSessionAck parses SESSION_ACK response
func ParseSessionAck(data []byte) (*SessionAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgSessionAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgSessionAck)
	}

	offset := 1
	ack := &SessionAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	if ack.Result != ResultSuccess {
		// Error case
		if len(data) >= offset+1 {
			ack.ErrorCode = data[offset]
		}
		return ack, nil
	}

	// Success case - parse token
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for token length")
	}
	tokenLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	if len(data) < offset+int(tokenLen) {
		return nil, fmt.Errorf("packet too short for token data")
	}
	ack.Token = string(data[offset : offset+int(tokenLen)])
	offset += int(tokenLen)

	// Expires at (8 bytes)
	if len(data) < offset+8 {
		return nil, fmt.Errorf("packet too short for expires_at")
	}
	ack.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8

	// Interval (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for interval")
	}
	ack.Interval = binary.LittleEndian.Uint16(data[offset : offset+2])

	return ack, nil
}

// SerializeSessionRefresh creates SESSION_REFRESH packet
// Format: [TYPE:1][TOKEN_LEN:2][TOKEN:var][UUID_LEN:2][UUID:var]
func SerializeSessionRefresh(req *SessionRefreshRequest) []byte {
	tokenBytes := []byte(req.SessionToken)
	uuidBytes := []byte(req.DroneUUID)
	packet := make([]byte, 0, 1+2+len(tokenBytes)+2+len(uuidBytes))

	// Message type
	packet = append(packet, MsgSessionRefresh)

	// Token length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
	packet = append(packet, buf...)

	// Token
	packet = append(packet, tokenBytes...)

	// UUID length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	return packet
}

// ParseSessionRefreshAck parses SESSION_REFRESH_ACK response
func ParseSessionRefreshAck(data []byte) (*SessionRefreshAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgSessionRefreshAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgSessionRefreshAck)
	}

	offset := 1
	ack := &SessionRefreshAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	if ack.Result != ResultSuccess {
		// Error case
		if len(data) >= offset+1 {
			ack.ErrorCode = data[offset]
		}
		return ack, nil
	}

	// Success case - parse expires_at
	if len(data) < offset+8 {
		return nil, fmt.Errorf("packet too short for expires_at")
	}
	ack.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8

	// Interval (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for interval")
	}
	ack.Interval = binary.LittleEndian.Uint16(data[offset : offset+2])

	return ack, nil
}

// ============================================================================
// REGISTRATION PROTOCOL SERIALIZATION (NEW)
// ============================================================================

// SerializeRegisterInit creates REGISTER_INIT packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var]
func SerializeRegisterInit(init *RegisterInit) []byte {
	uuidBytes := []byte(init.DroneUUID)
	packet := make([]byte, 0, 1+2+len(uuidBytes))

	// Message type
	packet = append(packet, MsgRegisterInit)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	return packet
}

// ParseRegisterChallenge parses REGISTER_CHALLENGE packet
func ParseRegisterChallenge(data []byte) (*RegisterChallenge, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgRegisterChallenge {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgRegisterChallenge)
	}

	offset := 1

	// Nonce length (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for nonce length")
	}
	nonceLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Nonce data
	if len(data) < offset+int(nonceLen) {
		return nil, fmt.Errorf("packet too short for nonce data")
	}
	nonce := make([]byte, nonceLen)
	copy(nonce, data[offset:offset+int(nonceLen)])
	offset += int(nonceLen)

	// Timeout (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for timeout")
	}
	timeoutSec := binary.LittleEndian.Uint16(data[offset : offset+2])

	return &RegisterChallenge{
		Nonce:      nonce,
		TimeoutSec: timeoutSec,
	}, nil
}

// SerializeRegisterResponse creates REGISTER_RESPONSE packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][HMAC_LEN:2][HMAC:32][TIMESTAMP:8]
func SerializeRegisterResponse(resp *RegisterResponse) []byte {
	uuidBytes := []byte(resp.DroneUUID)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(resp.HMAC)+8)

	// Message type
	packet = append(packet, MsgRegisterResponse)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// HMAC length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(resp.HMAC)))
	packet = append(packet, buf...)

	// HMAC
	packet = append(packet, resp.HMAC...)

	// Timestamp (8 bytes)
	buf = make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, resp.Timestamp)
	packet = append(packet, buf...)

	return packet
}

// ParseRegisterAck parses REGISTER_ACK packet
// Format: [TYPE:1][RESULT:1][SECRET_KEY_LEN:2][SECRET_KEY:var][SESSION_TOKEN_LEN:2][SESSION_TOKEN:var][EXPIRES_AT:8][INTERVAL:2]
func ParseRegisterAck(data []byte) (*RegisterAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgRegisterAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgRegisterAck)
	}

	offset := 1
	ack := &RegisterAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	if ack.Result != ResultSuccess {
		// Error case
		if len(data) >= offset+1 {
			ack.ErrorCode = data[offset]
		}
		return ack, nil
	}

	// Helper to safely read string with 2-byte length prefix
	readString := func() (string, error) {
		if len(data) < offset+2 {
			return "", fmt.Errorf("buffer too short for length prefix")
		}
		length := binary.LittleEndian.Uint16(data[offset : offset+2])
		offset += 2
		if len(data) < offset+int(length) {
			return "", fmt.Errorf("buffer too short for string data")
		}
		str := string(data[offset : offset+int(length)])
		offset += int(length)
		return str, nil
	}

	var err error

	// SECRET_KEY
	ack.SecretKey, err = readString()
	if err != nil {
		return nil, fmt.Errorf("failed to read secret key: %w", err)
	}

	// SESSION_TOKEN
	ack.SessionToken, err = readString()
	if err != nil {
		return nil, fmt.Errorf("failed to read session token: %w", err)
	}

	// EXPIRES_AT (8 bytes)
	if len(data) < offset+8 {
		return nil, fmt.Errorf("packet too short for expires_at")
	}
	ack.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8

	// INTERVAL (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for interval")
	}
	ack.Interval = binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	return ack, nil
}

// ============================================================================
// API KEY PROTOCOL SERIALIZATION/PARSING
// ============================================================================

// SerializeAPIKeyRequest creates API_KEY_REQUEST packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][TOKEN_LEN:2][TOKEN:var][EXPIRATION:2]
func SerializeAPIKeyRequest(req *APIKeyRequest) []byte {
	uuidBytes := []byte(req.DroneUUID)
	tokenBytes := []byte(req.SessionToken)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(tokenBytes)+2)

	// Message type
	packet = append(packet, MsgAPIKeyRequest)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// Token length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
	packet = append(packet, buf...)

	// Token
	packet = append(packet, tokenBytes...)

	// Expiration hours (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, req.ExpirationHours)
	packet = append(packet, buf...)

	return packet
}

// ParseAPIKeyResponse parses API_KEY_RESPONSE from router
// Supports both old format [TYPE:1]... and new format [LENGTH:2][TYPE:1]...
func ParseAPIKeyResponse(data []byte) (*APIKeyResponse, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	// Check if new format with length field
	if len(data) >= 3 && data[2] == MsgAPIKeyResponse {
		// New format: [LENGTH:2][TYPE:1]...
		payloadLength := binary.LittleEndian.Uint16(data[0:2])
		if len(data) < int(payloadLength)+2 {
			return nil, fmt.Errorf("packet incomplete, expected %d bytes, got %d", payloadLength+2, len(data))
		}
		offset := 2
		return parseAPIKeyResponseFromOffset(data, offset)
	} else if data[0] == MsgAPIKeyResponse {
		// Old format: [TYPE:1]...
		offset := 0
		return parseAPIKeyResponseFromOffset(data, offset)
	} else {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAPIKeyResponse)
	}
}

// parseAPIKeyResponseFromOffset parses from given offset
func parseAPIKeyResponseFromOffset(data []byte, offset int) (*APIKeyResponse, error) {
	// TYPE
	if data[offset] != MsgAPIKeyResponse {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[offset], MsgAPIKeyResponse)
	}
	offset++

	// RESULT (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	result := data[offset]
	offset++

	// ERROR_CODE (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for error code")
	}
	errorCode := data[offset]
	offset++

	resp := &APIKeyResponse{
		Result:    result,
		ErrorCode: errorCode,
	}

	// If success, parse API key
	if result == 0x00 {
		// KEY_LEN (2 bytes)
		if len(data) < offset+2 {
			return nil, fmt.Errorf("packet too short for key length")
		}
		keyLen := binary.LittleEndian.Uint16(data[offset : offset+2])
		offset += 2

		// KEY
		if len(data) < offset+int(keyLen) {
			return nil, fmt.Errorf("packet too short for key data")
		}
		resp.APIKey = string(data[offset : offset+int(keyLen)])
		offset += int(keyLen)

		// EXPIRES_AT (8 bytes)
		if len(data) < offset+8 {
			return nil, fmt.Errorf("packet too short for expires_at")
		}
		resp.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
	}

	return resp, nil
}

// SerializeAPIKeyRevoke creates API_KEY_REVOKE packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][TOKEN_LEN:2][TOKEN:var]
func SerializeAPIKeyRevoke(req *APIKeyRevokeRequest) []byte {
	uuidBytes := []byte(req.DroneUUID)
	tokenBytes := []byte(req.SessionToken)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(tokenBytes))

	// Message type
	packet = append(packet, MsgAPIKeyRevoke)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// Token length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
	packet = append(packet, buf...)

	// Token
	packet = append(packet, tokenBytes...)

	return packet
}

// ParseAPIKeyRevokeAck parses API_KEY_REVOKE_ACK from router
func ParseAPIKeyRevokeAck(data []byte) (*APIKeyRevokeAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgAPIKeyRevokeAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAPIKeyRevokeAck)
	}

	offset := 1
	ack := &APIKeyRevokeAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	if ack.Result != ResultSuccess && len(data) >= offset+1 {
		ack.ErrorCode = data[offset]
	}

	return ack, nil
}

// SerializeAPIKeyStatus creates API_KEY_STATUS packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][TOKEN_LEN:2][TOKEN:var]
func SerializeAPIKeyStatus(req *APIKeyStatusRequest) []byte {
	uuidBytes := []byte(req.DroneUUID)
	tokenBytes := []byte(req.SessionToken)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(tokenBytes))

	// Message type
	packet = append(packet, MsgAPIKeyStatus)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// Token length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
	packet = append(packet, buf...)

	// Token
	packet = append(packet, tokenBytes...)

	return packet
}

// ParseAPIKeyStatusResponse parses API_KEY_STATUS_RESP from router
// Format: [TYPE:1][HAS_KEY:1][STATUS_LEN:2][STATUS:var][API_KEY_LEN:2][API_KEY:var][...optional fields...]
func ParseAPIKeyStatusResponse(data []byte) (*APIKeyStatusResponse, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgAPIKeyStatusResp {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAPIKeyStatusResp)
	}

	offset := 1
	resp := &APIKeyStatusResponse{}

	// Has active key (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for has_active_key")
	}
	resp.HasActiveKey = data[offset]
	offset++

	// Status length (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for status length")
	}
	statusLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Status
	if len(data) < offset+int(statusLen) {
		return nil, fmt.Errorf("packet too short for status data")
	}
	resp.Status = string(data[offset : offset+int(statusLen)])
	offset += int(statusLen)

	// API Key length (2 bytes)
	if len(data) < offset+2 {
		return nil, fmt.Errorf("packet too short for api_key length")
	}
	apiKeyLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// API Key
	if len(data) < offset+int(apiKeyLen) {
		return nil, fmt.Errorf("packet too short for api_key data")
	}
	resp.APIKey = string(data[offset : offset+int(apiKeyLen)])
	offset += int(apiKeyLen)

	// If has active key, parse additional fields
	if resp.HasActiveKey == 0x01 {
		// Created at (8 bytes)
		if len(data) >= offset+8 {
			resp.CreatedAt = binary.LittleEndian.Uint64(data[offset : offset+8])
			offset += 8
		}

		// Expires at (8 bytes)
		if len(data) >= offset+8 {
			resp.ExpiresAt = binary.LittleEndian.Uint64(data[offset : offset+8])
			offset += 8
		}

		// User UUID length (2 bytes)
		if len(data) >= offset+2 {
			userLen := binary.LittleEndian.Uint16(data[offset : offset+2])
			offset += 2
			if len(data) >= offset+int(userLen) {
				resp.UserUUID = string(data[offset : offset+int(userLen)])
				offset += int(userLen)
			}
		}

		// User activated at (8 bytes)
		if len(data) >= offset+8 {
			resp.UserActivatedAt = binary.LittleEndian.Uint64(data[offset : offset+8])
		}
	}

	return resp, nil
}

// SerializeAPIKeyDelete creates API_KEY_DELETE packet
// Format: [TYPE:1][UUID_LEN:2][UUID:var][TOKEN_LEN:2][TOKEN:var]
func SerializeAPIKeyDelete(req *APIKeyDeleteRequest) []byte {
	uuidBytes := []byte(req.DroneUUID)
	tokenBytes := []byte(req.SessionToken)
	packet := make([]byte, 0, 1+2+len(uuidBytes)+2+len(tokenBytes))

	// Message type
	packet = append(packet, MsgAPIKeyDelete)

	// UUID length (2 bytes)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(uuidBytes)))
	packet = append(packet, buf...)

	// UUID
	packet = append(packet, uuidBytes...)

	// Token length (2 bytes)
	buf = make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(len(tokenBytes)))
	packet = append(packet, buf...)

	// Token
	packet = append(packet, tokenBytes...)

	return packet
}

// ParseAPIKeyDeleteAck parses API_KEY_DELETE_ACK from router
func ParseAPIKeyDeleteAck(data []byte) (*APIKeyDeleteAck, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("packet too short")
	}

	if data[0] != MsgAPIKeyDeleteAck {
		return nil, fmt.Errorf("invalid message type: 0x%02x (expected 0x%02x)", data[0], MsgAPIKeyDeleteAck)
	}

	offset := 1
	ack := &APIKeyDeleteAck{}

	// Result (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for result")
	}
	ack.Result = data[offset]
	offset++

	// Error code (1 byte)
	if len(data) < offset+1 {
		return nil, fmt.Errorf("packet too short for error_code")
	}
	ack.ErrorCode = data[offset]

	return ack, nil
}

