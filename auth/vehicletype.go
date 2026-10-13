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
