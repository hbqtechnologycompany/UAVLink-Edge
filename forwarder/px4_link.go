package forwarder

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v3/pkg/message"

	"DroneBridge/config"
	"DroneBridge/logger"
	"DroneBridge/web"
)

const (
	linkHealthPeriod    = 500 * time.Millisecond
	linkHeartbeatPeriod = time.Second
	// Health checks start 250ms after the supervisor so they never land on the 1 Hz heartbeat.
	linkHealthOffset = 250 * time.Millisecond
)

type linkID int

const (
	linkNone linkID = iota
	linkEth
	linkTelem2
	linkTelem3
)

func (id linkID) String() string {
	switch id {
	case linkEth:
		return "ethernet"
	case linkTelem2:
		return "telem2"
	case linkTelem3:
		return "telem3"
	default:
		return "none"
	}
}

type linkPhase int

const (
	phaseNone linkPhase = iota
	phaseHealth
	phaseHeartbeat
)

type linkSnap struct {
	active         linkID
	entered        time.Time
	holdUntil      time.Time
	ethLost        bool
	ethHBSinceLoss int
}

// stepLink picks at most one link change.
// Ethernet recovery is decided before any UART loss, and a link that was just
// entered is not judged until its own window ends.
func stepLink(s linkSnap, now time.Time, fresh, open [4]bool, loss time.Duration, recoverN int) (linkSnap, string) {
	if s.active == linkNone {
		s.active = linkEth
	}
	if recoverN < 1 {
		recoverN = 2
	}
	if loss <= 0 {
		loss = 2 * time.Second
	}

	if s.active != linkEth && s.ethLost && s.ethHBSinceLoss >= recoverN && fresh[linkEth] && open[linkEth] {
		s.active = linkEth
		s.entered = now
		s.holdUntil = time.Time{}
		s.ethLost = false
		s.ethHBSinceLoss = 0
		return s, "eth-recovered"
	}

	if !s.holdUntil.IsZero() && now.Before(s.holdUntil) && open[s.active] {
		return s, ""
	}

	switch s.active {
	case linkEth:
		if fresh[linkEth] || now.Sub(s.entered) < loss {
			return s, ""
		}
		s.ethLost = true
		s.ethHBSinceLoss = 0
		next := linkNone
		reason := ""
		if open[linkTelem2] {
			next = linkTelem2
			reason = "eth-lost"
		} else if open[linkTelem3] {
			next = linkTelem3
			reason = "eth-lost-telem2-absent"
		}
		if next == linkNone {
			return s, ""
		}
		s.active = next
		s.entered = now
		s.holdUntil = now.Add(loss)
		return s, reason
	case linkTelem2:
		if fresh[linkTelem2] && open[linkTelem2] {
			return s, ""
		}
		if open[linkTelem3] {
			s.active = linkTelem3
			s.entered = now
			s.holdUntil = now.Add(loss)
			return s, "telem2-lost"
		}
		return s, ""
	case linkTelem3:
		if fresh[linkTelem2] && open[linkTelem2] {
			s.active = linkTelem2
			s.entered = now
			s.holdUntil = now.Add(loss)
			return s, "telem2-restored"
		}
		return s, ""
	default:
		s.active = linkEth
		s.entered = now
		return s, ""
	}
}

// nextLinkPhase returns the single action due at now.
// If a stall makes both phases due, health runs and the heartbeat is pushed
// 200ms later so the two never execute in the same turn.
func nextLinkPhase(now, nextHealth, nextHB time.Time) (linkPhase, time.Time, time.Time, bool) {
	healthDue := !nextHealth.After(now)
	hbDue := !nextHB.After(now)
	switch {
	case healthDue && hbDue:
		return phaseHealth, advanceDeadline(nextHealth, linkHealthPeriod, now), now.Add(200 * time.Millisecond), true
	case hbDue:
		return phaseHeartbeat, nextHealth, advanceDeadline(nextHB, linkHeartbeatPeriod, now), true
	case healthDue:
		return phaseHealth, advanceDeadline(nextHealth, linkHealthPeriod, now), nextHB, true
	default:
		return phaseNone, nextHealth, nextHB, false
	}
}

