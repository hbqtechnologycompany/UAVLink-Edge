package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var throttleCache struct {
	sync.Mutex
	at  time.Time
	val int64
	ok  bool
}

func readSysInt(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

func firstGlob(pattern string) string {
	m, _ := filepath.Glob(pattern)
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

func hwmonByName(name string) string {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, "name")); err == nil && strings.TrimSpace(string(b)) == name {
			return d
		}
	}
	return ""
}

func readThrottled() (int64, bool) {
	throttleCache.Lock()
	defer throttleCache.Unlock()
	if time.Since(throttleCache.at) < 5*time.Second {
		return throttleCache.val, throttleCache.ok
	}
	throttleCache.at = time.Now()
	throttleCache.ok = false
	out, err := exec.Command("vcgencmd", "get_throttled").Output()
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(out))
	if i := strings.Index(s, "="); i >= 0 {
		s = s[i+1:]
	}
	v, err := strconv.ParseInt(strings.TrimPrefix(s, "0x"), 16, 64)
	if err != nil {
		return 0, false
	}
	throttleCache.val, throttleCache.ok = v, true
	return v, true
}

func hostThermalStatus() map[string]interface{} {
	out := map[string]interface{}{}
	if v, ok := readSysInt("/sys/class/thermal/thermal_zone0/temp"); ok {
		out["cpu_temp_c"] = float64(v) / 1000
	}
	devs, _ := filepath.Glob("/sys/class/thermal/cooling_device*")
	for _, d := range devs {
		b, err := os.ReadFile(filepath.Join(d, "type"))
		if err != nil || strings.TrimSpace(string(b)) != "pwm-fan" {
			continue
		}
		cur, ok1 := readSysInt(filepath.Join(d, "cur_state"))
		max, ok2 := readSysInt(filepath.Join(d, "max_state"))
		if ok1 && ok2 {
			out["fan_level"] = cur
			out["fan_level_max"] = max
			out["fan_at_max"] = max > 0 && cur >= max
		}
		break
	}
	fan := firstGlob("/sys/devices/platform/cooling_fan/hwmon/hwmon*")
	if fan == "" {
		fan = hwmonByName("pwmfan")
	}
	if fan != "" {
		if v, ok := readSysInt(filepath.Join(fan, "pwm1")); ok {
			out["fan_pwm_pct"] = int(v * 100 / 255)
		}
		if v, ok := readSysInt(filepath.Join(fan, "fan1_input")); ok {
			out["fan_rpm"] = v
		}
	}
	if volt := hwmonByName("rpi_volt"); volt != "" {
		if v, ok := readSysInt(filepath.Join(volt, "in0_lcrit_alarm")); ok {
			out["undervoltage_now"] = v != 0
		}
	}
	if t, ok := readThrottled(); ok {
		out["throttled_raw"] = "0x" + strconv.FormatInt(t, 16)
		out["throttled_now"] = t&0x6 != 0
		out["undervoltage_since_boot"] = t&0x10000 != 0
		out["throttled_since_boot"] = t&0x60000 != 0
		out["temp_limit_since_boot"] = t&0x80000 != 0
	}
	return out
}
