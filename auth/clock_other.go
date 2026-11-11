//go:build !linux

package auth

// ClockSynced: ngoài Linux không đọc được trạng thái NTP — coi như đã đồng bộ.
func ClockSynced() bool { return true }
