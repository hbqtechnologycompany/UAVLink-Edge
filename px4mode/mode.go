package px4mode

import (
	"fmt"
	"strings"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

// MAV_AUTOPILOT_PX4 — autopilot field in HEARTBEAT when firmware is PX4.
const MAVAutopilotPX4 = uint8(common.MAV_AUTOPILOT_PX4)

// Info is the decoded PX4 flight mode (same rules as QGroundControl PX4 plugin).
type Info struct {
	CustomMode   uint32 `json:"custom_mode"`
	MainMode     uint8  `json:"main_mode"`
	SubMode      uint8  `json:"sub_mode"`
	NamePX4      string `json:"flight_mode_px4"` // PX4 internal: LOITER, POSCTL, …
	NameQGC      string `json:"flight_mode"`     // QGC toolbar label: Hold, Position, …
	Autopilot    string `json:"autopilot"`       // "PX4" when parsed from PX4 HEARTBEAT
	Source       string `json:"source"`          // "HEARTBEAT" | "CURRENT_MODE"
	SystemStatus uint8  `json:"system_status,omitempty"`
}

// CustomMode encodes PX4 main/sub mode into HEARTBEAT custom_mode (PX4 px4_custom_mode).
func CustomMode(main, sub uint8) uint32 {
	return uint32(main)<<16 | uint32(sub)<<24
}

// Decode splits PX4 custom_mode: main @ bits 16–23, sub @ bits 24–31 (PX4 + QGC).
func Decode(customMode uint32) (main, sub uint8) {
	main = uint8((customMode >> 16) & 0xFF)
	sub = uint8((customMode >> 24) & 0xFF)
	return main, sub
}

// FromHeartbeat decodes mode when HEARTBEAT is from PX4. Returns ok=false for other autopilots.
func FromHeartbeat(msg *common.MessageHeartbeat) (Info, bool) {
	if msg == nil || uint8(msg.Autopilot) != MAVAutopilotPX4 {
		return Info{}, false
	}
	if msg.CustomMode == 0 && msg.BaseMode&common.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED == 0 {
		return Info{}, false
	}
	info := FromCustomMode(msg.CustomMode)
	info.SystemStatus = uint8(msg.SystemStatus)
	info.Source = "HEARTBEAT"
	return info, true
}

// FromCurrentMode decodes PX4 CURRENT_MODE (msg 436).
func FromCurrentMode(msg *common.MessageCurrentMode) (Info, bool) {
	if msg == nil || msg.CustomMode == 0 {
		return Info{}, false
	}
	info := FromCustomMode(msg.CustomMode)
	info.Source = "CURRENT_MODE"
	return info, true
}

// FromCustomMode builds Info from PX4 custom_mode uint32.
func FromCustomMode(customMode uint32) Info {
	main, sub := Decode(customMode)
	return Info{
		CustomMode: customMode,
		MainMode:   main,
		SubMode:    sub,
		NamePX4:    namePX4(main, sub),
		NameQGC:    nameQGC(main, sub),
		Autopilot:  "PX4",
	}
}

// Name returns PX4 internal mode name (legacy helper).
func Name(customMode uint32) string {
	main, sub := Decode(customMode)
	return namePX4(main, sub)
}

// NameQGC returns the label QGroundControl shows in the flight-mode toolbar (PX4 multicopter/rover).
func NameQGC(customMode uint32) string {
	main, sub := Decode(customMode)
	return nameQGC(main, sub)
}

// namePX4 — PX4 firmware mode ids (px4_main_mode / px4_auto_sub_mode).
func namePX4(main, sub uint8) string {
	switch main {
	case 1:
		return "MANUAL"
	case 2:
		return "ALTCTL"
	case 3:
		return "POSCTL"
	case 4:
		switch sub {
		case 1:
			return "READY"
		case 2:
			return "TAKEOFF"
		case 3:
			return "LOITER"
		case 4:
			return "MISSION"
		case 5:
			return "RTL"
		case 6:
			return "LAND"
		case 7:
			return "RTGS"
		case 8:
			return "FOLLOW"
		default:
			return "AUTO"
		}
	case 5:
		return "ACRO"
	case 6:
		return "OFFBOARD"
	case 7:
		return "STAB"
	case 8:
		return "RATTITUDE"
	case 9:
		return "SIMPLE"
	default:
		return fmt.Sprintf("M%d", main)
	}
}

// nameQGC — QGroundControl FirmwarePluginPX4 toolbar strings.
// Ref: QGC PX4 flight mode map (Hold = AUTO:LOITER, Return = RTL, …).
func nameQGC(main, sub uint8) string {
	switch main {
	case 1:
		return "Manual"
	case 2:
		return "Altitude"
	case 3:
		return "Position"
	case 4:
		switch sub {
		case 1:
			return "Ready"
		case 2:
			return "Takeoff"
		case 3:
			return "Hold"
		case 4:
			return "Mission"
		case 5:
			return "Return"
		case 6:
			return "Land"
		case 7:
			return "Return to Groundstation"
		case 8:
			return "Follow Me"
		default:
			return "Auto"
		}
	case 5:
		return "Acro"
	case 6:
		return "Offboard"
	case 7:
		return "Stabilized"
	case 8:
		return "Rattitude"
	case 9:
		return "Simple"
	default:
		return fmt.Sprintf("Mode %d", main)
	}
}

// ResolveSetMode maps user-facing mode names to PX4 main/sub modes.
func ResolveSetMode(mode string) (main, sub uint8, ok bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "manual":
		return 1, 0, true
	case "altctl", "altitude":
		return 2, 0, true
	case "posctl", "position", "normal":
		return 3, 0, true
	case "stab", "stabilize", "stabilized":
		return 7, 0, true
	case "acro":
		return 5, 0, true
	case "offboard":
		return 6, 0, true
	case "loiter", "hold":
		return 4, 3, true
	case "mission", "auto":
		return 4, 4, true
	case "rtl", "return":
		return 4, 5, true
	case "land":
		return 4, 6, true
	default:
		return 0, 0, false
	}
}
