package logger

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level represents logging level
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

var levelFromString = map[string]Level{
	"debug": DEBUG,
	"info":  INFO,
	"warn":  WARN,
	"error": ERROR,
}

// Logger is a leveled logger
type Logger struct {
	mu          sync.RWMutex
	level       Level
	logger      *log.Logger
	useUnixTime bool

	onlyDebugSections bool
	showStartupInfo   bool
	showAuthInfo      bool
	showPktStats      bool

	// Scope filters requested from config.
	showServerConnectionOnly bool
	showWebInteractionLogs   bool
}

var stdLogBridgeOnce sync.Once

var defaultLogger = &Logger{
	level:                    INFO,
	logger:                   log.New(os.Stdout, "", log.LstdFlags),
	useUnixTime:              false,
	onlyDebugSections:        false,
	showStartupInfo:          true,
	showAuthInfo:             true,
	showPktStats:             true,
	showServerConnectionOnly: false,
	showWebInteractionLogs:   true,
}

// ConfigureOutputFilters controls category filtering for runtime logs.
func ConfigureOutputFilters(onlyDebugSections, showStartupInfo, showAuthInfo, showPktStats bool) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.onlyDebugSections = onlyDebugSections
	defaultLogger.showStartupInfo = showStartupInfo
	defaultLogger.showAuthInfo = showAuthInfo
	defaultLogger.showPktStats = showPktStats
}

// ConfigureScopeFilters controls high-level log scopes from config.
// serverConnectionOnly=true keeps only server-connection related logs.
// showWebInteractionLogs=true allows web interaction logs on local web port.
func ConfigureScopeFilters(serverConnectionOnly, showWebInteractionLogs bool) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.showServerConnectionOnly = serverConnectionOnly
	defaultLogger.showWebInteractionLogs = showWebInteractionLogs
}

type stdLogFilterWriter struct{}

