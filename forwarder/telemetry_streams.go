//go:build ignore

package forwarder

import (
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"

	"DroneBridge/logger"
)

// px4StreamRequests mirrors PX4_Mavlink_Drone_Simulator telemetry rates (interval µs).
// Values are requested from real PX4 — CM5 does not synthesize messages.
var px4StreamRequests = []struct {
	msgID    uint32
	interval int32 // microseconds; MAV_CMD_SET_MESSAGE_INTERVAL param2
	name     string
}{
	{1, 1_000_000, "SYS_STATUS"},           // 1 Hz
	{24, 100_000, "GPS_RAW_INT"},           // 10 Hz
	{30, 33_333, "ATTITUDE"},               // ~30 Hz
	{32, 100_000, "LOCAL_POSITION_NED"},    // 10 Hz
	{33, 100_000, "GLOBAL_POSITION_INT"},    // 10 Hz
	{74, 100_000, "VFR_HUD"},               // 10 Hz
	{147, 1_000_000, "BATTERY_STATUS"},     // 1 Hz
	{436, 2_000_000, "CURRENT_MODE"},       // 0.5 Hz
}

func (f *Forwarder) requestPixhawkTelemetryStreams() {
	if f.listenerNode == nil {
		return
	}
	sysID := resolveOutSystemID()
	for _, item := range px4StreamRequests {
		cmd := &common.MessageCommandLong{
			TargetSystem:    sysID,
			TargetComponent: 1,
			Command:         common.MAV_CMD_SET_MESSAGE_INTERVAL,
			Param1:          float32(item.msgID),
			Param2:          float32(item.interval),
		}
		if err := f.listenerNode.WriteMessageAll(cmd); err != nil {
			logger.Warn("[MAVLINK] SET_MESSAGE_INTERVAL %s (%d) failed: %v", item.name, item.msgID, err)
		}
	}
	logger.Info("[MAVLINK] Requested PX4 telemetry streams (simulator parity, sys=%d, %d msgs)", sysID, len(px4StreamRequests))
}
