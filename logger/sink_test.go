package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeverityOf(t *testing.T) {
	cases := map[string]int{
		"2026/11/11 08:00:00 [FATAL] boom":                                           sevCrit,
		"2026/11/11 08:00:00 [ERROR] x":                                              sevErr,
		"2026/11/11 08:00:00 [WARN] [AUTH] retry":                                    sevWarning,
		"2026/11/11 08:00:00 [AUTH] 🔌 Reset TCP connection: AUTH_ACK reject error=1": sevWarning,
		"2026/11/11 08:00:00 [PBR] ✗ không xác định":                                 sevWarning,
		"2026/11/11 08:00:00 [INFO] [CAMERA] Stats: 10 sent":                         sevInfo,
		"2026/11/11 08:00:00 [SESSION_REFRESH] ✓ Session extended":                   sevInfo,
	}
	for msg, want := range cases {
		if got := severityOf(msg); got != want {
			t.Errorf("severityOf(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestErrorFileRepeatAndRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "err.log")
	ConfigureErrorFile(path, 200, 2)
	defer ConfigureErrorFile("", 0, 0)

	errorLog.write([]byte("2026/11/11 08:00:00 [WARN] [AUTH] retry failed (error=1)\n"))
	errorLog.write([]byte("2026/11/11 08:00:15 [WARN] [AUTH] retry failed (error=1)\n"))
	errorLog.write([]byte("2026/11/11 08:00:30 [WARN] [AUTH] retry failed (error=1)\n"))
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), "retry failed"); n != 1 {
		t.Fatalf("lặp trong cửa sổ phải gộp thành 1 dòng, có %d:\n%s", n, data)
	}

	errorLog.seen[repeatKey("2026/11/11 08:00:00 [WARN] [AUTH] retry failed (error=1)")].last =
		time.Now().Add(-2 * errorRepeatWindow)
	errorLog.write([]byte("2026/11/11 08:02:00 [WARN] [AUTH] retry failed (error=1)\n"))
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "(+2 lần tương tự") {
		t.Fatalf("thiếu đếm số lần bỏ qua:\n%s", data)
	}

	for i := 0; i < 20; i++ {
		errorLog.write([]byte("2026/11/11 08:03:00 [ERROR] khác nhau " + strings.Repeat("x", i+1) + "\n"))
	}
	for _, p := range []string{path, path + ".1", path + ".2"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("thiếu %s: %v", p, err)
		}
		if st.Size() > 200 {
			t.Fatalf("%s vượt giới hạn: %d", p, st.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatalf("giữ quá số file cho phép")
	}
}
