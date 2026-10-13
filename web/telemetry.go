package web

import (
	"fmt"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

const telemetryStaleAfter = 5 * time.Second

// TelemetrySnapshot is the latest PX4/MAVLink data for LCD and status APIs.
type TelemetrySnapshot struct {
	Connected  bool      `json:"connected"`
	Valid      bool      `json:"valid"`
	AltitudeM  float64   `json:"altitude_m"`
	SpeedMs    float64   `json:"speed_ms"`
	ClimbMs    float64   `json:"climb_ms"`
	BatteryPct float64   `json:"battery_pct"`
	VoltageV   float64   `json:"voltage_v"`
	Satellites int       `json:"satellites"`
	GpsFix     int       `json:"gps_fix"`
	GpsLock    string    `json:"gps_lock"`
	LatitudeDeg  float64 `json:"latitude_deg,omitempty"`
	LongitudeDeg float64 `json:"longitude_deg,omitempty"`
	Hdop         float64 `json:"hdop,omitempty"`
	HeadingDeg   float64 `json:"heading_deg,omitempty"`
	FlightMode string    `json:"flight_mode"`
	LastUpdate time.Time `json:"last_update"`
}

type telemetryCache struct {
	mu sync.RWMutex

	connected  bool
	altitudeM  float64
	speedMs    float64
	climbMs    float64
	batteryPct float64
	voltageV   float64
	satellites int
	gpsFix     int
	gpsLock    string
	latitudeDeg  float64
	longitudeDeg float64
	hdop         float64
	headingDeg   float64
	flightMode string
	lastUpdate time.Time

	hasAltitude bool
	hasSpeed    bool
	hasClimb    bool
	hasBattery  bool
	hasGPS      bool
	hasPosition bool
	hasHeading  bool
	hasMode     bool
}

var telemetry telemetryCache

func px4FlightModeName(customMode uint32) string {
	mainMode := uint8((customMode >> 16) & 0xFF)
	subMode := uint8((customMode >> 24) & 0xFF)

	switch mainMode {
	case 1:
		return "MANUAL"
	case 2:
		return "ALTCTL"
	case 3:
		return "POSCTL"
	case 4:
		switch subMode {
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
	default:
		return fmt.Sprintf("M%d", mainMode)
	}
}

func gpsFixLabel(fixType uint8, satellites int) string {
	switch fixType {
	case 0:
		return "NO GPS"
	case 1:
		return "NO FIX"
	case 2:
		if satellites > 0 {
			return fmt.Sprintf("2D %d", satellites)
		}
		return "2D FIX"
	case 3:
		if satellites > 0 {
			return fmt.Sprintf("3D %d", satellites)
		}
		return "3D FIX"
	case 4:
		if satellites > 0 {
			return fmt.Sprintf("DGPS %d", satellites)
		}
		return "DGPS"
	case 5:
		if satellites > 0 {
			return fmt.Sprintf("RTK %d", satellites)
		}
		return "RTK"
	case 6:
		if satellites > 0 {
			return fmt.Sprintf("RTK2 %d", satellites)
		}
		return "RTK2"
	default:
		if satellites > 0 {
			return fmt.Sprintf("FIX%d %d", fixType, satellites)
		}
		return fmt.Sprintf("FIX%d", fixType)
	}
}

func (t *telemetryCache) touch() {
	t.lastUpdate = time.Now()
}

func (t *telemetryCache) markConnected() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
	t.touch()
}

func (t *telemetryCache) markDisconnected() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = false
	t.hasAltitude = false
	t.hasSpeed = false
	t.hasClimb = false
	t.hasBattery = false
	t.hasGPS = false
	t.hasPosition = false
	t.hasHeading = false
	t.hasMode = false
}

// HandleTelemetryHeartbeat updates flight mode from a real Pixhawk heartbeat.
func HandleTelemetryHeartbeat(msg *common.MessageHeartbeat) {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.connected = true
	telemetry.flightMode = px4FlightModeName(msg.CustomMode)
	telemetry.hasMode = true
	telemetry.touch()
}

// HandleTelemetryVfrHud updates HUD metrics from Pixhawk.
func HandleTelemetryVfrHud(msg *common.MessageVfrHud) {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.connected = true
	telemetry.altitudeM = float64(msg.Alt)
	telemetry.speedMs = float64(msg.Groundspeed)
	telemetry.climbMs = float64(msg.Climb)
	telemetry.headingDeg = float64(msg.Heading)
	telemetry.hasHeading = true
	telemetry.hasAltitude = true
	telemetry.hasSpeed = true
	telemetry.hasClimb = true
	telemetry.touch()
}

