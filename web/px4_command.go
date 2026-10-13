package web

import (
	"fmt"
	"sync"

	"github.com/bluenviron/gomavlib/v3/pkg/message"
)

var (
	px4WriteMu sync.RWMutex
	px4Write   func(message.Message) error

	px4LinkMu    sync.RWMutex
	px4LinkName        = "ethernet"
	px4LinkHBAge int64 = -1
)

// SetPX4CommandWriter installs the single-link sender used for commands to PX4.
func SetPX4CommandWriter(fn func(message.Message) error) {
	px4WriteMu.Lock()
	px4Write = fn
	px4WriteMu.Unlock()
}

// SetPX4LinkStatus records the link currently allowed to carry commands.
func SetPX4LinkStatus(name string, hbAgeMs int64) {
	if name == "" {
		name = "ethernet"
	}
	px4LinkMu.Lock()
	px4LinkName = name
	px4LinkHBAge = hbAgeMs
	px4LinkMu.Unlock()
}

// PX4LinkStatus returns the active command link and the age of its last PX4 heartbeat.
func PX4LinkStatus() (name string, hbAgeMs int64) {
	px4LinkMu.RLock()
	defer px4LinkMu.RUnlock()
	return px4LinkName, px4LinkHBAge
}

func (b *MAVLinkBridge) writeToPixhawk(msg message.Message) error {
	px4WriteMu.RLock()
	fn := px4Write
	px4WriteMu.RUnlock()
	if fn == nil {
		return fmt.Errorf("PX4 link arbiter not ready")
	}
	return fn(msg)
}
