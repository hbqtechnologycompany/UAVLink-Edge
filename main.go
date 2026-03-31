package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bluenviron/gomavlib/v3"

	"UAVLink-Edge/auth"
	"UAVLink-Edge/config"
	"UAVLink-Edge/forwarder"
	"UAVLink-Edge/logger"
	"UAVLink-Edge/web"
)

func startAuthWithRetry(authClient *auth.Client) {
	go func() {
		for {
			time.Sleep(10 * time.Second)
			logger.Warn("[AUTH] Retrying authentication in background...")
			if err := authClient.Start(); err != nil {
				logger.Warn("[AUTH] Background authentication retry failed: %v", err)
				continue
			}
			logger.Info("[AUTH] ✅ Background authentication retry succeeded")
			return
		}
	}()
}

func main() {
	// Parse command-line flags
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	logLevel := flag.String("log", "", "Log level: debug, info, warn, error (overrides config)")
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

	logger.Info("UAVLink-Edge configuration loaded successfully (Log level: %s)", logger.GetLevelString())

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

	// Create single auth client instance
	authClient := auth.NewClient(
		cfg.Auth.Host,
		cfg.Auth.Port,
		cfg.Auth.UUID,
		cfg.Auth.SharedSecret,
		cfg.Auth.KeepaliveInterval,
	)

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

	// STEP 1: Wait for Pixhawk connection
	logger.Info("[STARTUP] ⏳ Waiting for Pixhawk heartbeat... (timeout: %ds)", cfg.Ethernet.PixhawkConnectionTimeout)
	pixhawkSysID := uint8(0)
	pixhawkConnected := false
	pixhawkReadyCh := make(chan struct{})

	go func() {
		eventCh := listenerNode.Events()
		timeout := time.NewTimer(time.Duration(cfg.Ethernet.PixhawkConnectionTimeout) * time.Second)
		defer timeout.Stop()

		for {
			select {
			case <-timeout.C:
				pixhawkReadyCh <- struct{}{}
				return
			case event := <-eventCh:
				if frame, ok := event.(*gomavlib.EventFrame); ok {
					sysID := frame.SystemID()
					if sysID == 255 {
						continue
					}
					pixhawkSysID = sysID
					pixhawkConnected = true
					logger.Info("[PIXHAWK_CONNECTED] ✅ First heartbeat received from Pixhawk (SysID: %d)", pixhawkSysID)
					web.HandleHeartbeat(pixhawkSysID)
					pixhawkReadyCh <- struct{}{}
					return
				}
			}
		}
	}()

	<-pixhawkReadyCh

	if !pixhawkConnected {
		if cfg.Ethernet.AllowMissingPixhawk {
			logger.Warn("[STARTUP] ⚠️ Pixhawk connection timeout, continuing in debug mode...")
		} else {
			logger.Fatal("[STARTUP] ❌ Pixhawk connection failed.")
		}
	}

	// STEP 2: Create full forwarder (no VPN)
	logger.Info("[STARTUP] ✈️ Creating forwarder...")
	fwd, err := forwarder.New(cfg, authClient, listenerNode)
	if err != nil {
		logger.Fatal("Failed to create forwarder: %v", err)
	}

	if syncManager != nil {
		fwd.SetSyncManager(syncManager)
		defer syncManager.Cleanup()
	}

	// STEP 3: Authenticate with server
	logger.Info("[STARTUP] ✈️ Authenticating with server via public TCP...")
	if err := authClient.Start(); err != nil {
		logger.Warn("[AUTH] Initial authentication failed: %v", err)
		logger.Warn("[AUTH] Service will continue running and retry in background")
		startAuthWithRetry(authClient)
	} else {
		logger.Info("✅ Successfully authenticated with server")
	}

	// STEP 4: Start forwarder
	logger.Info("[STARTUP] Starting forwarder...")
	if err := fwd.Start(); err != nil {
		logger.Fatal("Failed to start forwarder: %v", err)
	}
	fwd.SetAuthClient(authClient)

	// Start web server
	web.StartServer(cfg.Web.Port, authClient, cfg.Auth.UUID)

	if syncManager != nil {
		if err := syncManager.MarkReady(); err != nil {
			logger.Warn("[SYNC] Failed to mark ready: %v", err)
		}
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	logger.Info("UAVLink-Edge running. Press Ctrl+C to stop.")
	<-sigCh

	fwd.Stop()
	logger.Info("UAVLink-Edge shutdown complete")
}
