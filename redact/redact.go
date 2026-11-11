package redact

import (
	"regexp"
	"strings"
)

// Phân loại dữ liệu nhạy cảm:
//   - Bí mật (shared_secret, secret_key, session token, API key, khóa VPN, mật khẩu):
//     không bao giờ ghi log đầy đủ, không gửi ra dashboard; chỉ dùng Hint.
//   - Định danh (drone UUID, publish path chứa UUID): che bằng redact.UUID khi hiển thị/ghi log;
//     xem đầy đủ phải nhập mật khẩu hệ thống (web: /api/drone/reveal).

var uuidInText = regexp.MustCompile(`(?i)\b([0-9a-f]{2})[0-9a-f]{6}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{10}([0-9a-f]{2})\b`)

const mask = "••••"

// UUID: "9f14f259-576c-41ad-90f9-783e6d831455" → "9f••••55" (chỉ 2 ký tự đầu, 2 ký tự cuối).
// Chuỗi khác cũng giữ 2 đầu, 2 cuối; ngắn hơn 8 ký tự thì che hết.
func UUID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if m := uuidInText.FindStringSubmatch(s); m != nil && len(m[0]) == len(s) {
		return m[1] + mask + m[2]
	}
	if len(s) < 8 {
		return mask
	}
	return s[:2] + mask + s[len(s)-2:]
}

// UUIDs che mọi UUID xuất hiện trong một đoạn text (log, đường dẫn stream, thông báo).
func UUIDs(s string) string {
	return uuidInText.ReplaceAllString(s, "${1}"+mask+"${2}")
}

// Hint trả 4 ký tự cuối của một bí mật để người dùng nhận ra, không đủ để dùng.
func Hint(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "••••"
	}
	return "••••" + secret[len(secret)-4:]
}
