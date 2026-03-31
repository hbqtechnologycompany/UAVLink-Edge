package auth

import (
	"fmt"
	"log"
	"time"

	"UAVLink-Edge/metrics"
)

// RequestAPIKey requests a new API key from the router with specified expiration
func (c *Client) RequestAPIKey(expirationHours int) (*APIKeyResponse, error) {
	c.tcpMu.Lock() // 🔒 Lock only for sending

	c.mu.RLock()
	token := c.sessionToken
	conn := c.conn
	running := c.running
	c.mu.RUnlock()

	if !running {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("auth client not running")
	}

	if token == "" {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("no active session")
	}

	if conn == nil {
		// Try to reconnect
		if err := c.reconnectTCP(); err != nil {
			c.tcpMu.Unlock()
			return nil, fmt.Errorf("connection lost and reconnect failed: %w", err)
		}
		c.mu.RLock()
		conn = c.conn
		c.mu.RUnlock()
	}

	// Clamp expiration hours
	if expirationHours < 1 {
		expirationHours = 1
	}
	if expirationHours > 720 {
		expirationHours = 720
	}

	// Send API_KEY_REQUEST
	req := &APIKeyRequest{
		DroneUUID:       c.droneUUID,
		SessionToken:    token,
		ExpirationHours: uint16(expirationHours),
	}

	packet := SerializeAPIKeyRequest(req)
	if _, err := conn.Write(packet); err != nil {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("failed to send API_KEY_REQUEST: %w", err)
	}
	log.Printf("[API_KEY] ✓ Sent API_KEY_REQUEST (expiration: %d hours)", expirationHours)

	// Read API_KEY_RESPONSE with short timeout before releasing lock
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // Reset deadline

	c.tcpMu.Unlock() // Release lock immediately after reading

	if err != nil {
		// Timeout or read error - return graceful error
		log.Printf("[API_KEY] ⏱️ No immediate response (this is OK, backend is processing)")
		return nil, fmt.Errorf("timeout waiting for API_KEY_RESPONSE")
	}

	resp, err := ParseAPIKeyResponse(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse API_KEY_RESPONSE: %w", err)
	}

	if resp.Result != ResultSuccess {
		return resp, fmt.Errorf("API key request failed (error code: 0x%02x)", resp.ErrorCode)
	}

	log.Printf("[API_KEY] ✅ Received API key (expires: %s)",
		time.Unix(int64(resp.ExpiresAt), 0).Format("2006-01-02 15:04:05"))
	metrics.Global.AddLog("INFO", "API key generated successfully")
	return resp, nil
}

// RevokeAPIKey revokes the current API key via TCP auth connection
func (c *Client) RevokeAPIKey() error {
	c.tcpMu.Lock() // 🔒 Lock only for sending

	c.mu.RLock()
	token := c.sessionToken
	conn := c.conn
	running := c.running
	c.mu.RUnlock()

	if !running {
		c.tcpMu.Unlock()
		return fmt.Errorf("auth client not running")
	}

	if token == "" {
		c.tcpMu.Unlock()
		return fmt.Errorf("no active session")
	}

	if conn == nil {
		if err := c.reconnectTCP(); err != nil {
			c.tcpMu.Unlock()
			return fmt.Errorf("connection lost and reconnect failed: %w", err)
		}
		c.mu.RLock()
		conn = c.conn
		c.mu.RUnlock()
	}

	// Send API_KEY_REVOKE
	req := &APIKeyRevokeRequest{
		DroneUUID:    c.droneUUID,
		SessionToken: token,
	}

	packet := SerializeAPIKeyRevoke(req)
	if _, err := conn.Write(packet); err != nil {
		c.tcpMu.Unlock()
		return fmt.Errorf("failed to send API_KEY_REVOKE: %w", err)
	}
	log.Printf("[API_KEY] ✓ Sent API_KEY_REVOKE")

	// Read API_KEY_REVOKE_ACK with short timeout before releasing lock
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // Reset deadline

	c.tcpMu.Unlock() // Release lock immediately after reading

	if err != nil {
		log.Printf("[API_KEY] ⏱️ No immediate response (this is OK)")
		return fmt.Errorf("timeout waiting for API_KEY_REVOKE_ACK")
	}

	ack, err := ParseAPIKeyRevokeAck(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse API_KEY_REVOKE_ACK: %w", err)
	}

	if ack.Result != ResultSuccess {
		return fmt.Errorf("API key revoke failed (error code: 0x%02x)", ack.ErrorCode)
	}

	log.Printf("[API_KEY] ✅ API key revoked successfully")
	metrics.Global.AddLog("INFO", "API key revoked successfully")
	return nil
}

