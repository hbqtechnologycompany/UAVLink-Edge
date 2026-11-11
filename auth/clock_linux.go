package auth

import "syscall"

// STA_UNSYNC trong timex.status: kernel chưa nhận giờ từ NTP.
const staUnsync = 0x0040

// ClockSynced giống "System clock synchronized" của timedatectl.
// Server kiểm tra timestamp trong AUTH_RESPONSE, nên giờ máy lệch là bị từ chối (error=1).
func ClockSynced() bool {
	var t syscall.Timex
	if _, err := syscall.Adjtimex(&t); err != nil {
		return true
	}
	return t.Status&staUnsync == 0
}