func advanceDeadline(due time.Time, period time.Duration, now time.Time) time.Time {
	next := due.Add(period)
	for !next.After(now) {
		next = next.Add(period)
	}
	return next
}

type px4Arbiter struct {
	mu   sync.Mutex
	txMu sync.Mutex

	telem2Dev string
	telem3Dev string
	loss      time.Duration
	recoverN  int

	ch     [4]*gomavlib.Channel
	lastHB [4]time.Time
	snap   linkSnap
	gen    uint64
}

var px4Links = &px4Arbiter{
	telem2Dev: "/dev/ttyAMA2",
	telem3Dev: "/dev/ttyAMA3",
	loss:      2 * time.Second,
	recoverN:  2,
	snap:      linkSnap{active: linkEth},
}

// uartBaud is the CM5 rate for both TELEM ports. 921600 and 57600 are the old caps.
func uartBaud(b int) int {
	if b <= 0 || b == 921600 || b == 57600 {
		return 1500000
	}
	return b
}

func configurePX4Links(cfg *config.Config) (telem2 string, telem2Baud int, telem3 string, telem3Baud int) {
	telem2 = "/dev/ttyAMA2"
	telem2Baud = 1500000
	telem3 = "/dev/ttyAMA3"
	telem3Baud = 1500000
	loss := 2 * time.Second
	recoverN := 2
	if cfg != nil {
		if cfg.Network.Telem2Port != "" {
			telem2 = cfg.Network.Telem2Port
		} else if cfg.Network.SerialPort != "" {
			telem2 = cfg.Network.SerialPort
		}
		if cfg.Network.Telem2Baud > 0 {
			telem2Baud = uartBaud(cfg.Network.Telem2Baud)
		}
		if cfg.Network.Telem3Port != "" {
			telem3 = cfg.Network.Telem3Port
		}
		if cfg.Network.Telem3Baud > 0 {
			telem3Baud = uartBaud(cfg.Network.Telem3Baud)
		}
		if cfg.Network.LinkLossTimeoutMs > 0 {
			loss = time.Duration(cfg.Network.LinkLossTimeoutMs) * time.Millisecond
		}
		if cfg.Network.EthRecoverHeartbeats > 0 {
			recoverN = cfg.Network.EthRecoverHeartbeats
		}
	}

	px4Links.mu.Lock()
	px4Links.telem2Dev = telem2
	px4Links.telem3Dev = telem3
	px4Links.loss = loss
	px4Links.recoverN = recoverN
	if px4Links.snap.entered.IsZero() {
		px4Links.snap.active = linkEth
		px4Links.snap.entered = time.Now()
	}
	px4Links.mu.Unlock()
	return telem2, telem2Baud, telem3, telem3Baud
}

func (a *px4Arbiter) identify(ch *gomavlib.Channel) linkID {
	if ch == nil || ch.Endpoint() == nil {
		return linkNone
	}
	switch conf := ch.Endpoint().Conf().(type) {
	case gomavlib.EndpointSerial:
		a.mu.Lock()
		t2, t3 := a.telem2Dev, a.telem3Dev
		a.mu.Unlock()
		switch conf.Device {
		case t2:
			return linkTelem2
		case t3:
			return linkTelem3
		default:
			return linkNone
		}
	case gomavlib.EndpointCustom:
		return linkEth
	default:
		return linkNone
	}
}

func (a *px4Arbiter) register(ch *gomavlib.Channel) {
	id := a.identify(ch)
	if id == linkNone || ch == nil {
		return
	}
	a.mu.Lock()
	a.ch[id] = ch
	a.mu.Unlock()
}

func (a *px4Arbiter) unregister(ch *gomavlib.Channel) {
	if ch == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.ch {
		if a.ch[id] == ch {
			a.ch[id] = nil
		}
	}
}

func (a *px4Arbiter) noteHeartbeat(id linkID, now time.Time) {
	if id == linkNone {
		return
	}
	a.mu.Lock()
	a.lastHB[id] = now
	if id == linkEth && a.snap.ethLost && a.snap.active != linkEth && a.snap.ethHBSinceLoss < 1000 {
		a.snap.ethHBSinceLoss++
	}
	a.mu.Unlock()
}

