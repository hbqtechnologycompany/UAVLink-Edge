package auth

import "time"

// WaitClockSync chờ tới khi đồng hồ đồng bộ NTP hoặc hết timeout; trả về trạng thái cuối.
func WaitClockSync(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !ClockSynced() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
	return true
}
