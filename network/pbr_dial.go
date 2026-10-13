package network

import (
	"net"
	"syscall"
	"time"
)

// PBRMark is fwmark 0x1 → routing table 100 (dronebridge), set by setup_pbr.sh.
const PBRMark = 0x1

func setPBRMark(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, PBRMark)
	})
}

// DialTCP opens a TCP connection using PBR table 100 for route lookup at connect time.
// iptables uid-owner marking alone is too late for the initial SYN route decision.
func DialTCP(timeout time.Duration, network, address string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: timeout,
		Control: setPBRMark,
	}
	return d.Dial(network, address)
}
