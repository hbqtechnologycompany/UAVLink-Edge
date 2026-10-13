//go:build ignore

package web

import (
	"fmt"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"DroneBridge/px4mode"
)

// SetFlightModeRequest is the JSON body for POST /api/px4/set-mode.
type SetFlightModeRequest struct {
	Mode string `json:"mode"`
}

// SetFlightModeResponse is returned after attempting to change PX4 mode.
type SetFlightModeResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	Mode       string `json:"mode,omitempty"`
	CustomMode uint32 `json:"custom_mode,omitempty"`
}

// SetFlightMode sends MAVLink SET_MODE to the connected Pixhawk.
func (b *MAVLinkBridge) SetFlightMode(modeName string) *SetFlightModeResponse {
	if b == nil || b.node == nil {
		return &SetFlightModeResponse{
			Success: false,
			Message: "MAVLink bridge not initialized",
		}
	}

	main, sub, ok := px4mode.ResolveSetMode(modeName)
	if !ok {
		return &SetFlightModeResponse{
			Success: false,
			Message: "Unsupported mode. Use: manual, posctl, normal, stab, loiter, mission, rtl, land",
		}
	}

	b.mutex.RLock()
	connected := b.connected
	sysID := b.pixhawkSysID
	b.mutex.RUnlock()

	if !connected {
		return &SetFlightModeResponse{
			Success: false,
			Message: "Not connected to Pixhawk",
		}
	}

	custom := px4mode.CustomMode(main, sub)
	msg := &common.MessageSetMode{
		TargetSystem: sysID,
		BaseMode:     common.MAV_MODE(common.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED),
		CustomMode:   custom,
	}

	if err := b.writeToPixhawk(msg); err != nil {
		return &SetFlightModeResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to send SET_MODE: %v", err),
		}
	}

	label := px4mode.FromCustomMode(custom).NameQGC
	return &SetFlightModeResponse{
		Success:    true,
		Message:    fmt.Sprintf("SET_MODE sent to system %d", sysID),
		Mode:       label,
		CustomMode: custom,
	}
}

// GetFlightModeStatus returns the latest cached PX4 mode for GET /api/px4/mode.
func GetFlightModeStatus() map[string]interface{} {
	telem := GetTelemetrySnapshot()
	out := map[string]interface{}{
		"pixhawk_connected":  telem.Connected,
		"telemetry_valid":    telem.Valid,
		"flight_mode":        telem.FlightMode,
		"flight_mode_px4":    telem.FlightModePX4,
		"main_mode":          telem.MainMode,
		"sub_mode":           telem.SubMode,
		"custom_mode":        telem.CustomMode,
		"flight_mode_source": telem.FlightModeSource,
		"supported_modes": []string{
			"manual", "posctl", "normal", "stab", "loiter", "mission", "rtl", "land",
		},
	}
	if !telem.Valid && telem.Connected {
		out["flight_mode"] = "UNKNOWN"
	}
	return out
}
