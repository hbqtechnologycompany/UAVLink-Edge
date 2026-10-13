package mavlink_custom

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/bluenviron/gomavlib/v3/pkg/frame"
	"github.com/bluenviron/gomavlib/v3/pkg/message"
)

// SessionHeartbeatMsgID matches PX4_Mavlink_Drone_Simulator (MAVLink session sync).
const SessionHeartbeatMsgID uint32 = 42999

// sessionHeartbeatCRCExtra matches simulator manual frame build (crc extra = 0).
const sessionHeartbeatCRCExtra byte = 0

// BuildSessionHeartbeatPayload — layout giống simulator connection.go:
// expires_at(u32) | sequence(u16) | pixhawk_active(u8) | SHA256(token_string)(32)
func BuildSessionHeartbeatPayload(token string, expiresAt uint32, sequence uint16, pixhawkActive uint8) []byte {
	tokenHash := sha256.Sum256([]byte(token))
	payload := make([]byte, 39)
	binary.LittleEndian.PutUint32(payload[0:4], expiresAt)
	binary.LittleEndian.PutUint16(payload[4:6], sequence)
	payload[6] = pixhawkActive
	copy(payload[7:39], tokenHash[:])
	return payload
}

// NewSessionHeartbeatFrame builds a MAVLink v2 frame (msg 42999) compatible with fleet router / simulator.
func NewSessionHeartbeatFrame(sysID, compID, seqNum byte, token string, expiresAt uint32, sequence uint16, pixhawkActive uint8) *frame.V2Frame {
	payload := BuildSessionHeartbeatPayload(token, expiresAt, sequence, pixhawkActive)
	fr := &frame.V2Frame{
		SequenceNumber: seqNum,
		SystemID:       sysID,
		ComponentID:    compID,
		Message: &message.MessageRaw{
			ID:      SessionHeartbeatMsgID,
			Payload: payload,
		},
	}
	fr.Checksum = fr.GenerateChecksum(sessionHeartbeatCRCExtra)
	return fr
}
