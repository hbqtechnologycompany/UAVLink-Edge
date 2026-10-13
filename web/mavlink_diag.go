package web

var mavlinkDiagnostics func() map[string]interface{}

// SetMavlinkDiagnostics registers a provider for /api/status mavlink_diagnostics.
func SetMavlinkDiagnostics(fn func() map[string]interface{}) {
	mavlinkDiagnostics = fn
}

func getMavlinkDiagnostics() map[string]interface{} {
	if mavlinkDiagnostics == nil {
		return nil
	}
	return mavlinkDiagnostics()
}
