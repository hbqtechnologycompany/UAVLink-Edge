package forwarder

import (
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/message"

	"DroneBridge/camera"
	"DroneBridge/companion"
	"DroneBridge/logger"
	"DroneBridge/mavlink_custom"
)

const px4GpsStaleAfter = 5 * time.Second

type px4GpsCache struct {
	mu         sync.RWMutex
	lastAt     time.Time
	fixType    uint8
	satellites uint8
	hasLocal   bool
	lastLocal  time.Time
	ekfAt      time.Time
	ekfLat     int32
	ekfLon     int32
	ekfAlt     int32
}

func (c *px4GpsCache) noteGpsRaw(msg *common.MessageGpsRawInt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAt = time.Now()
	c.fixType = uint8(msg.FixType)
	c.satellites = msg.SatellitesVisible
}

func (c *px4GpsCache) noteLocalPosition() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hasLocal = true
	c.lastLocal = time.Now()
}

// noteGlobal stores the latest EKF global position (GLOBAL_POSITION_INT).
// Lat/lon of 0 are ignored so an uninitialized estimator cannot pin the map at Null Island.
func (c *px4GpsCache) noteGlobal(msg *common.MessageGlobalPositionInt) {
	if msg == nil || (msg.Lat == 0 && msg.Lon == 0) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ekfAt = time.Now()
	c.ekfLat = msg.Lat
	c.ekfLon = msg.Lon
	c.ekfAlt = msg.Alt
}

func (c *px4GpsCache) freshEkf(maxAge time.Duration) (lat, lon, alt int32, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ekfAt.IsZero() || time.Since(c.ekfAt) > maxAge {
		return 0, 0, 0, false
	}
	return c.ekfLat, c.ekfLon, c.ekfAlt, true
}

func (c *px4GpsCache) snapshot() (fixType, sats, px4Streaming, diagnosis uint8) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	px4Streaming = 0
	if !c.lastAt.IsZero() && time.Since(c.lastAt) <= px4GpsStaleAfter {
		px4Streaming = 1
		fixType = c.fixType
		sats = c.satellites
		if c.fixType >= 3 && c.satellites > 0 {
			return fixType, sats, px4Streaming, mavlink_custom.GpsDiagPx4Ok
		}
		return fixType, sats, px4Streaming, mavlink_custom.GpsDiagPx4NoFix
	}

	fixType = 255
	if c.hasLocal && !c.lastLocal.IsZero() && time.Since(c.lastLocal) <= px4GpsStaleAfter {
		return fixType, 0, 0, mavlink_custom.GpsDiagPx4LocalOnly
	}
	return fixType, 0, 0, mavlink_custom.GpsDiagNoPx4Stream
}

func (f *Forwarder) initCompanionSender(endpoint gomavlib.EndpointConf) error {
	node, err := createOutboundNode(endpoint, companion.MavlinkSysID, companion.CompOnboard)
	if err != nil {
		return err
	}
	f.companionSenderMu.Lock()
	if f.companionSenderNode != nil {
		f.companionSenderNode.Close()
	}
	f.companionSenderNode = node
	f.companionSenderMu.Unlock()
	return nil
}

// PublishCompanion sends CM5 onboard messages (sys=245, comp=191) — không phải PX4.
func (f *Forwarder) PublishCompanion(msg message.Message) error {
	return f.publishViaNode(&f.companionSenderMu, f.companionSenderNode, msg)
}

func (f *Forwarder) startCompanionStatusBridge() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-f.stopCh:
				return
			case <-ticker.C:
				f.publishDronebridgeStatus()
			}
		}
	}()
	logger.Info("[CM5][MAVLINK] DRONEBRIDGE_STATUS @ 1 Hz (sys=%d comp=%d)", companion.MavlinkSysID, companion.CompOnboard)
}

func (f *Forwarder) publishDronebridgeStatus() {
	fix, sats, px4Stream, diag := f.gpsPx4.snapshot()
	cam0, cam1 := cameraLiveFlags()
	msg := &mavlink_custom.MessageDronebridgeStatus{
		TimestampMs:     uint32(time.Now().UnixMilli()),
		GpsFixType:      fix,
		GpsSatellites:   sats,
		GpsPx4Streaming: px4Stream,
		GpsDiagnosis:    diag,
		Camera0Live:     cam0,
		Camera1Live:     cam1,
	}
	if err := f.PublishCompanion(msg); err != nil {
		logger.Debug("[CM5][MAVLINK] DRONEBRIDGE_STATUS: %v", err)
	}
}

func cameraLiveFlags() (cam0, cam1 uint8) {
	for _, id := range []int{0, 1} {
		st := camera.ReadStreamStats(id, 5*time.Second)
		if st == nil || st.FPSWindow < 0.5 {
			continue
		}
		if id == 0 {
			cam0 = 1
		} else {
			cam1 = 1
		}
	}
	return cam0, cam1
}

// GpsDiagnostic returns human-readable GPS source analysis for /api/status.
func (f *Forwarder) GpsDiagnostic() map[string]interface{} {
	fix, sats, px4Stream, diag := f.gpsPx4.snapshot()
	cam0, cam1 := cameraLiveFlags()
	labels := map[uint8]string{
		mavlink_custom.GpsDiagUnknown:      "unknown",
		mavlink_custom.GpsDiagNoPx4Stream:  "no_px4_gps_raw",
		mavlink_custom.GpsDiagPx4NoFix:     "px4_no_gps_fix",
		mavlink_custom.GpsDiagPx4Ok:        "px4_gps_ok",
		mavlink_custom.GpsDiagPx4LocalOnly: "px4_local_position_only",
	}
	hints := map[uint8]string{
		mavlink_custom.GpsDiagNoPx4Stream:  "PX4 không gửi GPS_RAW — đèn Global pos trên server nếu xanh là do LocalPosition/Altitude (lỗi hiển thị server)",
		mavlink_custom.GpsDiagPx4NoFix:     "PX4 báo không có GPS fix — đúng khi chưa lắp module GPS",
		mavlink_custom.GpsDiagPx4Ok:        "GPS từ PX4 OK",
		mavlink_custom.GpsDiagPx4LocalOnly: "Có vị trí cục bộ (EKF) nhưng không có GPS_RAW — server không nên bật đèn GPS global",
	}
	return map[string]interface{}{
		"source":            "cm5_companion_mavlink",
		"companion_sys_id":  companion.MavlinkSysID,
		"camera_sys_id":     resolveOutSystemID(),
		"camera_comp_id":    companion.CompCamera,
		"camera0_live":      cam0 == 1,
		"camera1_live":      cam1 == 1,
		"px4_gps_streaming": px4Stream == 1,
		"gps_fix_type":      fix,
		"gps_satellites":    sats,
		"diagnosis_code":    diag,
		"diagnosis":         labels[diag],
		"hint":              hints[diag],
	}
}
