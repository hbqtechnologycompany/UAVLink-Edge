package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ComputeHMAC computes HMAC-SHA256 signature (UUID-based)
// Message format: "DroneUUID:NonceHex:Timestamp"
func ComputeHMAC(secret string, droneUUID string, nonce []byte, timestamp uint64) []byte {
	nonceHex := hex.EncodeToString(nonce)
	message := fmt.Sprintf("%s:%s:%d", droneUUID, nonceHex, timestamp)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return h.Sum(nil)
}

// VerifyHMAC verifies HMAC signature (UUID-based)
func VerifyHMAC(secret string, droneUUID string, nonce []byte, timestamp uint64, signature []byte) bool {
	expected := ComputeHMAC(secret, droneUUID, nonce, timestamp)
	return hmac.Equal(expected, signature)
}
