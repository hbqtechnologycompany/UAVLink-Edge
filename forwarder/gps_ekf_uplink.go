package forwarder

import (
	"fmt"
	"sync"

	"github.com/bluenviron/gomavlib/v3/pkg/dialect"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/frame"

	"DroneBridge/mavlink_custom"
)

var (
	gpsDialectOnce sync.Once
	gpsDialectRW   *dialect.ReadWriter
	gpsDialectErr  error
)

func gpsMessageRW() (*dialect.ReadWriter, error) {
	gpsDialectOnce.Do(func() {
		gpsDialectRW = &dialect.ReadWriter{Dialect: mavlink_custom.GetCombinedDialect()}
		gpsDialectErr = gpsDialectRW.Initialize()
	})
	return gpsDialectRW, gpsDialectErr
}

// gpsUplinkFrame copies a GPS_RAW_INT frame and replaces lat/lon/alt with the EKF
// position. Fix type, satellite count and dilution stay from the GPS receiver.
// Sequence, system id and component id stay on the original frame so the server
// keeps one MAVLink sequence stream.
func gpsUplinkFrame(fr frame.Frame, lat, lon, alt int32) (frame.Frame, error) {
	src, ok := fr.GetMessage().(*common.MessageGpsRawInt)
	if !ok || src == nil {
		return nil, fmt.Errorf("frame is not GPS_RAW_INT")
	}
	patched := *src
	patched.Lat = lat
	patched.Lon = lon
	patched.Alt = alt
	patched.AltEllipsoid = alt

	rw, err := gpsMessageRW()
	if err != nil {
		return nil, err
	}
	mp := rw.GetMessage(patched.GetID())
	if mp == nil {
		return nil, fmt.Errorf("GPS_RAW_INT is not in the dialect")
	}

	switch ff := fr.(type) {
	case *frame.V2Frame:
		cp := *ff
		cp.IncompatibilityFlag &^= frame.V2FlagSigned
		cp.Signature = nil
		cp.Message = mp.Write(&patched, true)
		cp.Checksum = cp.GenerateChecksum(mp.CRCExtra())
		return &cp, nil
	case *frame.V1Frame:
		cp := *ff
		cp.Message = mp.Write(&patched, false)
		cp.Checksum = cp.GenerateChecksum(mp.CRCExtra())
		return &cp, nil
	default:
		return nil, fmt.Errorf("unsupported MAVLink frame %T", fr)
	}
}
