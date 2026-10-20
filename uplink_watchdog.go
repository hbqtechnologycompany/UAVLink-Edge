package main

import (
	"fmt"
	"sync"
	"time"

	"DroneBridge/auth"
	"DroneBridge/forwarder"
	"DroneBridge/logger"
	"DroneBridge/metrics"
	"DroneBridge/network"
	"DroneBridge/vpn"
)

const (
	// Startup (runAuthWhenReady) brings up auth + VPN itself; start watching after that.
	uplinkWatchStartDelay = time.Minute
	uplinkCheckEvery      = 5 * time.Second
	// WireGuard re-keys every ~2 min while traffic flows and drops the session after 180 s
	// without one, so an older handshake means the tunnel carries nothing.
	vpnHandshakeStale = 200 * time.Second
	// Time allowed for the first handshake after the tunnel is (re)started.
	vpnFirstHandshakeGrace = 45 * time.Second
	vpnRetryMin            = 15 * time.Second
	vpnRetryMax            = 2 * time.Minute
)

var vpnStartMu sync.Mutex

// superviseUplink keeps the server path alive for the whole process lifetime, on whichever
// interface netmon routes the cloud uplink through (wlan0 or wwan0):
//   - uplink comes back after an outage → re-verify the auth session immediately;
//   - auth is up but the WireGuard tunnel is missing or has no recent handshake →
//     re-provision (0xB0), restart the tunnel and rebind the MAVLink sender.
func superviseUplink(authClient *auth.Client, vpnMgr *vpn.Manager, fwd *forwarder.Forwarder) {
	time.Sleep(uplinkWatchStartDelay)
	ticker := time.NewTicker(uplinkCheckEvery)
	defer ticker.Stop()

	egressUp := true
	retry := vpnRetryMin
	var nextVPNAttempt time.Time

	for range ticker.C {
		iface, ip, ok := network.PhysicalEgress()
		if !ok {
			if egressUp {
				logger.Warn("[UPLINK] Mất đường ra server (netmon chưa có cloud uplink) — chờ mạng trở lại")
				metrics.Global.AddLog("WARN", "Uplink lost — waiting for network")
			}
			egressUp = false
			continue
		}
		if !egressUp {
			egressUp = true
			logger.Info("[UPLINK] Mạng trở lại qua %s (%s) — kết nối lại server ngay", iface, ip)
			metrics.Global.AddLog("INFO", fmt.Sprintf("Uplink back on %s (%s)", iface, ip))
			retry = vpnRetryMin
			nextVPNAttempt = time.Time{}
			authClient.RecoverNow("uplink-restored")
			continue
		}

		// Before the first successful auth, startAuthWithRetry owns the connection.
		// Provisioning the tunnel needs a live auth TCP session.
		if !authClient.IsRunning() || !authClient.IsServerReachable() {
			continue
		}

		problem := vpnProblem(vpnMgr)
		if problem == "" {
			retry = vpnRetryMin
			continue
		}
		if time.Now().Before(nextVPNAttempt) {
			continue
		}
		logger.Warn("[UPLINK] VPN %s — cấp lại cấu hình và dựng lại tunnel", problem)
		metrics.Global.AddLog("WARN", "VPN "+problem+" — restarting tunnel")
		nextVPNAttempt = time.Now().Add(retry)
		retry = min(retry*2, vpnRetryMax)
		if err := startVPNAfterAuth(authClient, vpnMgr); err != nil {
			logger.Warn("[UPLINK] Dựng lại VPN thất bại: %v (thử lại sau %s)", err, time.Until(nextVPNAttempt).Round(time.Second))
			continue
		}
		fwd.RebindUplink("uplink-watchdog")
	}
}

// vpnProblem returns a short description when the tunnel cannot carry traffic, "" when healthy.
func vpnProblem(m *vpn.Manager) string {
	if !m.IsRunning() {
		return "chưa chạy"
	}
	age, ok := m.HandshakeAge()
	if !ok {
		if time.Since(m.StartedAt()) > vpnFirstHandshakeGrace {
			return "không bắt tay được với server"
		}
		return ""
	}
	if age > vpnHandshakeStale {
		return fmt.Sprintf("mất bắt tay %.0fs", age.Seconds())
	}
	return ""
}
