package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"DroneBridge/redact"
)

// Mức syslog cho journald (stdout của unit systemd đọc tiền tố "<N>").
const (
	sevCrit    = 2
	sevErr     = 3
	sevWarning = 4
	sevInfo    = 6
)

// Có JOURNAL_STREAM nghĩa là stdout nối thẳng vào journald (chạy dưới systemd).
var toJournal = os.Getenv("JOURNAL_STREAM") != ""

// severityOf phân loại một dòng log. Gói auth/web/forwarder dùng log.Printf không có mức,
// nên dòng chứa dấu hiệu lỗi rõ ràng cũng tính là cảnh báo.
func severityOf(msg string) int {
	switch {
	case strings.Contains(msg, "[FATAL]"):
		return sevCrit
	case strings.Contains(msg, "[ERROR]"):
		return sevErr
	case strings.Contains(msg, "[WARN]"),
		strings.Contains(msg, "❌"),
		strings.Contains(msg, "✗"),
		strings.Contains(msg, " reject"),
		strings.Contains(msg, "failed"),
		strings.Contains(msg, "FAIL"):
		return sevWarning
	}
	return sevInfo
}

// lineWriter là đích của mọi dòng log: stdout (journald) và file lỗi.
type lineWriter struct{}

func (lineWriter) Write(p []byte) (int, error) {
	writeLine(p)
	return len(p), nil
}

func writeLine(p []byte) {
	// Lưới an toàn: không dòng log nào (kể cả output tiến trình con) mang UUID đầy đủ.
	if bytes.Count(p, []byte{'-'}) >= 4 {
		p = []byte(redact.UUIDs(string(p)))
	}
	sev := severityOf(string(p))
	if toJournal {
		os.Stdout.Write(append([]byte(fmt.Sprintf("<%d>", sev)), p...))
	} else {
		os.Stdout.Write(p)
	}
	if sev <= sevWarning {
		errorLog.write(p)
	}
}

// ── File lỗi: chỉ WARN/ERROR/FATAL, xoay vòng theo dung lượng, gộp dòng lặp ─────

// errorRepeatWindow: cùng một lỗi (khác số) trong khoảng này chỉ ghi một lần,
// lần ghi kế tiếp kèm số lần đã bỏ qua. Vòng thử lại auth 15 s/lần không làm đầy file.
const errorRepeatWindow = 60 * time.Second

type repeatState struct {
	last       time.Time
	suppressed int
}

type errorFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
	seen     map[string]*repeatState
	failed   bool
}

var errorLog = &errorFile{}

// ConfigureErrorFile bật file lỗi. path rỗng = tắt. Tổng dung lượng tối đa ≈ maxBytes × (keep+1).
func ConfigureErrorFile(path string, maxBytes int64, keep int) {
	errorLog.mu.Lock()
	defer errorLog.mu.Unlock()
	if errorLog.f != nil {
		errorLog.f.Close()
		errorLog.f = nil
	}
	errorLog.path = path
	errorLog.maxBytes = maxBytes
	errorLog.keep = keep
	errorLog.seen = map[string]*repeatState{}
	errorLog.failed = false
}

// ErrorFilePath trả về đường dẫn file lỗi đang dùng ("" nếu tắt).
func ErrorFilePath() string {
	errorLog.mu.Lock()
	defer errorLog.mu.Unlock()
	return errorLog.path
}

// repeatKey bỏ phần ngày giờ đầu dòng và thay chữ số bằng '#', để lỗi giống nhau
// (khác thời gian, cổng, số lần thử) dùng chung một khóa.
func repeatKey(line string) string {
	if i := strings.Index(line, "["); i >= 0 {
		line = line[i:]
	}
	b := []byte(line)
	for i, c := range b {
		if c >= '0' && c <= '9' {
			b[i] = '#'
		}
	}
	return string(b)
}

func (e *errorFile) write(p []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.path == "" || e.failed {
		return
	}
	now := time.Now()
	line := strings.TrimRight(string(p), "\n")
	key := repeatKey(line)
	st := e.seen[key]
	if st != nil && now.Sub(st.last) < errorRepeatWindow {
		st.suppressed++
		return
	}
	if st == nil {
		if len(e.seen) > 500 {
			for k, v := range e.seen {
				if now.Sub(v.last) > 10*errorRepeatWindow {
					delete(e.seen, k)
				}
			}
		}
		st = &repeatState{}
		e.seen[key] = st
	}
	if st.suppressed > 0 {
		line = fmt.Sprintf("%s  (+%d lần tương tự trong %s trước)", line, st.suppressed, errorRepeatWindow)
	}
	st.last = now
	st.suppressed = 0
	if err := e.appendLine(line + "\n"); err != nil {
		e.failed = true
		os.Stdout.Write([]byte(fmt.Sprintf("[LOGGER] tắt file lỗi %s: %v\n", e.path, err)))
	}
}

func (e *errorFile) appendLine(line string) error {
	if e.f == nil {
		if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(e.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		st, _ := f.Stat()
		e.f = f
		e.size = st.Size()
	}
	if e.maxBytes > 0 && e.size+int64(len(line)) > e.maxBytes {
		if err := e.rotate(); err != nil {
			return err
		}
	}
	n, err := e.f.WriteString(line)
	e.size += int64(n)
	return err
}

// rotate: file.log → file.log.1 → … → file.log.<keep>; bản cũ nhất bị xóa.
func (e *errorFile) rotate() error {
	e.f.Close()
	e.f = nil
	os.Remove(fmt.Sprintf("%s.%d", e.path, e.keep))
	for i := e.keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", e.path, i), fmt.Sprintf("%s.%d", e.path, i+1))
	}
	if e.keep > 0 {
		os.Rename(e.path, e.path+".1")
	} else {
		os.Remove(e.path)
	}
	f, err := os.OpenFile(e.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	e.f = f
	e.size = 0
	return nil
}
