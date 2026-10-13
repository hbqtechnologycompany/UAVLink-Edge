package mavlink_custom

import (
	"github.com/bluenviron/gomavlib/v3/pkg/dialect"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/all"
	"github.com/bluenviron/gomavlib/v3/pkg/message"
)

// MessageMavlinkKeepAlive is a custom MAVLink message for session token synchronization
// Message ID: 42999 (Changed from 42000 to avoid conflicts)
type MessageMavlinkKeepAlive struct {
	Token            [32]byte // Session token (32 bytes binary)
	ExpiresAt        uint32   // Session expiration timestamp (Unix time)
	Sequence         uint16   // Sequence number for tracking
	PixhawkConnected uint8    // Pixhawk connection status (0=disconnected, 1=connected)
}

// GetID implements the Message interface
func (*MessageMavlinkKeepAlive) GetID() uint32 {
	return 42999
}

// GpsDiagnosis — mã chẩn đoán GPS cho QCC (msg DRONEBRIDGE_STATUS).
const (
	GpsDiagUnknown       uint8 = 0
	GpsDiagNoPx4Stream   uint8 = 1 // PX4 không gửi GPS_RAW → đừng suy GPS từ LocalPosition
	GpsDiagPx4NoFix      uint8 = 2 // PX4 gửi GPS_RAW fix=0 (chưa lắp GPS / trong nhà)
	GpsDiagPx4Ok         uint8 = 3 // PX4 báo fix 3D+
	GpsDiagPx4LocalOnly  uint8 = 4 // Có LocalPosition/Altitude nhưng không có GPS_RAW
)

// MessageDronebridgeStatus — telemetry rõ nguồn từ CM5 companion (không phải PX4).
// Message ID: 42998
type MessageDronebridgeStatus struct {
	TimestampMs     uint32
	GpsFixType      uint8 // 255 = PX4 chưa gửi GPS_RAW
	GpsSatellites   uint8
	GpsPx4Streaming uint8 // 1 = đã nhận GPS_RAW từ PX4 trong 5s
	GpsDiagnosis    uint8 // GpsDiag*
	Camera0Live     uint8
	Camera1Live     uint8
	Reserved        uint8
}

func (*MessageDronebridgeStatus) GetID() uint32 {
	return 42998
}

func appendCustomMessages(msgs []message.Message) []message.Message {
	ids := map[uint32]bool{42998: true, 42999: true}
	for _, msg := range msgs {
		ids[msg.GetID()] = false
	}
	out := make([]message.Message, len(msgs))
	copy(out, msgs)
	if ids[42998] {
		out = append(out, &MessageDronebridgeStatus{})
	}
	if ids[42999] {
		out = append(out, &MessageMavlinkKeepAlive{})
	}
	return out
}

// GetCombinedDialect creates a dialect that includes both all standard and custom messages
func GetCombinedDialect() *dialect.Dialect {
	// First, check if our ID is already in all.Dialect (extremely unlikely for 42999)
	for _, msg := range all.Dialect.Messages {
		if msg.GetID() == 42999 {
			return all.Dialect // Already exists, just return all
		}
	}

	// Create a NEW slice to avoid modifying the original all.Dialect global slice
	allMsgs := make([]message.Message, len(all.Dialect.Messages))
	copy(allMsgs, all.Dialect.Messages)
	allMsgs = appendCustomMessages(allMsgs)

	return &dialect.Dialect{
		Version:  all.Dialect.Version,
		Messages: allMsgs,
	}
}
