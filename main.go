package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"DroneBridge/auth"
	"DroneBridge/camera"
	"DroneBridge/config"
	"DroneBridge/forwarder"
	"DroneBridge/logger"
	"DroneBridge/network"
	"DroneBridge/vpn"
	"DroneBridge/web"
)

// startVPNAfterAuth follows QCC / STARTUP_FLOW: TCP auth session first, then 0xB0
// provision (updates WG peer endpoint on uplink change), then bring up userspace WG.
func startVPNAfterAuth(authClient *auth.Client, vpnMgr *vpn.Manager) error {
	if vpnMgr == nil {
		return nil
	}
	vpnStartMu.Lock()
	defer vpnStartMu.Unlock()

	logger.Info("[VPN] Post-auth: requesting VPN provisioning from router (0xB0)...")
	if err := authClient.RequestVPNProvisioning(); err != nil {
		logger.Warn("[VPN] Provisioning failed: %v — trying cached vpn_config.json", err)
	}

	if vpnMgr.IsRunning() {
		vpnMgr.Stop()
	}
	if err := vpnMgr.Start(); err != nil {
		return err
	}
	logger.Info("[VPN] ✅ WireGuard tunnel up (assigned %s)", vpnMgr.GetAssignedIP())
	return nil
}

// watchPixhawkDeadline checks the SysID flag after the deadline. Startup does not wait on it.
func watchPixhawkDeadline(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	<-timer.C
	if web.GetPixhawkSystemID() == 0 {
		logger.Fatal("[STARTUP] Pixhawk connection failed. Set 'allow_missing_pixhawk: true' in config to skip this requirement.")
	}
}

// runAuthWhenReady reads cloud_ready once, authenticates without holding startup,
// and checks the flag again later if it was still clear.
func runAuthWhenReady(authClient *auth.Client, vpnMgr *vpn.Manager, fwd *forwarder.Forwarder, retrySec int) {
	if iface, ip, ok := network.PhysicalEgress(); ok {
		logger.Info("[STARTUP] Cloud egress ready: %s (%s)", iface, ip)
	} else {
		logger.Warn("[STARTUP] cloud_ready chưa bật — xác thực trên đường hiện có, kiểm tra lại khi cờ đổi")
		go watchCloudAndRebind(vpnMgr, fwd)
	}
	logger.Info("[STARTUP] Authenticating with router server...")
	if err := authClient.Start(); err != nil {
		logger.Warn("[AUTH] Initial authentication failed: %v", err)
		logger.Warn("[AUTH] Service will continue running and retry in background")
		startAuthWithRetry(authClient, vpnMgr, fwd, retrySec)
		return
	}
	logger.Info("✅ Successfully authenticated with router (Session ready)")
	if err := startVPNAfterAuth(authClient, vpnMgr); err != nil {
		logger.Warn("[VPN] Tunnel start failed after auth: %v", err)
		return
	}
	fwd.RebindUplink("post-auth-vpn-ready")
}

func watchCloudAndRebind(vpnMgr *vpn.Manager, fwd *forwarder.Forwarder) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		iface, ip, ok := network.PhysicalEgress()
		if !ok || vpnMgr == nil || !vpnMgr.IsRunning() {
			continue
		}
		logger.Info("[STARTUP] Cloud egress ready: %s (%s)", iface, ip)
		fwd.RebindUplink("cloud-ready")
		return
	}
}

func startAuthWithRetry(authClient *auth.Client, vpnMgr *vpn.Manager, fwd *forwarder.Forwarder, retrySec int) {
	if retrySec <= 0 {
		retrySec = 15
	}
	interval := time.Duration(retrySec) * time.Second
	go func() {
		for {
			time.Sleep(interval)
			logger.Warn("[AUTH] Retrying authentication in background (every %ds)...", retrySec)
			if err := authClient.Start(); err != nil {
				logger.Warn("[AUTH] Background authentication retry failed: %v", err)
				continue
			}
			logger.Info("[AUTH] ✅ Background authentication retry succeeded")
			if err := startVPNAfterAuth(authClient, vpnMgr); err != nil {
				logger.Warn("[VPN] Background VPN start failed after auth retry: %v", err)
			} else if fwd != nil {
				fwd.RebindUplink("auth-retry-vpn-ready")
			}
			return
		}
	}()
}

