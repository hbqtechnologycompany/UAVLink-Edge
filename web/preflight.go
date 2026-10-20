package web

import (
	"strings"
	"sync"
	"time"

	"DroneBridge/camera"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

const (
	sensorGyro       uint32 = 1
	sensorAccel      uint32 = 2
	sensorMag        uint32 = 4
	sensorBaro       uint32 = 8
	sensorGPS        uint32 = 32
	sensorMotors     uint32 = 32768
	sensorRC         uint32 = 65536
	sensorBattery    uint32 = 33554432
	sensorPrearm     uint32 = 268435456
	sensorPropulsion uint32 = 1073741824

	modeArmed uint8 = 128

	cmdNavTakeoff uint16 = 22
	cmdArmDisarm  uint16 = 400
)

type preflightNote struct {
	At       time.Time
	Severity uint8
	Text     string
}

type preflightAck struct {
	At      time.Time
	Command uint16
	Result  uint8
}

type escSlot struct {
	Seen    bool
	Rpm     int32
	Voltage float32
	Current float32
}

type preflightSnapshot struct {
	hbAt                     time.Time
	vehType                  uint8
	baseMode                 uint8
	sysState                 uint8
	mode                     string
	sysAt                    time.Time
	present, enabled, health uint32
	volts                    float64
	battPct                  int
	ekfAt                    time.Time
}

type preflightState struct {
	mu sync.Mutex

	hbAt     time.Time
	vehType  uint8
	baseMode uint8
	sysState uint8
	mode     string

	sysAt                    time.Time
	present, enabled, health uint32
	volts                    float64
	battPct                  int

	ekfAt time.Time

	notes []preflightNote
	acks  []preflightAck

	escAt time.Time
	esc   [8]escSlot
}

var preflight preflightState

func HandlePreflightStatustext(msg *common.MessageStatustext) {
	if msg == nil {
		return
	}
	text := strings.Trim(strings.TrimSpace(msg.Text), "\x00")
	if text == "" {
		return
	}
	preflight.mu.Lock()
	defer preflight.mu.Unlock()
	preflight.notes = append([]preflightNote{{
		At: time.Now(), Severity: uint8(msg.Severity), Text: text,
	}}, preflight.notes...)
	if len(preflight.notes) > 12 {
		preflight.notes = preflight.notes[:12]
	}
}

func HandlePreflightAck(msg *common.MessageCommandAck) {
	if msg == nil {
		return
	}
	preflight.mu.Lock()
	defer preflight.mu.Unlock()
	preflight.acks = append([]preflightAck{{
		At: time.Now(), Command: uint16(msg.Command), Result: uint8(msg.Result),
	}}, preflight.acks...)
	if len(preflight.acks) > 8 {
		preflight.acks = preflight.acks[:8]
	}
}

func HandlePreflightEsc(msg *common.MessageEscStatus) {
	if msg == nil {
		return
	}
	preflight.mu.Lock()
	defer preflight.mu.Unlock()
	base := int(msg.Index)
	if base < 0 || base > 7 {
		return
	}
	preflight.escAt = time.Now()
	for i := 0; i < 4 && base+i < 8; i++ {
		preflight.esc[base+i] = escSlot{
			Seen: true, Rpm: msg.Rpm[i], Voltage: msg.Voltage[i], Current: msg.Current[i],
		}
	}
}

func notePreflightHeartbeat(msg *common.MessageHeartbeat, mode string) {
	if msg == nil {
		return
	}
	preflight.mu.Lock()
	defer preflight.mu.Unlock()
	preflight.hbAt = time.Now()
	preflight.vehType = uint8(msg.Type)
	preflight.baseMode = uint8(msg.BaseMode)
	preflight.sysState = uint8(msg.SystemStatus)
	preflight.mode = mode
}

func notePreflightSys(msg *common.MessageSysStatus) {
	if msg == nil {
		return
	}
	preflight.mu.Lock()
	defer preflight.mu.Unlock()
	preflight.sysAt = time.Now()
	preflight.present = uint32(msg.OnboardControlSensorsPresent)
	preflight.enabled = uint32(msg.OnboardControlSensorsEnabled)
	preflight.health = uint32(msg.OnboardControlSensorsHealth)
	if msg.VoltageBattery != 0 && msg.VoltageBattery != 65535 {
		preflight.volts = float64(msg.VoltageBattery) / 1000.0
	}
	if msg.BatteryRemaining >= 0 && msg.BatteryRemaining <= 100 {
		preflight.battPct = int(msg.BatteryRemaining)
	}
}

func notePreflightEkf() {
	preflight.mu.Lock()
	preflight.ekfAt = time.Now()
	preflight.mu.Unlock()
}

type checkItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type partItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

func BuildPreflightReport() map[string]interface{} {
	preflight.mu.Lock()
	st := preflightSnapshot{
		hbAt: preflight.hbAt, vehType: preflight.vehType, baseMode: preflight.baseMode,
		sysState: preflight.sysState, mode: preflight.mode,
		sysAt: preflight.sysAt, present: preflight.present, enabled: preflight.enabled, health: preflight.health,
		volts: preflight.volts, battPct: preflight.battPct, ekfAt: preflight.ekfAt,
	}
	notes := append([]preflightNote(nil), preflight.notes...)
	acks := append([]preflightAck(nil), preflight.acks...)
	var esc [8]escSlot
	copy(esc[:], preflight.esc[:])
	escAt := preflight.escAt
	preflight.mu.Unlock()

	telem := GetTelemetrySnapshot()
	now := time.Now()
	linked := !st.hbAt.IsZero() && now.Sub(st.hbAt) <= 3*time.Second
	sysFresh := !st.sysAt.IsZero() && now.Sub(st.sysAt) <= 5*time.Second
	ekfFresh := !st.ekfAt.IsZero() && now.Sub(st.ekfAt) <= 5*time.Second
	escFresh := !escAt.IsZero() && now.Sub(escAt) <= 3*time.Second
	armed := st.baseMode&modeArmed != 0

	cfgType := uint8(0)
	if cfg, err := loadAppConfig(); err == nil {
		cfgType = cfg.Auth.VehicleType
	}
	liveKind, liveLabel := vehicleKind(st.vehType)
	cfgKind, cfgLabel := vehicleKind(cfgType)
	if cfgType == 0 {
		cfgLabel = "Chưa khai báo trong config"
	}

	checks := make([]checkItem, 0, 12)
	checks = append(checks, checkItem{
		ID: "link", Label: "Liên lạc PX4",
		State:  tern(linked, "ok", "bad"),
		Detail: tern(linked, "Pixhawk đang trả heartbeat.", "Không thấy heartbeat. Chưa đọc được loại máy hay điều kiện bay."),
	})

	typeDetail := liveLabel
	if linked && cfgType != 0 && cfgType != st.vehType {
		typeDetail = liveLabel + ". Config đang ghi " + cfgLabel + " — hai bên không khớp."
	} else if linked && cfgType == 0 {
		typeDetail = liveLabel + ". Config chưa khai báo loại máy; đang dùng loại PX4 báo."
	} else if !linked {
		typeDetail = "Chưa có heartbeat để biết đây là máy bay, xe hay tàu."
	}
	typeState := "unknown"
	if linked {
		typeState = "ok"
		if cfgType != 0 && cfgType != st.vehType {
			typeState = "warn"
		}
	}
	checks = append(checks, checkItem{ID: "vehicle", Label: "Loại phương tiện", State: typeState, Detail: typeDetail})

	if sysFresh && bit(st.present, sensorPrearm) {
		ok := bit(st.health, sensorPrearm)
		checks = append(checks, checkItem{
			ID: "prearm", Label: "Điều kiện mở khóa",
			State: tern(ok, "ok", "bad"),
			Detail: tern(ok,
				"PX4 báo đã qua kiểm tra trước khi mở khóa.",
				"PX4 chưa cho mở khóa. Xem các mục đỏ và dòng PX4 phía dưới."),
		})
	}

	gpsState, gpsDetail := gpsCheck(telem)
	checks = append(checks, checkItem{ID: "gps", Label: "GPS", State: gpsState, Detail: gpsDetail})
	checks = append(checks, checkItem{
		ID: "ekf", Label: "Vị trí toàn cục (EKF)",
		State: tern(ekfFresh, "ok", "bad"),
		Detail: tern(ekfFresh,
			"Đang có GLOBAL_POSITION_INT. EKF đã xuất vị trí.",
			"Chưa có vị trí EKF. Cất cánh sẽ bị từ chối dù GPS thô vẫn có số."),
	})

	if sysFresh {
		checks = append(checks,
			sensorCheck("gyro", "Con quay", st, sensorGyro, "Con quay đang khỏe.", "Con quay lỗi hoặc chưa hiệu chuẩn."),
			sensorCheck("accel", "Gia tốc", st, sensorAccel, "Gia tốc kế đang khỏe.", "Gia tốc kế lệch hoặc chưa hiệu chuẩn."),
			sensorCheck("mag", "La bàn", st, sensorMag, "La bàn đang khỏe.", "La bàn lỗi hoặc bị nhiễu."),
			sensorCheck("baro", "Áp suất", st, sensorBaro, "Cảm biến áp suất đang khỏe.", "Áp suất kế lỗi — độ cao baro không tin được."),
		)
		if bit(st.present, sensorRC) {
			checks = append(checks, sensorCheck("rc", "Tay điều khiển", st, sensorRC, "Đã thấy tín hiệu RC.", "Có RC nhưng tín hiệu lỗi."))
		}
	}

	battState, battDetail := batteryCheck(st, sysFresh)
	checks = append(checks, checkItem{ID: "battery", Label: "Pin", State: battState, Detail: battDetail})

	motorState, motorDetail := propulsionCheck(st, sysFresh, escFresh, esc, armed)
	checks = append(checks, checkItem{ID: "motors", Label: "Động cơ", State: motorState, Detail: motorDetail})
	kept := checks[:0]
	for _, c := range checks {
		if c.State == "skip" {
			continue
		}
		kept = append(kept, c)
	}
	checks = kept

	parts := buildParts(liveKind, checks, escFresh, esc, armed, telem)
	verdict, verdictDetail := verdictFor(linked, armed, st.mode, checks, acks)
	return map[string]interface{}{
		"connected": linked,
		"armed":     armed,
		"mode":      st.mode,
		"vehicle": map[string]interface{}{
			"type":             st.vehType,
			"kind":             liveKind,
			"label":            liveLabel,
			"configured_type":  cfgType,
			"configured_kind":  cfgKind,
			"configured_label": cfgLabel,
			"same_as_config":   cfgType == 0 || cfgType == st.vehType,
		},
		"verdict": map[string]string{
			"code":   verdict,
			"title":  verdictTitle(verdict),
			"detail": verdictDetail,
		},
		"checks": checks,
		"parts":  parts,
		"acks":   decodeAcks(acks),
		"notes":  decodeNotes(notes),
	}
}

func vehicleKind(t uint8) (kind, label string) {
	switch t {
	case 1:
		return "plane", "Máy bay cánh cố định"
	case 2:
		return "quad", "Máy bay 4 cánh quạt"
	case 3:
		return "quad", "Máy bay đồng trục"
	case 4:
		return "heli", "Trực thăng"
	case 10:
		return "rover", "Xe mặt đất"
	case 11:
		return "boat", "Tàu mặt nước"
	case 13:
		return "hex", "Máy bay 6 cánh quạt"
	case 14:
		return "octo", "Máy bay 8 cánh quạt"
	case 15:
		return "tri", "Máy bay 3 cánh quạt"
	case 0:
		return "unknown", "Chưa xác định"
	default:
		return "unknown", "Loại khác"
	}
}

func sensorCheck(id, label string, st preflightSnapshot, bitv uint32, okText, badText string) checkItem {
	if !bit(st.present, bitv) {
		return checkItem{ID: id, Label: label, State: "skip", Detail: "PX4 không khai báo cảm biến này."}
	}
	if !bit(st.enabled, bitv) {
		return checkItem{ID: id, Label: label, State: "warn", Detail: "Có lắp nhưng đang tắt."}
	}
	if !bit(st.health, bitv) {
		return checkItem{ID: id, Label: label, State: "bad", Detail: badText}
	}
	return checkItem{ID: id, Label: label, State: "ok", Detail: okText}
}

func gpsCheck(t TelemetrySnapshot) (string, string) {
	if !t.Valid || !t.Connected {
		return "unknown", "Chưa có bản tin GPS."
	}
	if t.GpsFix >= 3 && t.Satellites >= 6 {
		return "ok", t.GpsLock + ". Đủ để xét cất cánh."
	}
	if t.GpsFix >= 3 && t.Satellites > 0 {
		return "warn", t.GpsLock + ". Có fix nhưng còn ít vệ tinh."
	}
	if t.GpsFix > 0 {
		return "bad", "GPS chưa khóa 3D. Chưa nên cất cánh."
	}
	return "bad", "Không có fix GPS."
}

func batteryCheck(st preflightSnapshot, fresh bool) (string, string) {
	if !fresh || (st.volts == 0 && st.battPct == 0) {
		return "unknown", "Chưa đọc được pin."
	}
	detail := ""
	if st.battPct > 0 {
		detail = sprintfInt(st.battPct) + "%"
	}
	if st.volts > 0 {
		if detail != "" {
			detail += " · "
		}
		detail += sprintfVolt(st.volts)
	}
	if bit(st.present, sensorBattery) && !bit(st.health, sensorBattery) {
		return "bad", detail + " — PX4 báo pin không khỏe."
	}
	if st.battPct > 0 && st.battPct < 20 {
		return "bad", detail + " — pin thấp, chưa nên cất cánh."
	}
	if st.battPct > 0 && st.battPct < 35 {
		return "warn", detail + " — pin trung bình."
	}
	return "ok", detail
}

func propulsionCheck(st preflightSnapshot, sysFresh, escFresh bool, esc [8]escSlot, armed bool) (string, string) {
	if escFresh {
		bad := 0
		seen := 0
		for _, s := range esc {
			if !s.Seen {
				continue
			}
			seen++
			if armed && s.Voltage > 1 && s.Rpm == 0 {
				bad++
			}
			if s.Voltage == 0 && neighborHasVoltage(esc) {
				bad++
			}
		}
		if seen == 0 {
			return "unknown", "Có bản tin ESC nhưng chưa có kênh nào."
		}
		if bad > 0 {
			return "bad", "Có động cơ không báo điện hoặc không quay trong khi động cơ khác vẫn sống."
		}
		if !armed {
			return "ok", "ESC đang báo. Động cơ chưa quay vì máy chưa mở khóa — đó là bình thường."
		}
		return "ok", "Các ESC đang báo đều."
	}
	if !sysFresh {
		return "unknown", "Chưa có SYS_STATUS để biết lối ra động cơ."
	}
	if bit(st.present, sensorPropulsion) && !bit(st.health, sensorPropulsion) {
		return "bad", "PX4 báo cụm động lực lỗi. Chưa có dữ liệu từng ESC để chỉ đúng một máy."
	}
	if bit(st.present, sensorMotors) && bit(st.enabled, sensorMotors) && !bit(st.health, sensorMotors) {
		return "bad", "PX4 báo lối ra động cơ lỗi."
	}
	if bit(st.present, sensorMotors) && bit(st.health, sensorMotors) {
		return "ok", "Lối ra động cơ khỏe. PX4 chưa gửi ESC_STATUS nên chưa tô từng động cơ."
	}
	return "unknown", "Chưa đủ dữ liệu động cơ."
}

func neighborHasVoltage(esc [8]escSlot) bool {
	for _, s := range esc {
		if s.Seen && s.Voltage > 1 {
			return true
		}
	}
	return false
}

func buildParts(kind string, checks []checkItem, escFresh bool, esc [8]escSlot, armed bool, telem TelemetrySnapshot) []partItem {
	byID := map[string]checkItem{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	n := motorCount(kind)
	labels := motorLabels(kind, n)
	parts := make([]partItem, 0, n+4)
	shared := byID["motors"]
	for i := 0; i < n; i++ {
		state, detail := shared.State, shared.Detail
		if escFresh && esc[i].Seen {
			state, detail = oneMotor(esc[i], esc, armed)
		} else if state == "ok" && !escFresh {
			state = "idle"
			detail = "Chưa có số liệu riêng. Màu này là trạng thái chung của cụm động cơ."
		}
		if state == "" {
			state = "unknown"
			detail = "Chưa có dữ liệu."
		}
		parts = append(parts, partItem{ID: motorID(i), Label: labels[i], State: state, Detail: detail})
	}
	bodyState := worstState(byID["gyro"].State, byID["accel"].State, byID["baro"].State, byID["link"].State)
	parts = append(parts, partItem{ID: "body", Label: "Thân máy", State: bodyState, Detail: "Gộp con quay, gia tốc và áp suất."})
	parts = append(parts, partItem{ID: "gps", Label: "GPS", State: orState(byID["gps"].State, "unknown"), Detail: byID["gps"].Detail})
	parts = append(parts, partItem{ID: "mag", Label: "La bàn", State: orState(byID["mag"].State, "unknown"), Detail: byID["mag"].Detail})
	parts = append(parts, partItem{ID: "battery", Label: "Pin", State: orState(byID["battery"].State, "unknown"), Detail: byID["battery"].Detail})
	camState, camDetail := cameraPart()
	parts = append(parts, partItem{ID: "camera", Label: "Camera", State: camState, Detail: camDetail})
	_ = telem
	return parts
}

func oneMotor(s escSlot, all [8]escSlot, armed bool) (string, string) {
	if s.Voltage == 0 && neighborHasVoltage(all) {
		return "bad", "ESC này không báo điện trong khi ESC khác vẫn có."
	}
	if armed && s.Voltage > 1 && s.Rpm == 0 {
		return "bad", "Đã mở khóa nhưng động cơ này không quay."
	}
	if !armed {
		return "ok", "ESC có mặt. Chưa quay vì chưa mở khóa."
	}
	return "ok", "Đang báo vòng quay."
}

func cameraPart() (string, string) {
	c0 := camera.ReadStreamStats(0, 5*time.Second)
	c1 := camera.ReadStreamStats(1, 5*time.Second)
	live := 0
	if c0 != nil && c0.FPSWindow >= 0.5 {
		live++
	}
	if c1 != nil && c1.FPSWindow >= 0.5 {
		live++
	}
	switch live {
	case 2:
		return "ok", "Cả hai camera đang có hình."
	case 1:
		return "warn", "Một camera có hình, camera còn lại chưa thấy."
	default:
		return "unknown", "Chưa thấy hình camera. Không chặn cất cánh."
	}
}

func motorCount(kind string) int {
	switch kind {
	case "quad":
		return 4
	case "hex":
		return 6
	case "octo":
		return 8
	case "tri":
		return 3
	case "rover":
		return 4
	default:
		return 0
	}
}

func motorLabels(kind string, n int) []string {
	if kind == "rover" {
		return []string{"Bánh trước-trái", "Bánh trước-phải", "Bánh sau-trái", "Bánh sau-phải"}
	}
	if kind == "quad" && n == 4 {
		return []string{"Động cơ 1 · trước-phải", "Động cơ 2 · sau-trái", "Động cơ 3 · trước-trái", "Động cơ 4 · sau-phải"}
	}
	out := make([]string, n)
	for i := range out {
		out[i] = "Động cơ " + sprintfInt(i+1)
	}
	return out
}

func motorID(i int) string {
	return "m" + sprintfInt(i+1)
}

func verdictFor(linked, armed bool, mode string, checks []checkItem, acks []preflightAck) (string, string) {
	if !linked {
		return "offline", "Chưa nối được Pixhawk nên chưa kết luận được có được cất cánh hay không."
	}
	if armed && (mode == "TAKEOFF" || mode == "AUTO") {
		return "flying", "Động cơ đã mở khóa và chế độ là " + mode + "."
	}
	if armed {
		return "armed", "Động cơ đã mở khóa. Máy được phép vào lệnh bay, chế độ hiện tại là " + mode + "."
	}
	if ack, ok := latestAck(acks, cmdArmDisarm); ok && ack.Result == 1 {
		return "arm_rejected", "Server đã gửi cất cánh và PX4 nhận lệnh đó. Nhưng lệnh mở khóa động cơ bị từ chối tạm thời, nên máy chưa được bay. Dòng xanh “đã nhận cất cánh” không có nghĩa là được phép rời đất."
	}
	if ack, ok := latestAck(acks, cmdArmDisarm); ok && (ack.Result == 2 || ack.Result == 4) {
		return "arm_rejected", "PX4 từ chối mở khóa động cơ. Chưa được cất cánh."
	}
	for _, c := range checks {
		if c.State == "bad" && (c.ID == "prearm" || c.ID == "ekf" || c.ID == "gps" || c.ID == "battery" || c.ID == "motors") {
			return "blocked", "Còn điều kiện chưa đạt. Chưa nên nhấn cất cánh trên server."
		}
	}
	if prearm, ok := findCheck(checks, "prearm"); ok && prearm.State == "ok" {
		return "ready", "PX4 đã qua kiểm tra mở khóa. Có thể gửi cất cánh. Máy vẫn đang khóa động cơ cho đến khi lệnh mở khóa được chấp nhận."
	}
	return "blocked", "Chưa đủ căn cứ để nói máy đã sẵn sàng. Xem các mục còn đỏ hoặc xám (được đánh dấu bằng mũi tên 3D)."
}

func verdictTitle(code string) string {
	switch code {
	case "offline":
		return "Chưa nối máy bay"
	case "flying":
		return "Đang trong chế độ bay"
	case "armed":
		return "Đã mở khóa"
	case "ready":
		return "Đủ điều kiện mở khóa"
	case "arm_rejected":
		return "Chưa được cất cánh"
	default:
		return "Chưa đủ điều kiện"
	}
}

func decodeAcks(acks []preflightAck) []map[string]string {
	out := make([]map[string]string, 0, len(acks))
	for _, a := range acks {
		out = append(out, map[string]string{
			"command":       sprintfInt(int(a.Command)),
			"command_label": commandLabel(a.Command),
			"result_label":  resultLabel(a.Result),
			"meaning":       ackMeaning(a.Command, a.Result),
			"at":            a.At.Format("15:04:05"),
		})
	}
	return out
}

func commandLabel(cmd uint16) string {
	switch cmd {
	case cmdNavTakeoff:
		return "Cất cánh"
	case cmdArmDisarm:
		return "Mở khóa động cơ"
	case 21:
		return "Hạ cánh"
	case 20:
		return "Quay về"
	default:
		return "Lệnh " + sprintfInt(int(cmd))
	}
}

func resultLabel(r uint8) string {
	switch r {
	case 0:
		return "Đã nhận"
	case 1:
		return "Từ chối tạm thời"
	case 2:
		return "Từ chối"
	case 3:
		return "Không hỗ trợ"
	case 4:
		return "Thất bại"
	case 5:
		return "Đang làm"
	case 6:
		return "Đã hủy"
	default:
		return "Mã " + sprintfInt(int(r))
	}
}

func ackMeaning(cmd uint16, result uint8) string {
	if cmd == cmdNavTakeoff && result == 0 {
		return "Máy đã nhận lệnh cất cánh. Dòng này không có nghĩa là được phép rời đất."
	}
	if cmd == cmdArmDisarm && result == 1 {
		return "Chưa mở được khóa động cơ vì còn điều kiện chưa đạt. Sửa mục đỏ rồi thử lại."
	}
	if cmd == cmdArmDisarm && result == 0 {
		return "Động cơ đã được phép quay."
	}
	if result == 0 {
		return "PX4 đã chấp nhận lệnh."
	}
	return "PX4 không thực hiện lệnh này."
}

func decodeNotes(notes []preflightNote) []map[string]string {
	out := make([]map[string]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, map[string]string{
			"at":      n.At.Format("15:04:05"),
			"text":    n.Text,
			"meaning": translateStatus(n.Text),
		})
	}
	return out
}

func translateStatus(text string) string {
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "arming denied"):
		return "PX4 không cho mở khóa."
	case strings.Contains(l, "gps") && strings.Contains(l, "fix"):
		return "GPS chưa đủ điều kiện."
	case strings.Contains(l, "ekf"):
		return "Bộ lọc vị trí chưa đủ dữ liệu."
	case strings.Contains(l, "accelerometer"):
		return "Gia tốc kế chưa đạt."
	case strings.Contains(l, "gyro"):
		return "Con quay chưa đạt."
	case strings.Contains(l, "mag"):
		return "La bàn chưa đạt hoặc đang bị nhiễu."
	case strings.Contains(l, "heading"):
		return "Hướng mũi chưa ổn định."
	case strings.Contains(l, "battery"):
		return "Pin chưa đạt điều kiện mở khóa."
	case strings.Contains(l, "attitude"):
		return "Tư thế máy chưa ổn định. Để máy đứng yên."
	case strings.Contains(l, "position") && strings.Contains(l, "error"):
		return "Sai số vị trí còn lớn."
	case strings.Contains(l, "preflight"):
		return "Một kiểm tra trước khi bay chưa đạt."
	default:
		return ""
	}
}

func latestAck(acks []preflightAck, cmd uint16) (preflightAck, bool) {
	for _, a := range acks {
		if a.Command == cmd && time.Since(a.At) < 3*time.Minute {
			return a, true
		}
	}
	return preflightAck{}, false
}

func findCheck(checks []checkItem, id string) (checkItem, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return checkItem{}, false
}

func bit(v, b uint32) bool { return v&b != 0 }

func tern(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func orState(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func worstState(states ...string) string {
	rank := map[string]int{"bad": 4, "warn": 3, "unknown": 2, "idle": 1, "ok": 0, "skip": 0, "": 0}
	best := "ok"
	for _, s := range states {
		if rank[s] > rank[best] {
			best = s
		}
	}
	if best == "" {
		return "unknown"
	}
	return best
}

func sprintfInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func sprintfVolt(v float64) string {
	tenths := int(v*10 + 0.5)
	return sprintfInt(tenths/10) + "." + sprintfInt(tenths%10) + " V"
}
