package mavlink_custom

import (
	"testing"

	"github.com/bluenviron/gomavlib/v3/pkg/message"
)

func TestSessionHeartbeatPayloadSize(t *testing.T) {
	p := BuildSessionHeartbeatPayload("test-session-token", 12345, 7, 1)
	if len(p) != 39 {
		t.Fatalf("payload len=%d want 39", len(p))
	}
}

func TestSessionHeartbeatFrameChecksum(t *testing.T) {
	fr := NewSessionHeartbeatFrame(1, 1, 0, "token", 999, 1, 1)
	if fr.Checksum == 0 {
		t.Fatal("expected non-zero checksum")
	}
	if len(fr.Message.(*message.MessageRaw).Payload) != 39 {
		t.Fatal("bad payload on frame")
	}
}
