package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

func registerHostPowerRoutes() {
	http.HandleFunc("/api/host/power", handleHostPower)
}

// handleHostPower runs reboot or poweroff. The password stays in the POST body
// and is written only to sudo stdin, then wiped.
func handleHostPower(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		writePowerMessage(w, "Phương thức không được phép.")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "Yêu cầu không hợp lệ.")
		return
	}
	defer zeroBytes(raw)

	var body struct {
		Action   string `json:"action"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "Yêu cầu không hợp lệ.")
		return
	}
	action := body.Action
	pw := []byte(body.Password)
	body.Password = ""
	defer zeroBytes(pw)

	unit := ""
	switch action {
	case "reboot":
		unit = "reboot.target"
	case "poweroff":
		unit = "poweroff.target"
	default:
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "Hành động không hợp lệ.")
		return
	}
	if len(bytes.TrimSpace(pw)) == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		writePowerMessage(w, "Cần mật khẩu hệ thống.")
		return
	}

	stdin := append(append([]byte{}, pw...), '\n')
	defer zeroBytes(stdin)

	cmd := exec.Command("sudo", "-S", "-k", "-p", "", "systemd-run", "--no-block", "--collect", "systemctl", "start", unit)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	defer zeroBytes(out)
	if err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "sorry") || strings.Contains(text, "password") {
			w.WriteHeader(http.StatusUnauthorized)
			writePowerMessage(w, "Mật khẩu không đúng.")
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		writePowerMessage(w, "Không thực hiện được lệnh nguồn.")
		return
	}

	msg := "CM5 sẽ khởi động lại."
	if action == "poweroff" {
		msg = "CM5 sẽ tắt nguồn."
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": msg})
}

func writePowerMessage(w http.ResponseWriter, msg string) {
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
