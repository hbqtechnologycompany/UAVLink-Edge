package forwarder

import (
	"testing"
	"time"
)

func TestStepLinkDoesNotSkipOrOverlap(t *testing.T) {
	loss := 2 * time.Second
	t0 := time.Unix(0, 0)
	openAll := [4]bool{linkEth: true, linkTelem2: true, linkTelem3: true}

	s := linkSnap{active: linkEth, entered: t0}
	// Inside the Ethernet window, a dead Ethernet link is not failed over.
	next, reason := stepLink(s, t0.Add(1999*time.Millisecond), [4]bool{}, openAll, loss, 2)
	if reason != "" || next.active != linkEth {
		t.Fatalf("ETH window overlapped UART: %+v %q", next, reason)
	}

	// One tick after the window: Ethernet → TELEM2 only. TELEM3 is not judged.
	next, reason = stepLink(s, t0.Add(2*time.Second), [4]bool{}, openAll, loss, 2)
	if reason != "eth-lost" || next.active != linkTelem2 {
		t.Fatalf("expected eth-lost → telem2, got %s %q", next.active, reason)
	}
	if next.ethHBSinceLoss != 0 || !next.ethLost {
		t.Fatalf("ETH loss did not arm recovery: %+v", next)
	}

	// The TELEM2 window just opened. Silence must not fall through to TELEM3.
	held, reason := stepLink(next, t0.Add(3*time.Second), [4]bool{}, openAll, loss, 2)
	if reason != "" || held.active != linkTelem2 {
		t.Fatalf("TELEM2 hold overlapped TELEM3: %+v %q", held, reason)
	}

	// One PX4 heartbeat on Ethernet is not enough to pull commands off UART.
	early := next
	early.ethHBSinceLoss = 1
	freshEth := [4]bool{linkEth: true}
	back, reason := stepLink(early, t0.Add(3*time.Second), freshEth, openAll, loss, 2)
	if reason != "" || back.active != linkTelem2 {
		t.Fatalf("single ETH heartbeat recovered too early: %+v %q", back, reason)
	}

	// Two heartbeats recover Ethernet and do not also touch TELEM3.
	early.ethHBSinceLoss = 2
	back, reason = stepLink(early, t0.Add(3*time.Second), freshEth, openAll, loss, 2)
	if reason != "eth-recovered" || back.active != linkEth || back.ethLost {
		t.Fatalf("expected eth-recovered, got %+v %q", back, reason)
	}

	// After TELEM2's own window, silence moves to TELEM3 and stops there.
	afterHold := next.holdUntil
	down, reason := stepLink(next, afterHold, [4]bool{}, openAll, loss, 2)
	if reason != "telem2-lost" || down.active != linkTelem3 {
		t.Fatalf("expected telem2-lost → telem3, got %s %q", down.active, reason)
	}
	stay, reason := stepLink(down, down.holdUntil.Add(-time.Millisecond), [4]bool{}, openAll, loss, 2)
	if reason != "" || stay.active != linkTelem3 {
		t.Fatalf("TELEM3 hold overlapped a second switch: %+v %q", stay, reason)
	}

	// TELEM2 coming back promotes one step, and does not jump to Ethernet.
	fresh2 := [4]bool{linkTelem2: true}
	up, reason := stepLink(down, down.holdUntil, fresh2, openAll, loss, 2)
	if reason != "telem2-restored" || up.active != linkTelem2 {
		t.Fatalf("expected telem2-restored, got %s %q", up.active, reason)
	}

	// Ethernet wins over a healthy TELEM2 in the same decision.
	both := down
	both.ethLost = true
	both.ethHBSinceLoss = 2
	both.holdUntil = time.Time{}
	win, reason := stepLink(both, t0.Add(20*time.Second), [4]bool{linkEth: true, linkTelem2: true}, openAll, loss, 2)
	if reason != "eth-recovered" || win.active != linkEth {
		t.Fatalf("ETH recovery did not preempt TELEM2: %+v %q", win, reason)
	}
}

func TestStepLinkSkipsMissingTelem2(t *testing.T) {
	t0 := time.Unix(0, 0)
	s := linkSnap{active: linkEth, entered: t0}
	open := [4]bool{linkEth: true, linkTelem3: true}
	next, reason := stepLink(s, t0.Add(2*time.Second), [4]bool{}, open, 2*time.Second, 2)
	if reason != "eth-lost-telem2-absent" || next.active != linkTelem3 {
		t.Fatalf("expected skip to telem3, got %s %q", next.active, reason)
	}
}

func TestLinkPhasesNeverShareATurn(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	nextHealth := start.Add(linkHealthOffset)
	nextHB := start.Add(linkHeartbeatPeriod)
	var healthN, hbN int
	for now := start; now.Before(start.Add(10 * time.Second)); now = now.Add(50 * time.Millisecond) {
		phase, healthAt, hbAt, run := nextLinkPhase(now, nextHealth, nextHB)
		if healthAt.Equal(hbAt) {
			t.Fatalf("health and heartbeat deadlines collided at %s", now)
		}
		nextHealth, nextHB = healthAt, hbAt
		if !run {
			continue
		}
		switch phase {
		case phaseHealth:
			healthN++
		case phaseHeartbeat:
			hbN++
		default:
			t.Fatalf("unexpected phase %d", phase)
		}
	}
	if healthN < 15 || hbN < 8 {
		t.Fatalf("phases starved: health=%d heartbeat=%d", healthN, hbN)
	}
}

func TestUartBaud(t *testing.T) {
	if uartBaud(0) != 1500000 || uartBaud(57600) != 1500000 || uartBaud(921600) != 1500000 {
		t.Fatal("old UART rates must become 1500000")
	}
	if uartBaud(1500000) != 1500000 {
		t.Fatal("1500000 should stay")
	}
}

func TestSeqHistDropsOverlapCopy(t *testing.T) {
	var h seqHist
	if h.duplicate(1) {
		t.Fatal("first frame is not a duplicate")
	}
	if !h.duplicate(1) {
		t.Fatal("immediate copy should drop")
	}
	if h.duplicate(2) || h.duplicate(3) {
		t.Fatal("new sequences dropped")
	}
	if !h.duplicate(1) {
		t.Fatal("late unicast/broadcast copy should drop")
	}
}

func TestFrameHasPX4Heartbeat(t *testing.T) {
	px4 := testHeartbeatFrame(1, 1, 12)
	if !frameHasPX4Heartbeat(px4) {
		t.Fatalf("PX4 heartbeat not recognized (%d bytes) %x", len(px4), px4)
	}
	if frameHasPX4Heartbeat(testHeartbeatFrame(255, 190, 0)) {
		t.Fatal("GCS heartbeat counted as PX4")
	}
	if frameHasPX4Heartbeat(testHeartbeatFrame(1, 1, 8)) {
		t.Fatal("non-PX4 autopilot counted as PX4")
	}
	px4[len(px4)-1] ^= 0xFF
	if frameHasPX4Heartbeat(px4) {
		t.Fatal("corrupt frame counted as PX4")
	}
}

func testHeartbeatFrame(sysID, compID, autopilot byte) []byte {
	payload := []byte{0, 0, 0, 0, 2, autopilot, 0, 4, 3}
	header := []byte{0xFD, byte(len(payload)), 0, 0, 0, sysID, compID, 0, 0, 0}
	body := append(append([]byte{}, header[1:]...), payload...)
	crc := mavlinkCRC(body, 50)
	frame := append(append([]byte{}, header...), payload...)
	return append(frame, byte(crc), byte(crc>>8))
}
