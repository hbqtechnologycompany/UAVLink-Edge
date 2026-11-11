package web

import (
	"testing"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

func TestVehicleProfileSwitch(t *testing.T) {
	notePreflightHeartbeat(&common.MessageHeartbeat{Type: common.MAV_TYPE_GROUND_ROVER}, "")

	// Đăng ký v1 (vehicle_type 0) → server giữ quadrotor; PX4 là rover → cần đăng ký lại.
	vp := vehicleProfile(0)
	if vp["registered_type"] != uint8(2) || vp["live_type"] != uint8(10) || vp["switch_needed"] != true {
		t.Fatalf("v1 + rover: %v", vp)
	}
	if vp := vehicleProfile(10); vp["switch_needed"] != false {
		t.Fatalf("rover đã đăng ký: %v", vp)
	}

	// Loại server không hỗ trợ (tàu mặt nước) → không nhắc đăng ký lại.
	notePreflightHeartbeat(&common.MessageHeartbeat{Type: common.MAV_TYPE_SURFACE_BOAT}, "")
	if vp := vehicleProfile(0); vp["switch_needed"] != false || vp["live_supported"] != false {
		t.Fatalf("boat: %v", vp)
	}
}