func (w *stdLogFilterWriter) Write(p []byte) (int, error) {
	msg := string(p)
	if shouldEmitStdMessage(msg) {
		_, err := os.Stdout.Write(p)
		if err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// InstallStdLogBridge routes standard library log.Printf output through logger filters.
// This is required because auth/web packages use the global stdlib logger.
func InstallStdLogBridge() {
	stdLogBridgeOnce.Do(func() {
		log.SetOutput(&stdLogFilterWriter{})
	})
}

func shouldEmitStdMessage(msg string) bool {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()

	if defaultLogger.showServerConnectionOnly {
		return isServerConnectionMessage(msg)
	}

	if !defaultLogger.showWebInteractionLogs && isWebInteractionMessage(msg) {
		return false
	}

	return true
}

func isServerConnectionMessage(msg string) bool {
	// In server-only mode, hide local listener logs on UDP :14540.
	if strings.Contains(msg, ":14540") || strings.Contains(msg, "port 14540") {
		return false
	}

	serverMarkers := []string{
		"[AUTH]",
		"[REGISTER]",
		"[SESSION]",
		"[SESSION_REFRESH]",
		"[SESSION_RECOVERY]",
		"[REFRESH]",
		"[RECONNECT]",
		"[REAUTH]",
		"[KEEPALIVE]",
		"[API_KEY]",
		"[IP_CHANGE]",
		"[VPN]",
		"[VPN_DEBUG]",
		"[SENDER]",
		"[PKT_",
		"[MSGID]",
		"[BANDWIDTH]",
		"[SERVER->PIXHAWK]",
		"[FORWARD]",
		"[RX]",
		"UDP client to",
		"Connecting to",
		"Registration",
		"authentication",
		"session",
	}
	for _, marker := range serverMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isWebInteractionMessage(msg string) bool {
	if strings.Contains(msg, "[WEB]") {
		return true
	}
	if strings.Contains(msg, "[CAMERA]") {
		return true
	}
	if strings.Contains(msg, "Starting web server on http://") {
		return true
	}
	if strings.Contains(msg, ":8080") {
		return true
	}
	return false
}

// SetLevel sets the global log level
func SetLevel(level Level) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = level
}

// SetLevelFromString sets log level from string (debug, info, warn, error)
func SetLevelFromString(levelStr string) {
	if level, ok := levelFromString[strings.ToLower(levelStr)]; ok {
		SetLevel(level)
		defaultLogger.logger.Printf("[LOGGER] Log level set to %s", levelNames[level])
	}
}

// SetTimestampFormat sets timestamp format ("time" or "unix")
func SetTimestampFormat(format string) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	if strings.ToLower(format) == "unix" {
		defaultLogger.useUnixTime = true
		defaultLogger.logger.SetFlags(0) // Remove default timestamp
		defaultLogger.logger.Printf("[%d] [LOGGER] Timestamp format set to Unix", time.Now().Unix())
	} else {
		defaultLogger.useUnixTime = false
		defaultLogger.logger.SetFlags(log.LstdFlags)
		defaultLogger.logger.Printf("[LOGGER] Timestamp format set to Time")
	}
}

// GetLevel returns current log level
func GetLevel() Level {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()
	return defaultLogger.level
}

// GetLevelString returns current log level as string
func GetLevelString() string {
	return levelNames[GetLevel()]
}

func shouldLog(level Level) bool {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()
	return level >= defaultLogger.level
}

func shouldEmitMessage(msg string, level Level) bool {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()

	if defaultLogger.onlyDebugSections {
		if strings.Contains(msg, "[DEBUG_CFG]") {
			return true
		}
		// Keep severe errors visible even in strict mode.
		if level >= ERROR {
			return true
		}
		return false
	}

	if !defaultLogger.showStartupInfo && strings.Contains(msg, "[STARTUP]") {
		return false
	}

	if !defaultLogger.showAuthInfo {
		if strings.Contains(msg, "[AUTH]") ||
			strings.Contains(msg, "[SESSION]") ||
			strings.Contains(msg, "[REFRESH]") ||
			strings.Contains(msg, "[RECONNECT]") ||
			strings.Contains(msg, "[KEEPALIVE]") {
			return false
		}
	}

	if !defaultLogger.showPktStats {
		if strings.Contains(msg, "[PKT_") ||
			strings.Contains(msg, "[MSGID]") ||
			strings.Contains(msg, "[BANDWIDTH]") {
			return false
		}
	}

	if defaultLogger.showServerConnectionOnly {
		if isServerConnectionMessage(msg) {
			return true
		}
		if level >= ERROR {
			return true
		}
		return false
	}

	if !defaultLogger.showWebInteractionLogs && isWebInteractionMessage(msg) {
		return false
	}

	return true
}

func allowDebugInServerOnlyMode() bool {
	defaultLogger.mu.RLock()
	defer defaultLogger.mu.RUnlock()
	return defaultLogger.showServerConnectionOnly
}

// formatMessage adds timestamp prefix if using Unix time
func formatMessage(prefix, format string, v ...interface{}) string {
	defaultLogger.mu.RLock()
	useUnix := defaultLogger.useUnixTime
	defaultLogger.mu.RUnlock()

	if useUnix {
		return fmt.Sprintf("[%d] %s%s", time.Now().Unix(), prefix, fmt.Sprintf(format, v...))
	}
	return fmt.Sprintf("%s%s", prefix, fmt.Sprintf(format, v...))
}

// Debug logs at DEBUG level
func Debug(format string, v ...interface{}) {
	if shouldLog(DEBUG) || allowDebugInServerOnlyMode() {
		msg := formatMessage("[DEBUG] ", format, v...)
		if shouldEmitMessage(msg, DEBUG) {
			defaultLogger.logger.Print(msg)
		}
	}
}

// Info logs at INFO level
func Info(format string, v ...interface{}) {
	if shouldLog(INFO) {
		msg := formatMessage("[INFO] ", format, v...)
		if shouldEmitMessage(msg, INFO) {
			defaultLogger.logger.Print(msg)
		}
	}
}

// Warn logs at WARN level
func Warn(format string, v ...interface{}) {
	if shouldLog(WARN) {
		msg := formatMessage("[WARN] ", format, v...)
		if shouldEmitMessage(msg, WARN) {
			defaultLogger.logger.Print(msg)
		}
	}
}

// Error logs at ERROR level
func Error(format string, v ...interface{}) {
	if shouldLog(ERROR) {
		msg := formatMessage("[ERROR] ", format, v...)
		if shouldEmitMessage(msg, ERROR) {
			defaultLogger.logger.Print(msg)
		}
	}
}

// Debugf is alias for Debug
func Debugf(format string, v ...interface{}) {
	Debug(format, v...)
}

// Infof is alias for Info
func Infof(format string, v ...interface{}) {
	Info(format, v...)
}

// Warnf is alias for Warn
func Warnf(format string, v ...interface{}) {
	Warn(format, v...)
}

// Errorf is alias for Error
func Errorf(format string, v ...interface{}) {
	Error(format, v...)
}

// Fatal logs at ERROR level and exits
func Fatal(format string, v ...interface{}) {
	defaultLogger.logger.Print(formatMessage("[FATAL] ", format, v...))
	os.Exit(1)
}

// Fatalf is alias for Fatal
func Fatalf(format string, v ...interface{}) {
	Fatal(format, v...)
}

// Printf always logs (for backward compatibility)
func Printf(format string, v ...interface{}) {
	defaultLogger.logger.Printf(format, v...)
}

// Println always logs (for backward compatibility)
func Println(v ...interface{}) {
	defaultLogger.logger.Println(v...)
}

// String returns formatted string without logging
func String(format string, v ...interface{}) string {
	return fmt.Sprintf(format, v...)
}