// HandleTelemetryGlobalPosition updates altitude/climb from GLOBAL_POSITION_INT.
func HandleTelemetryGlobalPosition(msg *common.MessageGlobalPositionInt) {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.connected = true
	if msg.RelativeAlt != 0 {
		telemetry.altitudeM = float64(msg.RelativeAlt) / 1000.0
		telemetry.hasAltitude = true
	}
	if msg.Vz != 0 {
		telemetry.climbMs = float64(-msg.Vz) / 100.0
		telemetry.hasClimb = true
	}
	if msg.Lat != 0 {
		telemetry.latitudeDeg = float64(msg.Lat) / 1e7
		telemetry.hasPosition = true
	}
	if msg.Lon != 0 {
		telemetry.longitudeDeg = float64(msg.Lon) / 1e7
		telemetry.hasPosition = true
	}
	if msg.Hdg != 65535 {
		telemetry.headingDeg = float64(msg.Hdg) / 100.0
		telemetry.hasHeading = true
	}
	telemetry.touch()
}

// HandleTelemetryGpsRaw updates GPS fix/satellites from Pixhawk.
func HandleTelemetryGpsRaw(msg *common.MessageGpsRawInt) {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.connected = true
	telemetry.gpsFix = int(msg.FixType)
	telemetry.satellites = int(msg.SatellitesVisible)
	telemetry.gpsLock = gpsFixLabel(uint8(msg.FixType), int(msg.SatellitesVisible))
	if msg.Lat != 0 {
		telemetry.latitudeDeg = float64(msg.Lat) / 1e7
		telemetry.hasPosition = true
	}
	if msg.Lon != 0 {
		telemetry.longitudeDeg = float64(msg.Lon) / 1e7
		telemetry.hasPosition = true
	}
	if msg.Eph != 0 && msg.Eph != 65535 {
		telemetry.hdop = float64(msg.Eph) / 100.0
	}
	if msg.Cog != 65535 {
		telemetry.headingDeg = float64(msg.Cog) / 100.0
		telemetry.hasHeading = true
	}
	telemetry.hasGPS = true
	telemetry.touch()
}

// HandleTelemetrySysStatus updates battery/voltage from Pixhawk.
func HandleTelemetrySysStatus(msg *common.MessageSysStatus) {
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	telemetry.connected = true
	if msg.VoltageBattery != 0 && msg.VoltageBattery != 65535 {
		telemetry.voltageV = float64(msg.VoltageBattery) / 1000.0
	}
	if msg.BatteryRemaining >= 0 && msg.BatteryRemaining <= 100 {
		telemetry.batteryPct = float64(msg.BatteryRemaining)
		telemetry.hasBattery = true
	}
	telemetry.touch()
}

func (t *telemetryCache) snapshot() TelemetrySnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	valid := t.connected && !t.lastUpdate.IsZero() && time.Since(t.lastUpdate) <= telemetryStaleAfter
	snap := TelemetrySnapshot{
		Connected:  t.connected,
		Valid:      valid,
		Satellites: t.satellites,
		GpsFix:     t.gpsFix,
		GpsLock:    t.gpsLock,
		FlightMode: t.flightMode,
		LastUpdate: t.lastUpdate,
	}
	if valid {
		if t.hasAltitude {
			snap.AltitudeM = t.altitudeM
		}
		if t.hasSpeed {
			snap.SpeedMs = t.speedMs
		}
		if t.hasClimb {
			snap.ClimbMs = t.climbMs
		}
		if t.hasBattery {
			snap.BatteryPct = t.batteryPct
		}
		if t.voltageV > 0 {
			snap.VoltageV = t.voltageV
		}
		if t.hasGPS {
			snap.GpsLock = t.gpsLock
		}
		if t.hasPosition {
			snap.LatitudeDeg = t.latitudeDeg
			snap.LongitudeDeg = t.longitudeDeg
		}
		if t.hdop > 0 {
			snap.Hdop = t.hdop
		}
		if t.hasHeading {
			snap.HeadingDeg = t.headingDeg
		}
		if t.hasMode {
			snap.FlightMode = t.flightMode
		}
	}
	if !valid {
		snap.GpsLock = ""
		snap.FlightMode = ""
	}
	return snap
}

// GetTelemetrySnapshot returns the latest cached MAVLink telemetry for APIs/LCD.
func GetTelemetrySnapshot() TelemetrySnapshot {
	return telemetry.snapshot()
}