// observe records which physical link produced a frame and whether that frame
// may be forwarded. Backup links stay receive-only.
func (a *px4Arbiter) observe(ch *gomavlib.Channel, msg message.Message, sysID, compID uint8, now time.Time) (forward bool, gen uint64) {
	id := a.identify(ch)
	if id != linkNone && ch != nil {
		a.mu.Lock()
		a.ch[id] = ch
		a.mu.Unlock()
	}
	if hb, ok := msg.(*common.MessageHeartbeat); ok && isPX4AutopilotHeartbeat(sysID, compID, hb) {
		a.noteHeartbeat(id, now)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return id != linkNone && id == a.snap.active, a.gen
}

func isPX4AutopilotHeartbeat(sysID, compID uint8, hb *common.MessageHeartbeat) bool {
	if hb == nil || sysID == 0 || sysID == 255 || compID != 1 {
		return false
	}
	return hb.Autopilot == common.MAV_AUTOPILOT_PX4
}

func (a *px4Arbiter) evaluate(now time.Time) {
	a.txMu.Lock()
	a.mu.Lock()
	fresh := [4]bool{}
	open := [4]bool{}
	for id := linkEth; id <= linkTelem3; id++ {
		open[id] = a.ch[id] != nil
		last := a.lastHB[id]
		fresh[id] = !last.IsZero() && now.Sub(last) < a.loss
	}
	next, reason := stepLink(a.snap, now, fresh, open, a.loss, a.recoverN)
	if reason != "" && next.active != a.snap.active {
		a.gen++
	}
	a.snap = next
	name := next.active.String()
	var ageMs int64 = -1
	if !a.lastHB[next.active].IsZero() {
		ageMs = now.Sub(a.lastHB[next.active]).Milliseconds()
	}
	a.mu.Unlock()
	a.txMu.Unlock()

	web.SetPX4LinkStatus(name, ageMs)
	if reason != "" {
		logger.Info("[MAVLINK] PX4 link → %s (%s)", name, reason)
	}
}

func gcsHeartbeat() *common.MessageHeartbeat {
	return &common.MessageHeartbeat{
		Type:         common.MAV_TYPE_GCS,
		Autopilot:    common.MAV_AUTOPILOT_INVALID,
		BaseMode:     0,
		CustomMode:   0,
		SystemStatus: common.MAV_STATE_ACTIVE,
	}
}

func (a *px4Arbiter) sendHeartbeat(node *gomavlib.Node) {
	if node == nil {
		return
	}
	a.txMu.Lock()
	defer a.txMu.Unlock()
	a.mu.Lock()
	id := a.snap.active
	ch := a.ch[id]
	a.mu.Unlock()
	if ch == nil {
		return
	}
	if err := node.WriteMessageTo(ch, gcsHeartbeat()); err != nil {
		logger.Warn("[PARTNER_HB] HEARTBEAT on %s failed: %v", id, err)
	}
}

func (a *px4Arbiter) write(node *gomavlib.Node, msg message.Message) error {
	if node == nil || msg == nil {
		return fmt.Errorf("PX4 link not ready")
	}
	a.txMu.Lock()
	defer a.txMu.Unlock()
	a.mu.Lock()
	id := a.snap.active
	ch := a.ch[id]
	a.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no open channel on PX4 link %s", id)
	}
	return node.WriteMessageTo(ch, msg)
}

func writeToActivePX4(node *gomavlib.Node, msg message.Message) error {
	return px4Links.write(node, msg)
}

var px4SupervisorOnce sync.Once

func installPX4CommandWriter(node *gomavlib.Node) {
	web.SetPX4CommandWriter(func(msg message.Message) error {
		return writeToActivePX4(node, msg)
	})
}

func startPX4LinkSupervisor(node *gomavlib.Node, cfg *config.Config, stop <-chan struct{}) {
	px4SupervisorOnce.Do(func() {
		installPX4CommandWriter(node)
		px4Links.mu.Lock()
		px4Links.snap.active = linkEth
		px4Links.snap.entered = time.Now()
		px4Links.snap.holdUntil = time.Time{}
		px4Links.snap.ethLost = false
		px4Links.snap.ethHBSinceLoss = 0
		loss := px4Links.loss
		recoverN := px4Links.recoverN
		t2 := px4Links.telem2Dev
		t3 := px4Links.telem3Dev
		px4Links.mu.Unlock()
		web.SetPX4LinkStatus(linkEth.String(), -1)
		logger.Info("[MAVLINK] Link arbiter: ETH first (loss %s, recover after %d PX4 HEARTBEAT), TELEM2 %s and TELEM3 %s at 1500000 8N1, no hardware flow control. Same Normal stream on all three links. Command TX on one link.",
			loss, recoverN, t2, t3)
		go px4Links.watchEthernetBroadcast(cfg, stop)
		go px4Links.loop(node, stop)
	})
}

