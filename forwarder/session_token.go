package forwarder

import (
	"encoding/hex"
	"fmt"
)

func hexDecodeSessionToken(tokenStr string) ([32]byte, error) {
	var out [32]byte
	decoded, err := hex.DecodeString(tokenStr)
	if err != nil || len(decoded) != 32 {
		return out, fmt.Errorf("legacy session token must be 64-char hex (len=%d): %w", len(decoded), err)
	}
	copy(out[:], decoded)
	return out, nil
}
