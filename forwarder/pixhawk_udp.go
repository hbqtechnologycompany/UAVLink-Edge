package forwarder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3/pkg/dialect"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/frame"
	"github.com/bluenviron/gomavlib/v3/pkg/message"
	"github.com/bluenviron/gomavlib/v3/pkg/streamwriter"

	"DroneBridge/config"
	"DroneBridge/logger"
)

// pixhawkUDPConn is a fixed-port UDP socket for PX4 ETH MAVLink (simulator pattern).
// PX4 registers partners by source IP:port — ephemeral ports break telemetry after restart.
// Bound to ethernet.local_ip when set — pairs with iptables exempt + table-100 eth route.
type pixhawkUDPConn struct {
	conn        *net.UDPConn
	remoteMu    sync.Mutex
	lastRemote  *net.UDPAddr
	fixedRemote *net.UDPAddr
}

type pixhawkBind struct {
	ip string // e.g. 10.41.10.10 — source IP on eth0
}

func pixhawkBindFromConfig(cfg *config.Config) pixhawkBind {
	b := pixhawkBind{}
	if cfg != nil && cfg.Ethernet.LocalIP != "" {
		b.ip = cfg.Ethernet.LocalIP
	}
	return b
}

func listenPixhawkUDP(port int, bind pixhawkBind) (*net.UDPConn, error) {
	host := "0.0.0.0"
	if bind.ip != "" {
		host = bind.ip
	}
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	udpConn, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("expected *net.UDPConn")
	}
	return udpConn, nil
}

func newPixhawkUDPConn(port int, cfg *config.Config) (*pixhawkUDPConn, error) {
	bind := pixhawkBindFromConfig(cfg)
	conn, err := listenPixhawkUDP(port, bind)
	if err != nil && bind.ip != "" {
		logger.Warn("[PARTNER_HB] bind %s:%d failed (%v) — retry 0.0.0.0:%d",
			bind.ip, port, err, port)
		conn, err = listenPixhawkUDP(port, pixhawkBind{})
	}
	if err != nil {
		return nil, fmt.Errorf("listen pixhawk udp :%d: %w", port, err)
	}
	logger.Info("[PARTNER_HB] UDP socket bound %s", conn.LocalAddr())
	pc := &pixhawkUDPConn{conn: conn}
	pc.setFixedTarget(cfg)
	return pc, nil
}

func (p *pixhawkUDPConn) setFixedTarget(cfg *config.Config) {
	if cfg == nil || cfg.Ethernet.PixhawkIP == "" {
		return
	}
	port := cfg.Ethernet.PixhawkPort
	if port <= 0 {
		port = 14550
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", cfg.Ethernet.PixhawkIP, port))
	if err != nil {
		logger.Warn("[PARTNER_HB] PX4 target %s:%d invalid: %v", cfg.Ethernet.PixhawkIP, port, err)
		return
	}
	p.remoteMu.Lock()
	p.fixedRemote = addr
	p.remoteMu.Unlock()
	logger.Info("[PARTNER_HB] Ethernet TX target %s", addr)
}

func (p *pixhawkUDPConn) Read(b []byte) (int, error) {
	n, remote, err := p.conn.ReadFromUDP(b)
	if err != nil {
		return n, err
	}
	p.remoteMu.Lock()
	p.lastRemote = remote
	p.remoteMu.Unlock()
	return n, err
}

func (p *pixhawkUDPConn) Write(b []byte) (int, error) {
	p.remoteMu.Lock()
	remote := p.fixedRemote
	if remote == nil {
		remote = p.lastRemote
	}
	p.remoteMu.Unlock()
	if remote == nil {
		return len(b), nil
	}
	return p.conn.WriteToUDP(b, remote)
}

func (p *pixhawkUDPConn) Close() error {
	return p.conn.Close()
}

func (p *pixhawkUDPConn) writeTo(remote *net.UDPAddr, data []byte) (int, error) {
	return p.conn.WriteToUDP(data, remote)
}

var (
	pixhawkUDP     *pixhawkUDPConn
	pixhawkUDPOnce sync.Once
)

func getOrCreatePixhawkUDP(port int, cfg *config.Config) (*pixhawkUDPConn, error) {
	var initErr error
	pixhawkUDPOnce.Do(func() {
		pixhawkUDP, initErr = newPixhawkUDPConn(port, cfg)
	})
	if initErr != nil {
		return nil, initErr
	}
	if pixhawkUDP == nil {
		return nil, fmt.Errorf("pixhawk UDP conn not initialized")
	}
	return pixhawkUDP, nil
}

func encodeMAVLinkV2(dialectRW *dialect.ReadWriter, sysID, compID uint8, msg message.Message) ([]byte, error) {
	buf := &bytes.Buffer{}
	frameWriter := &frame.Writer{
		ByteWriter: buf,
		DialectRW:  dialectRW,
	}
	if err := frameWriter.Initialize(); err != nil {
		return nil, err
	}
	streamWriter := &streamwriter.Writer{
		FrameWriter: frameWriter,
		Version:     streamwriter.V2,
		SystemID:    sysID,
		ComponentID: compID,
	}
	if err := streamWriter.Initialize(); err != nil {
		return nil, err
	}
	if err := streamWriter.Write(msg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func runPartnerHeartbeatRaw(pc *pixhawkUDPConn, targetAddr string) {
	remote, err := net.ResolveUDPAddr("udp4", targetAddr)
	if err != nil {
		logger.Error("[PARTNER_HB] Invalid target %s: %v", targetAddr, err)
		return
	}

	dialectRW := &dialect.ReadWriter{Dialect: common.Dialect}
	if err := dialectRW.Initialize(); err != nil {
		logger.Error("[PARTNER_HB] Dialect init failed: %v", err)
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var sentCount uint64
	firstSent := false
	lastInfoLog := time.Now()

	for range ticker.C {
		msg := &common.MessageHeartbeat{
			Type:         6, // MAV_TYPE_GCS
			Autopilot:    0,
			BaseMode:     0,
			CustomMode:   0,
			SystemStatus: 4, // MAV_STATE_ACTIVE
		}
		frame, err := encodeMAVLinkV2(dialectRW, partnerHeartbeatSysID, partnerHeartbeatCompID, msg)
		if err != nil {
			logger.Error("[PARTNER_HB] Encode failed: %v", err)
			continue
		}
		if _, err := pc.writeTo(remote, frame); err != nil {
			logger.Error("[PARTNER_HB] Failed to send heartbeat to %s: %v", targetAddr, err)
			continue
		}
		sentCount++
		if !firstSent {
			logger.Info("[PARTNER_HB] ✓ First HEARTBEAT sent from %s → %s (eth-bound)", pc.conn.LocalAddr(), targetAddr)
			firstSent = true
		} else if time.Since(lastInfoLog) >= 60*time.Second {
			logger.Info("[PARTNER_HB] HEARTBEAT 1 Hz active → %s (sent %d)", targetAddr, sentCount)
			lastInfoLog = time.Now()
		}
	}
}

// ensure pixhawkUDPConn implements io.ReadWriteCloser
var _ io.ReadWriteCloser = (*pixhawkUDPConn)(nil)
