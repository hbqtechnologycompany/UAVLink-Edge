// Package companion — MAVLink identity cho CM5 (tách khỏi PX4 autopilot).
package companion

// CM5 companion MAVLink identity (chỉ cho DRONEBRIDGE_STATUS diagnostic, không dùng cho camera).
const (
	MavlinkSysID uint8 = 245 // onboard companion — tách khỏi PX4 autopilot
	CompCamera   uint8 = 100 // MAV_COMP_ID_CAMERA
	CompOnboard  uint8 = 191 // MAV_COMP_ID_ONBOARD_COMPUTER
)