func (a *px4Arbiter) loop(node *gomavlib.Node, stop <-chan struct{}) {
	start := time.Now()
	nextHealth := start.Add(linkHealthOffset)
	nextHB := start.Add(linkHeartbeatPeriod)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var hbSent uint64
	var hbLogged time.Time

	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			phase, healthAt, hbAt, run := nextLinkPhase(now, nextHealth, nextHB)
			nextHealth, nextHB = healthAt, hbAt
			if !run {
				continue
			}
			switch phase {
			case phaseHealth:
				a.evaluate(now)
			case phaseHeartbeat:
				a.mu.Lock()
				name := a.snap.active.String()
				a.mu.Unlock()
				a.sendHeartbeat(node)
				hbSent++
				if hbLogged.IsZero() || now.Sub(hbLogged) >= 60*time.Second {
					logger.Info("[PARTNER_HB] GCS HEARTBEAT 1 Hz on %s only (sent %d, sysid=%d compid=%d type=GCS)",
						name, hbSent, partnerHeartbeatSysID, partnerHeartbeatCompID)
					hbLogged = now
				}
			}
		}
	}
}

func (a *px4Arbiter) watchEthernetBroadcast(cfg *config.Config, stop <-chan struct{}) {
	if cfg == nil || cfg.Ethernet.BroadcastIP == "" {
		return
	}
	port := cfg.Network.LocalListenPort
	if port <= 0 {
		port = 14550
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", cfg.Ethernet.BroadcastIP, port))
	if err != nil {
		logger.Warn("[MAVLINK] Ethernet broadcast watch disabled: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		logger.Warn("[MAVLINK] Ethernet broadcast watch on %s failed: %v", addr, err)
		return
	}
	logger.Info("[MAVLINK] Ethernet broadcast watch %s (health only, no command TX)", addr)
	go func() {
		<-stop
		conn.Close()
	}()

	pixhawkIP := net.ParseIP(cfg.Ethernet.PixhawkIP)
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			return
		}
		if pixhawkIP != nil && (remote == nil || !remote.IP.Equal(pixhawkIP)) {
			continue
		}
		if frameHasPX4Heartbeat(buf[:n]) {
			a.noteHeartbeat(linkEth, time.Now())
		}
	}
}

func frameHasPX4Heartbeat(buf []byte) bool {
	for i := 0; i+12 <= len(buf); i++ {
		if buf[i] != 0xFD {
			continue
		}
		plen := int(buf[i+1])
		if plen < 9 || i+10+plen+2 > len(buf) {
			continue
		}
		msgid := uint32(buf[i+7]) | uint32(buf[i+8])<<8 | uint32(buf[i+9])<<16
		if msgid != 0 {
			continue
		}
		sysid := buf[i+5]
		compid := buf[i+6]
		if sysid == 0 || sysid == 255 || compid != 1 {
			continue
		}
		payload := buf[i+10 : i+10+plen]
		// HEARTBEAT wire order: custom_mode u32, type, autopilot, base_mode, system_status, mavlink_version.
		if payload[5] != 12 {
			continue
		}
		body := buf[i+1 : i+10+plen]
		crc := mavlinkCRC(body, 50)
		got := uint16(buf[i+10+plen]) | uint16(buf[i+10+plen+1])<<8
		if crc == got {
			return true
		}
	}
	return false
}

func mavlinkCRC(data []byte, extra byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = crcAccumulate(b, crc)
	}
	return crcAccumulate(extra, crc)
}

func crcAccumulate(b byte, crc uint16) uint16 {
	tmp := uint8(crc&0xFF) ^ b
	tmp ^= tmp << 4
	return (crc >> 8) ^ (uint16(tmp) << 8) ^ (uint16(tmp) << 3) ^ (uint16(tmp) >> 4)
}
