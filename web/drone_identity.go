package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"DroneBridge/auth"
	"DroneBridge/config"
	"DroneBridge/redact"
)

var droneUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// reregisterState là nội dung data/reregister.json do install/service/reregister.sh ghi.
type reregisterState struct {
	State      string `json:"state"` // running | ok | failed
	UUID       string `json:"uuid"`
	OldUUID    string `json:"old_uuid"`
	Message    string `json:"message"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Log        string `json:"log,omitempty"`
}

func reregisterStatePath() string { return projectPath("data", "reregister.json") }

func readReregisterState() *reregisterState {
	data, err := os.ReadFile(reregisterStatePath())
	if err != nil {
		return nil
	}
	var st reregisterState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

// masked trả bản sao an toàn để gửi ra dashboard: UUID bị che, log chỉ giữ dòng lỗi.
func (st reregisterState) masked() reregisterState {
	st.UUID = redact.UUID(st.UUID)
	st.OldUUID = redact.UUID(st.OldUUID)
	st.Message = redact.UUIDs(st.Message)
	var keep []string
	for _, line := range strings.Split(st.Log, "\n") {
		if strings.Contains(line, "FATAL") || strings.Contains(line, "ERROR") ||
			strings.Contains(line, "failed") || strings.Contains(line, "❌") {
			keep = append(keep, redact.UUIDs(line))
		}
	}
	st.Log = strings.Join(keep, "\n")
	return st
}

// registerDroneIdentityRoutes: trạng thái đăng ký (UUID đã che), đăng ký lại, xem UUID/API key
// đầy đủ bằng mật khẩu hệ thống. runningUUID là UUID process đọc lúc khởi động.
func registerDroneIdentityRoutes(authClient *auth.Client, runningUUID string) {
	http.HandleFunc("/api/drone/identity", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		configUUID := ""
		sentType := uint8(0)
		if cfg, err := loadAppConfig(); err == nil {
			configUUID = cfg.Auth.UUID
			sentType = cfg.Auth.VehicleType
		}
		secretUUID, _, secretErr := auth.LoadSecret()
		out := map[string]interface{}{
			"uuid_masked":    redact.UUID(configUUID),
			"has_uuid":       configUUID != "",
			"has_secret":     secretErr == nil,
			"registered":     secretErr == nil && secretUUID == configUUID,
			"restart_needed": configUUID != runningUUID,
			"authenticated":  authClient != nil && authClient.IsAuthenticated(),
		}
		if secretErr == nil && secretUUID != configUUID {
			out["secret_uuid_masked"] = redact.UUID(secretUUID)
		}
		if st := readReregisterState(); st != nil {
			out["reregister"] = st.masked()
		}
		out["vehicle"] = vehicleProfile(sentType)
		json.NewEncoder(w).Encode(out)
	})
	http.HandleFunc("/api/drone/reregister", handleReregister)
	http.HandleFunc("/api/drone/reveal", func(w http.ResponseWriter, r *http.Request) {
		handleReveal(w, r, authClient)
	})
}

// vehicleProfile so loại máy PX4 đang báo với hồ sơ server giữ từ lần đăng ký gần nhất
// (auth.vehicle_type; reregister.sh ghi giá trị đã gửi và khôi phục nếu đăng ký lỗi).
func vehicleProfile(sentType uint8) map[string]interface{} {
	registered := auth.ServerVehicleType(sentType)
	_, regLabel := vehicleKind(registered)
	out := map[string]interface{}{
		"registered_type":  registered,
		"registered_label": regLabel,
		"switch_needed":    false,
	}
	if live, ok := LivePX4VehicleType(); ok {
		_, liveLabel := vehicleKind(live)
		out["live_type"] = live
		out["live_label"] = liveLabel
		out["live_supported"] = auth.SupportedVehicleType(live)
		out["switch_needed"] = auth.SupportedVehicleType(live) && live != registered
	}
	return out
}

type passwordBody struct {
	UUID        string `json:"uuid"`
	VehicleType *int   `json:"vehicle_type"`
	What        string `json:"what"`
	Password    string `json:"password"`
}

// readPasswordBody đọc body JSON nhỏ; mật khẩu trả riêng dạng []byte để xóa sau khi dùng.
func readPasswordBody(w http.ResponseWriter, r *http.Request) (passwordBody, []byte, bool) {
	var body passwordBody
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	zeroBytes(raw)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "Yêu cầu không hợp lệ.")
		return body, nil, false
	}
	pw := []byte(body.Password)
	body.Password = ""
	return body, pw, true
}

// handleReveal trả UUID hoặc API key đầy đủ khi mật khẩu hệ thống đúng.
func handleReveal(w http.ResponseWriter, r *http.Request, authClient *auth.Client) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		writePowerMessage(w, "Phương thức không được phép.")
		return
	}
	body, pw, ok := readPasswordBody(w, r)
	if !ok {
		return
	}
	defer zeroBytes(pw)
	if code, msg := verifyHostPassword(r, pw); code != http.StatusOK {
		w.WriteHeader(code)
		writePowerMessage(w, msg)
		return
	}
	switch body.What {
	case "uuid":
		cfg, err := loadAppConfig()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writePowerMessage(w, "Không đọc được config.")
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"value": cfg.Auth.UUID})
	case "api_key":
		if authClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			writePowerMessage(w, "Auth client chưa sẵn sàng.")
			return
		}
		st, err := authClient.GetAPIKeyStatus()
		if err != nil || st == nil || st.HasActiveKey != 0x01 {
			w.WriteHeader(http.StatusNotFound)
			writePowerMessage(w, "Không có API key đang hoạt động.")
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"value": st.APIKey})
	default:
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "Chỉ xem được uuid hoặc api_key.")
	}
}

// handleReregister đổi auth.uuid (nếu có UUID mới) rồi đăng ký lại với server.
// Chạy reregister.sh qua systemd-run: script dừng dronebridge (chính process này) nên phải
// nằm ngoài cgroup của service. Mật khẩu chỉ đi vào stdin của sudo rồi bị xóa.
func handleReregister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		writePowerMessage(w, "Phương thức không được phép.")
		return
	}
	body, pw, ok := readPasswordBody(w, r)
	if !ok {
		return
	}
	defer zeroBytes(pw)

	// Rỗng = đăng ký lại với UUID hiện có trong config (người dùng không cần biết UUID đầy đủ).
	uuid := strings.ToLower(strings.TrimSpace(body.UUID))
	if uuid == "" {
		if cfg, err := loadAppConfig(); err == nil {
			uuid = strings.ToLower(strings.TrimSpace(cfg.Auth.UUID))
		}
	}
	if !droneUUIDPattern.MatchString(uuid) {
		w.WriteHeader(http.StatusBadRequest)
		writePowerMessage(w, "UUID không hợp lệ (dạng xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx, chữ số hex).")
		return
	}
	// Không chọn loại máy = lấy HEARTBEAT.type PX4 đang báo; mất heartbeat thì giữ auth.vehicle_type.
	vehicleType := -1
	if body.VehicleType != nil {
		vehicleType = *body.VehicleType
		if vehicleType != 0 && (vehicleType < 0 || vehicleType > 255 || !auth.SupportedVehicleType(uint8(vehicleType))) {
			w.WriteHeader(http.StatusBadRequest)
			writePowerMessage(w, "Loại phương tiện không được server hỗ trợ.")
			return
		}
	} else if live, ok := LivePX4VehicleType(); ok && auth.SupportedVehicleType(live) {
		vehicleType = int(live)
	} else if cfg, err := loadAppConfig(); err == nil {
		vehicleType = int(cfg.Auth.VehicleType)
	}
	if vehicleType < 0 {
		vehicleType = 0
	}
	if len(bytes.TrimSpace(pw)) == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		writePowerMessage(w, "Cần mật khẩu hệ thống.")
		return
	}
	if msg := passwordLocked(r); msg != "" {
		w.WriteHeader(http.StatusTooManyRequests)
		writePowerMessage(w, msg)
		return
	}
	if st := readReregisterState(); st != nil && st.State == "running" {
		if t, err := time.Parse(time.RFC3339, st.StartedAt); err == nil && time.Since(t) < 3*time.Minute {
			w.WriteHeader(http.StatusConflict)
			writePowerMessage(w, "Đang đăng ký lại, chờ xong rồi thử lại.")
			return
		}
	}

	script := projectPath("install", "service", "reregister.sh")
	if _, err := os.Stat(script); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writePowerMessage(w, "Thiếu install/service/reregister.sh — chạy sudo bash install/install.sh --check.")
		return
	}

	stdin := append(append([]byte{}, pw...), '\n')
	defer zeroBytes(stdin)
	cmd := exec.Command("sudo", "-S", "-k", "-p", "", "--",
		"/usr/bin/systemd-run", "--system", "--unit=dronebridge-reregister", "--collect", "--no-block", "--quiet",
		"/bin/bash", script, uuid, strconv.Itoa(vehicleType))
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = sudoEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	defer zeroBytes(out)
	if err != nil {
		switch {
		case sudoWrongPassword(out):
			recordPassword(r, false)
			w.WriteHeader(http.StatusUnauthorized)
			writePowerMessage(w, "Mật khẩu không đúng.")
		case strings.Contains(strings.ToLower(string(out)), "already"):
			w.WriteHeader(http.StatusConflict)
			writePowerMessage(w, "Đang có một lần đăng ký lại chạy, chờ xong rồi thử lại.")
		default:
			w.WriteHeader(http.StatusBadGateway)
			writePowerMessage(w, "Không chạy được đăng ký lại (xem journalctl -u dronebridge-reregister).")
		}
		return
	}
	recordPassword(r, true)
	_, typeLabel := vehicleKind(auth.ServerVehicleType(uint8(vehicleType)))
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"uuid":         redact.UUID(uuid),
		"vehicle_type": vehicleType,
		"message": "Đang đăng ký lại với UUID " + redact.UUID(uuid) + ", loại " + typeLabel +
			". DroneBridge sẽ khởi động lại trong ít giây.",
	})
}

var cfgVehicleCache struct {
	sync.Mutex
	mod   time.Time
	value uint8
}

// cfgVehicleType đọc auth.vehicle_type, chỉ parse lại config khi file đổi (/api/status gọi liên tục).
func cfgVehicleType() uint8 {
	path := resolveAppConfigPath()
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	cfgVehicleCache.Lock()
	defer cfgVehicleCache.Unlock()
	if !fi.ModTime().Equal(cfgVehicleCache.mod) {
		if cfg, err := config.Load(path); err == nil {
			cfgVehicleCache.value = cfg.Auth.VehicleType
			cfgVehicleCache.mod = fi.ModTime()
		}
	}
	return cfgVehicleCache.value
}
