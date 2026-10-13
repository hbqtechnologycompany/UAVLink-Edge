package camera

import (
	"math"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/message"

	"DroneBridge/config"
	"DroneBridge/logger"
)

// LandingMavlinkPublisher sends generated MAVLink to the cloud router.
type LandingMavlinkPublisher interface {
	PublishGenerated(msg message.Message) error
}

// StartLandingMavlinkBridge publishes LANDING_TARGET from /tmp/camera_landing_*.json (IV-1).
func StartLandingMavlinkBridge(cfg *config.Config, pub LandingMavlinkPublisher, stopCh <-chan struct{}) {
	if cfg == nil || !cfg.Landing.MavlinkEnabled || pub == nil {
		return
	}
	cameraID := cfg.Landing.MavlinkCamera
	if cameraID < 0 {
		cameraID = 0
	}
	hz := cfg.Landing.MavlinkHz
	if hz <= 0 {
		hz = 10
	}
	interval := time.Duration(float64(time.Second) / hz)
	logger.Info("[LANDING][MAVLINK] LANDING_TARGET bridge cam%d @ %.1f Hz", cameraID, hz)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				lt := ReadLandingTelemetry(cameraID, 2*time.Second)
				if lt == nil || !lt.Detected {
					continue
				}
				msg := landingTargetFromTelemetry(lt)
				if err := pub.PublishGenerated(msg); err != nil {
					logger.Debug("[LANDING][MAVLINK] write failed: %v", err)
				}
			}
		}
	}()
}

func landingTargetFromTelemetry(lt *LandingTelemetry) *common.MessageLandingTarget {
	var angleX, angleY float32
	if lt.OffsetX != nil {
		angleX = float32(*lt.OffsetX)
	}
	if lt.OffsetY != nil {
		angleY = float32(*lt.OffsetY)
	}
	size := float32(0.25)
	if lt.Similarity != nil && *lt.Similarity > 0 {
		size = float32(math.Min(1.0, *lt.Similarity))
	}
	return &common.MessageLandingTarget{
		TimeUsec:      uint64(time.Now().UnixMicro()),
		TargetNum:     0,
		Frame:         common.MAV_FRAME_BODY_FRD,
		AngleX:        angleX,
		AngleY:        angleY,
		Distance:      1.0,
		SizeX:         size,
		SizeY:         size,
		Type:          common.LANDING_TARGET_TYPE_VISION_FIDUCIAL,
		PositionValid: 0,
	}
}