func main() {
	// Parse command-line flags
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	logLevel := flag.String("log", "", "Log level: debug, info, warn, error (overrides config)")
	register := flag.Bool("register", false, "Register this drone with the fleet server")
	syncDir := flag.String("sync-dir", "", "Directory for multi-drone synchronization files (enables sync mode)")
	droneID := flag.String("drone-id", "", "Drone ID for synchronization (required if sync-dir is set)")
	flag.Parse()

	// Load configuration
	logger.Info("Loading configuration from %s", *configFile)
	cfg, err := config.Load(*configFile)
	if err != nil {
		logger.Fatal("Failed to load configuration: %v", err)
	}

	// Set log level from config or command line
	if *logLevel != "" {
		logger.SetLevelFromString(*logLevel)
	} else {
		logger.SetLevelFromString(cfg.Log.Level)
	}

	// Set timestamp format from config
	if cfg.Log.TimestampFormat != "" {
		logger.SetTimestampFormat(cfg.Log.TimestampFormat)
	}

	// Route stdlib log.Printf (used by auth/web packages) through logger filters.
	logger.InstallStdLogBridge()

	// Configure runtime log scopes from config.
	logger.ConfigureScopeFilters(cfg.Log.ServerConnectionOnly, cfg.Log.ShowWebInteractionLogs)
	logger.ConfigureOutputFilters(false, true, true, cfg.Log.ShowPacketStats)

	logger.Info("Configuration loaded successfully (Log level: %s)", logger.GetLevelString())

	web.InitAppConfig(*configFile)

	// Validate sync parameters
	if *syncDir != "" && *droneID == "" {
		logger.Fatal("--drone-id is required when --sync-dir is specified")
	}

	// Create sync manager if enabled
	var syncManager *forwarder.SyncManager
	if *syncDir != "" {
		syncManager = forwarder.NewSyncManager(*syncDir, *droneID)
		logger.Info("🔄 Multi-drone synchronization enabled: syncDir=%s, droneID=%s", *syncDir, *droneID)
	}

	// Create single auth client instance - will be reused for both registration and normal operation
	authClient := auth.NewClient(
		cfg.Auth.Host,
		cfg.Auth.Port,
		cfg.Auth.UUID,
		cfg.Auth.SharedSecret,
		cfg.Auth.KeepaliveInterval,
	)
	authClient.SetVPNRouterIP(cfg.VPN.RouterVPNIP)
	if cfg.Auth.ReconnectInterval > 0 {
		authClient.SetReconnectInterval(cfg.Auth.ReconnectInterval)
	}

	var vpnMgr *vpn.Manager
	if cfg.VPN.Enabled {
		logger.Info("[VPN] VPN provisioning ENABLED (config: %s)", cfg.VPN.ConfigFile)
		vpnMgr = vpn.NewManager(cfg.VPN.ConfigFile)
		authClient.SetVPNManager(vpnMgr)
	}
	// Handle registration mode - SEPARATE from auth
	if *register {
		logger.Info("🚀 STARTING REGISTRATION PROCESS")
		logger.Info("Connecting to %s:%d", cfg.Auth.Host, cfg.Auth.Port)

		authClient.SetRegistrationMeta(cfg.Auth.VehicleType, cfg.Auth.Model)
		if cfg.Auth.VehicleType != 0 || cfg.Auth.Model != "" {
			logger.Info("Registration meta: vehicle_type=%d, model=%q", cfg.Auth.VehicleType, cfg.Auth.Model)
		}

		if err := authClient.Register(); err != nil {
			logger.Fatal("❌ Registration failed: %v", err)
		}

		logger.Info("✅ Registration completed successfully!")
		logger.Info("Secret key has been saved to .drone_secret")
		logger.Info("Run the service WITHOUT --register to start normal operation.")
		// Registration is a one-time setup. Exit here so the service unit file
		// (which must NOT include --register) can be used for normal restarts.
		// If the process fell through and the service had --register + Restart=always,
		// every crash/restart would re-register instead of re-authenticating.
		os.Exit(0)
	}

	logger.Info("Listening on port %d, forwarding to %s",
		cfg.Network.LocalListenPort, cfg.GetAddress())

	// STEP 0: Create listener node ONLY (to listen for Pixhawk, no sender yet)
	logger.Info("[STARTUP] Creating MAVLink listener for Pixhawk...")
	listenerNode, err := forwarder.NewListener(cfg)
	if err != nil {
		logger.Fatal("Failed to create listener: %v", err)
	}

	// Initialize MAVLink bridge EARLY with listener node (for web access)
	web.InitMAVLinkBridge(listenerNode)
	// Web listens before Pixhawk and cloud waits so the settings page stays up.
	web.StartServer(cfg.Web.Port, authClient, cfg.Auth.UUID)
	if cfg.Camera.Enabled && cfg.Camera.AutoStart {
		go func() {
			logger.Info("[CAMERA] Camera streaming enabled, auto-starting from config.yaml...")
			if pids, err := web.BootstrapCameraStreams(); err != nil {
				logger.Error("[CAMERA] Failed to start camera streaming: %v", err)
			} else {
				logger.Info("[CAMERA] ✅ Camera streaming started (PIDs: %v)", pids)
			}
			if result, err := camera.ScanLive(true); err == nil && result != nil {
				logger.Info("[CAMERA] Detect: %d/%d connected, %d sensor types supported",
					result.ConnectedCount, result.ExpectedCount, result.SupportedCount)
			}
		}()
	} else if cfg.Camera.Enabled {
		logger.Info("[CAMERA] Camera enabled (auto_start=false — start via web API)")
	} else {
		logger.Info("[CAMERA] Camera streaming disabled in config")
	}

	if !cfg.VPN.Enabled {
		logger.Fatal("[VPN] VPN-only mode requires vpn.enabled=true in config")
	}
	if vpnMgr == nil {
		logger.Fatal("[VPN] VPN manager is not initialized")
	}

	// One reader of the listener: the forwarder. SysID is a flag it sets on the first heartbeat.
	logger.Info("[STARTUP] ✈️  Creating forwarder...")
	fwd, err := forwarder.NewWithVPN(cfg, authClient, listenerNode, vpnMgr)
	if err != nil {
		logger.Fatal("Failed to create forwarder: %v", err)
	}
	if syncManager != nil {
		fwd.SetSyncManager(syncManager)
		defer syncManager.Cleanup()
	}
	fwd.SetAuthClient(authClient)

	logger.Info("[STARTUP] Starting forwarder...")
	if err := fwd.Start(); err != nil {
		logger.Fatal("Failed to start forwarder: %v", err)
	}
	if !cfg.Ethernet.AllowMissingPixhawk {
		go watchPixhawkDeadline(time.Duration(cfg.Ethernet.PixhawkConnectionTimeout) * time.Second)
	}

	// Auth does not block MAVLink. cloud_ready is checked now, then again until the session is up.
	go runAuthWhenReady(authClient, vpnMgr, fwd, cfg.Auth.StartupRetryInterval)
	go superviseUplink(authClient, vpnMgr, fwd)

	landingStop := make(chan struct{})
	camera.StartLandingMavlinkBridge(cfg, fwd, landingStop)
	camera.StartCameraMavlinkBridge(cfg, fwd, landingStop)
	web.SetMavlinkDiagnostics(fwd.GpsDiagnostic)

	if syncManager != nil {
		if err := syncManager.MarkReady(); err != nil {
			logger.Warn("[SYNC] Failed to mark ready: %v", err)
		}
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	logger.Info("MAVLink forwarder running. Press Ctrl+C to stop.")
	<-sigCh

	close(landingStop)

	// Stop camera streamers if running
	web.StopCameraStreamersPublic()

	// Stop forwarder
	fwd.Stop()
	logger.Info("MAVLink forwarder shutdown complete")
}
