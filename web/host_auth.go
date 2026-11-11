package web

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Dashboard chưa có đăng nhập: thao tác nhạy cảm (xem UUID/API key đầy đủ, tạo key,
// đăng ký lại, reboot/tắt nguồn) đòi mật khẩu hệ thống, do sudo kiểm tra.
// Sai passwordMaxFails lần liên tiếp từ một IP → khóa IP đó passwordLockout.
const (
	passwordMaxFails = 5
	passwordLockout  = 5 * time.Minute
)

var passwordFails = struct {
	sync.Mutex
	byIP map[string]*passwordFailState
}{byIP: map[string]*passwordFailState{}}

type passwordFailState struct {
	count int
	until time.Time
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// passwordLocked trả thông báo nếu IP đang bị khóa vì nhập sai nhiều lần.
func passwordLocked(r *http.Request) string {
	passwordFails.Lock()
	defer passwordFails.Unlock()
	st := passwordFails.byIP[clientIP(r)]
	if st == nil || time.Now().After(st.until) {
		return ""
	}
	return fmt.Sprintf("Nhập sai mật khẩu quá %d lần — thử lại sau %s.",
		passwordMaxFails, time.Until(st.until).Round(time.Second))
}

func recordPassword(r *http.Request, ok bool) {
	passwordFails.Lock()
	defer passwordFails.Unlock()
	ip := clientIP(r)
	if ok {
		delete(passwordFails.byIP, ip)
		return
	}
	st := passwordFails.byIP[ip]
	if st == nil {
		st = &passwordFailState{}
		passwordFails.byIP[ip] = st
	}
	st.count++
	if st.count >= passwordMaxFails {
		st.until = time.Now().Add(passwordLockout)
		st.count = 0
	}
}

// sudoWrongPassword nhận diện output của sudo khi mật khẩu sai.
func sudoWrongPassword(out []byte) bool {
	text := strings.ToLower(string(out))
	return strings.Contains(text, "sorry") || strings.Contains(text, "incorrect password") ||
		strings.Contains(text, "password is required")
}

// verifyHostPassword kiểm mật khẩu hệ thống bằng sudo (không chạy lệnh nào khác).
// Trả http.StatusOK khi đúng; ngược lại là mã HTTP nên trả cho client kèm thông báo.
func verifyHostPassword(r *http.Request, pw []byte) (int, string) {
	if msg := passwordLocked(r); msg != "" {
		return http.StatusTooManyRequests, msg
	}
	if len(bytes.TrimSpace(pw)) == 0 {
		return http.StatusUnauthorized, "Cần mật khẩu hệ thống."
	}
	stdin := append(append([]byte{}, pw...), '\n')
	defer zeroBytes(stdin)
	cmd := exec.Command("sudo", "-S", "-k", "-p", "", "--", "/bin/true")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = sudoEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err == nil {
		recordPassword(r, true)
		return http.StatusOK, ""
	}
	if sudoWrongPassword(out) {
		recordPassword(r, false)
		if msg := passwordLocked(r); msg != "" {
			return http.StatusTooManyRequests, msg
		}
		return http.StatusUnauthorized, "Mật khẩu không đúng."
	}
	return http.StatusInternalServerError, "Không kiểm tra được mật khẩu: " + strings.TrimSpace(string(out))
}