// GetAPIKeyStatus gets the current API key status via TCP auth connection
func (c *Client) GetAPIKeyStatus() (*APIKeyStatusResponse, error) {
	c.tcpMu.Lock() // 🔒 Lock only for sending

	c.mu.RLock()
	token := c.sessionToken
	conn := c.conn
	running := c.running
	c.mu.RUnlock()

	if !running {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("auth client not running")
	}

	if token == "" {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("no active session")
	}

	if conn == nil {
		if err := c.reconnectTCP(); err != nil {
			c.tcpMu.Unlock()
			return nil, fmt.Errorf("connection lost and reconnect failed: %w", err)
		}
		c.mu.RLock()
		conn = c.conn
		c.mu.RUnlock()
	}

	// Send API_KEY_STATUS
	req := &APIKeyStatusRequest{
		DroneUUID:    c.droneUUID,
		SessionToken: token,
	}

	packet := SerializeAPIKeyStatus(req)
	if _, err := conn.Write(packet); err != nil {
		c.tcpMu.Unlock()
		return nil, fmt.Errorf("failed to send API_KEY_STATUS: %w", err)
	}
	log.Printf("[API_KEY] ✓ Sent API_KEY_STATUS request")

	// Read API_KEY_STATUS_RESP with short timeout before releasing lock
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // Reset deadline

	c.tcpMu.Unlock() // Release lock immediately after reading

	if err != nil {
		log.Printf("[API_KEY] ⏱️ No immediate response (this is OK, backend is processing)")
		return nil, fmt.Errorf("timeout waiting for API_KEY_STATUS_RESP")
	}

	resp, err := ParseAPIKeyStatusResponse(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse API_KEY_STATUS_RESP: %w", err)
	}

	log.Printf("[API_KEY] ✓ Received API key status: %s", resp.Status)
	return resp, nil
}

// DeleteAPIKey completely deletes the API key from database via TCP auth connection
func (c *Client) DeleteAPIKey() error {
	c.tcpMu.Lock() // 🔒 Lock only for sending

	c.mu.RLock()
	token := c.sessionToken
	conn := c.conn
	running := c.running
	c.mu.RUnlock()

	if !running {
		c.tcpMu.Unlock()
		return fmt.Errorf("auth client not running")
	}

	if token == "" {
		c.tcpMu.Unlock()
		return fmt.Errorf("no active session")
	}

	if conn == nil {
		if err := c.reconnectTCP(); err != nil {
			c.tcpMu.Unlock()
			return fmt.Errorf("connection lost and reconnect failed: %w", err)
		}
		c.mu.RLock()
		conn = c.conn
		c.mu.RUnlock()
	}

	// Send API_KEY_DELETE
	req := &APIKeyDeleteRequest{
		DroneUUID:    c.droneUUID,
		SessionToken: token,
	}

	packet := SerializeAPIKeyDelete(req)
	if _, err := conn.Write(packet); err != nil {
		c.tcpMu.Unlock()
		return fmt.Errorf("failed to send API_KEY_DELETE: %w", err)
	}
	log.Printf("[API_KEY] ✓ Sent API_KEY_DELETE")

	// Read API_KEY_DELETE_ACK with short timeout before releasing lock
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // Reset deadline

	c.tcpMu.Unlock() // Release lock immediately after reading

	if err != nil {
		log.Printf("[API_KEY] ⏱️ No immediate response (this is OK)")
		return fmt.Errorf("timeout waiting for API_KEY_DELETE_ACK")
	}

	ack, err := ParseAPIKeyDeleteAck(buf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse API_KEY_DELETE_ACK: %w", err)
	}

	if ack.Result != ResultSuccess {
		return fmt.Errorf("API key delete failed (error code: 0x%02x)", ack.ErrorCode)
	}

	log.Printf("[API_KEY] ✅ API key deleted successfully")
	metrics.Global.AddLog("INFO", "API key deleted successfully")
	return nil
}
