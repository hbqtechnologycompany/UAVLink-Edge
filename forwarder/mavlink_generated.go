package forwarder

import (
	"fmt"
	"sync"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/message"

	"DroneBridge/companion"
	"DroneBridge/logger"
	"DroneBridge/mavlink_custom"
	"DroneBridge/metrics"
)

func createOutboundNode(endpoint gomavlib.EndpointConf, sysID, compID uint8) (*gomavlib.Node, error) {
	return gomavlib.NewNode(gomavlib.NodeConf{
		Endpoints:        []gomavlib.EndpointConf{endpoint},
		Dialect:          mavlink_custom.GetCombinedDialect(),
		OutVersion:       gomavlib.V2,
		OutSystemID:      sysID,
		OutComponentID:   compID,
		HeartbeatDisable: true,
	})
}

func (f *Forwarder) initCameraSender(endpoint gomavlib.EndpointConf) error {
	// Chuẩn MAVLink + QCC: camera trên cùng system_id máy bay (PX4), comp_id=100.
	aircraftSysID := resolveOutSystemID()
	node, err := createOutboundNode(endpoint, aircraftSysID, companion.CompCamera)
	if err != nil {
		return err
	}
	f.cameraSenderMu.Lock()
	if f.cameraSenderNode != nil {
		f.cameraSenderNode.Close()
	}
	f.cameraSenderNode = node
	f.cameraSenderMu.Unlock()
	logger.Info("[CM5][MAVLINK] Camera sender sys=%d comp=%d (CM5 → server, cùng sys PX4)", aircraftSysID, companion.CompCamera)
	return nil
}

// PublishGenerated sends a MAVLink message to the cloud router (autopilot component).
func (f *Forwarder) PublishGenerated(msg message.Message) error {
	return f.publishViaNode(&f.senderMu, f.senderNode, msg)
}

// PublishCamera sends camera MAVLink từ CM5 (sys=PX4, comp=100).
func (f *Forwarder) PublishCamera(msg message.Message) error {
	return f.publishViaNode(&f.cameraSenderMu, f.cameraSenderNode, msg)
}

func (f *Forwarder) publishViaNode(mu *sync.RWMutex, node *gomavlib.Node, msg message.Message) error {
	mu.RLock()
	n := node
	mu.RUnlock()
	if n == nil {
		return fmt.Errorf("mavlink outbound node not ready")
	}
	if err := n.WriteMessageAll(msg); err != nil {
		return err
	}
	f.incMsgOut(msg.GetID())
	f.incPktOutServerGenerated()
	metrics.Global.IncSent(getMessageTypeName(msg))
	return nil
}

// requestPixhawkTelemetryStreams does not change PX4 rates.
// Ethernet, TELEM2 and TELEM3 already share the Normal set under a 100000 byte/s cap.
func (f *Forwarder) requestPixhawkTelemetryStreams() {
	logger.Info("[MAVLINK] Using PX4 Normal stream on the active link (100000 byte/s, no SET_MESSAGE_INTERVAL)")
}
