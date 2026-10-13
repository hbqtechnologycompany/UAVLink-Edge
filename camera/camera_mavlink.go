package camera

import (
	"fmt"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/message"

	"DroneBridge/config"
	"DroneBridge/logger"
)

// CameraMavlinkPublisher publishes CM5 camera MAVLink (sys=PX4, comp=100).
type CameraMavlinkPublisher interface {
	PublishCamera(msg message.Message) error
}

// StartCameraMavlinkBridge emits HEARTBEAT + VIDEO_STREAM_* từ CM5 lên server (không qua PX4).
func StartCameraMavlinkBridge(cfg *config.Config, pub CameraMavlinkPublisher, stopCh <-chan struct{}) {
	if cfg == nil || pub == nil || !cfg.Camera.Enabled {
		return
	}
	if cfg.Camera.MavlinkStatusEnabled != nil && !*cfg.Camera.MavlinkStatusEnabled {
		return
	}
	hz := cfg.Camera.MavlinkStatusHz
	if hz <= 0 {
		hz = 1
	}
	interval := time.Duration(float64(time.Second) / hz)
	mtx := ResolveMediamtx(cfg)
	logger.Info("[CM5][MAVLINK] Camera bridge comp=%d @ %.1f Hz (sys=PX4 via forwarder)", 100, hz)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var seq uint8
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				seq++
				publishCm5CameraMavlink(cfg, pub, mtx, seq)
			}
		}
	}()
}

func publishCm5CameraMavlink(cfg *config.Config, pub CameraMavlinkPublisher, mtx Mediamtx, seq uint8) {
	// HEARTBEAT MAV_TYPE_CAMERA — chuẩn MAVLink cho component camera (comp=100, sys=PX4).
	hb := &common.MessageHeartbeat{
		Type:           common.MAV_TYPE_CAMERA,
		Autopilot:      common.MAV_AUTOPILOT_INVALID,
		BaseMode:       0,
		CustomMode:     0,
		SystemStatus:   common.MAV_STATE_ACTIVE,
		MavlinkVersion: 3,
	}
	if err := pub.PublishCamera(hb); err != nil {
		logger.Debug("[CM5][MAVLINK] camera HEARTBEAT: %v", err)
	}

	streamID := uint8(0)
	for _, stream := range cfg.Camera.Streams {
		if !stream.Enabled {
			continue
		}
		st := ReadStreamStats(stream.CameraID, 5*time.Second)
		if st == nil || st.FPSWindow < 0.5 {
			continue
		}
		streamID++
		w, h := 640, 480
		if len(stream.Size) >= 2 {
			w, h = stream.Size[0], stream.Size[1]
		}
		bitrate := stream.Bitrate
		if bitrate <= 0 {
			bitrate = 5000
		}

		status := &common.MessageVideoStreamStatus{
			StreamId:       streamID,
			Flags:          common.VIDEO_STREAM_STATUS_FLAGS_RUNNING,
			Framerate:      float32(st.FPSWindow),
			ResolutionH:    uint16(w),
			ResolutionV:    uint16(h),
			Bitrate:        uint32(bitrate) * 1000,
			CameraDeviceId: 0,
		}
		if err := pub.PublishCamera(status); err != nil {
			logger.Debug("[CM5][MAVLINK] VIDEO_STREAM_STATUS cam%d: %v", stream.CameraID, err)
		}

		// Thông tin stream (URI RTSP) — gửi lần đầu mỗi chu kỳ seq==1 hoặc mỗi 10s
		if seq == 1 || seq%10 == 0 {
			path, err := PublishPath(cfg, stream.CameraID)
			if err == nil {
				uri := fmt.Sprintf("rtsp://%s:%d%s", mtx.Host, mtx.RTSPPort, path)
				name := fmt.Sprintf("CM5_CAM%d", stream.CameraID)
				info := &common.MessageVideoStreamInformation{
					StreamId:       streamID,
					Count:          streamID,
					Type:           common.VIDEO_STREAM_TYPE_RTSP,
					Flags:          common.VIDEO_STREAM_STATUS_FLAGS_RUNNING,
					Framerate:      float32(st.FPSWindow),
					ResolutionH:    uint16(w),
					ResolutionV:    uint16(h),
					Bitrate:        uint32(bitrate) * 1000,
					Name:           name,
					Uri:            uri,
					Encoding:       common.VIDEO_STREAM_ENCODING_H264,
					CameraDeviceId: 0,
				}
				if err := pub.PublishCamera(info); err != nil {
					logger.Debug("[CM5][MAVLINK] VIDEO_STREAM_INFORMATION cam%d: %v", stream.CameraID, err)
				}
			}
		}
	}
}
