package auth

// MAVLink HEARTBEAT.type values used in REGISTER_INIT v2.
// Server maps these to drones.hardware_profile.
const (
	VehicleTypeFixedWing      uint8 = 1
	VehicleTypeQuadrotor      uint8 = 2
	VehicleTypeHelicopter     uint8 = 4
	VehicleTypeGroundRover    uint8 = 10
	VehicleTypeSubmarine      uint8 = 12
	VehicleTypeHexarotor      uint8 = 13
	VehicleTypeOctorotor      uint8 = 14
	VehicleTypeVTOLFixedRotor uint8 = 22
)

const MaxRegisterModelLen = 32

// SupportedVehicleType: server có hardware_profile riêng cho loại này.
func SupportedVehicleType(t uint8) bool {
	switch t {
	case VehicleTypeFixedWing, VehicleTypeQuadrotor, VehicleTypeHelicopter, VehicleTypeGroundRover,
		VehicleTypeSubmarine, VehicleTypeHexarotor, VehicleTypeOctorotor, VehicleTypeVTOLFixedRotor:
		return true
	}
	return false
}

// ServerVehicleType là loại server lưu cho vehicle_type đã gửi lúc đăng ký:
// 0 (gói v1, không trailer) và loại không hỗ trợ đều thành quadrotor.
func ServerVehicleType(sent uint8) uint8 {
	if SupportedVehicleType(sent) {
		return sent
	}
	return VehicleTypeQuadrotor
}
