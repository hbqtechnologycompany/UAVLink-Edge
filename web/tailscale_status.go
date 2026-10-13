//go:build ignore

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	tailscaleAuthKeyFile = "/opt/dronebridge/data/tailscale_authkey"
	tailscaleInstallSh   = "/opt/dronebridge/install/network/install_tailscale.sh"
)

var loginURLPattern = regexp.MustCompile(`https://login\.tailscale\.com/a/[a-z0-9]+`)

// TailscaleLiveStatus reads tailscaled + tailscale CLI (admin plane).
func TailscaleLiveStatus() map[string]interface{} {
	out := map[string]interface{}{
		"interface":      "tailscale0",
		"service":        systemdActive("tailscaled.service"),
		"backend_state":  "",
		"online":         false,
		"logged_in":      false,
		"ip":             "",
		"ipv6":           "",
		"hostname":       "",
		"dns_name":       "",
		"tailnet":        "",
		"reply_rule":     "",
		"auth_key_file":  fileExists(tailscaleAuthKeyFile),
		"dashboard_port": 8080,
		"dashboard_path": "/dashboard.html",
	}

	if ip, _ := ifaceIPv4("tailscale0"); ip != "" {
		out["ip"] = ip
		out["link_up"] = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	raw, err := cmd.Output()
	if err != nil {
		out["error"] = strings.TrimSpace(err.Error())
		enrichTailscaleURLs(out)
		return out
	}

	var doc map[string]interface{}
	if json.Unmarshal(raw, &doc) != nil {
		out["error"] = "invalid tailscale status json"
		enrichTailscaleURLs(out)
		return out
	}

	if self, ok := doc["Self"].(map[string]interface{}); ok {
		out["hostname"], _ = self["HostName"].(string)
		out["dns_name"], _ = self["DNSName"].(string)
		out["tailnet"], _ = self["TailscaleIPs"].(string)
		if online, ok := self["Online"].(bool); ok {
			out["online"] = online
		}
		if addrs, ok := self["TailscaleIPs"].([]interface{}); ok {
			for _, a := range addrs {
				s, _ := a.(string)
				if strings.Contains(s, ":") {
					if out["ipv6"] == "" {
						out["ipv6"] = s
					}
				} else if strings.HasPrefix(s, "100.") && out["ip"] == "" {
					out["ip"] = s
				}
			}
		}
	}
	if bs, ok := doc["BackendState"].(string); ok {
		out["backend_state"] = bs
		out["logged_in"] = bs == "Running" && out["ip"] != ""
	}

	enrichTailscaleUplink(out, doc)
	enrichTailscaleURLs(out)
	return out
}

// enrichTailscaleUplink — mạng vật lý Tailscale dùng để đồng bộ + mesh (khác ống data 4G).
func enrichTailscaleUplink(out map[string]interface{}, statusDoc map[string]interface{}) {
	defIface, defGW := defaultRouteDev()
	out["control_uplink_iface"] = defIface
	out["control_uplink_gateway"] = defGW
	if defIface != "" {
		if ip, _ := ifaceIPv4(defIface); ip != "" {
			out["control_uplink_ip"] = ip
		}
	}

	if self, ok := statusDoc["Self"].(map[string]interface{}); ok {
		if relay, ok := self["Relay"].(string); ok && relay != "" {
			out["derp_home"] = relay
		}
		if cur, ok := self["CurAddr"].(string); ok && cur != "" {
			out["self_transport"] = "direct " + cur
		} else if relay, ok := self["Relay"].(string); ok && relay != "" {
			out["self_transport"] = "derp:" + relay
		}
	}

	if peers, ok := statusDoc["Peer"].(map[string]interface{}); ok {
		for _, peer := range peers {
			pm, ok := peer.(map[string]interface{})
			if !ok {
				continue
			}
			online, _ := pm["Online"].(bool)
			if !online {
				continue
			}
			name, _ := pm["HostName"].(string)
			out["peer_sample"] = name
			if cur, ok := pm["CurAddr"].(string); ok && cur != "" {
				out["peer_transport"] = "direct " + cur
				if strings.HasPrefix(cur, "192.168.") || strings.HasPrefix(cur, "10.") {
					out["peer_transport_note"] = "Cùng LAN/WiFi — mesh trực tiếp, không qua 4G"
				}
			} else if relay, ok := pm["Relay"].(string); ok && relay != "" {
				out["peer_transport"] = "derp:" + relay
				out["peer_transport_note"] = "Không direct được — relay qua DERP"
			}
			break
		}
	}

	parts := []string{}
	if defIface != "" {
		parts = append(parts, fmt.Sprintf("Điều khiển/login Tailscale → %s (default route)", defIface))
	}
	if pt, ok := out["peer_transport"].(string); ok && pt != "" {
		parts = append(parts, "Mesh VPN tới peer: "+pt)
	}
	parts = append(parts, "Reply SSH/dashboard từ IP Tailscale → tailscale0 (table 102)")
	out["uplink_summary"] = strings.Join(parts, " · ")
}

func defaultRouteDev() (iface, gateway string) {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "default ") {
			continue
		}
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == "via" {
				gateway = fields[i+1]
			}
			if fields[i] == "dev" {
				iface = fields[i+1]
			}
		}
		if iface != "" {
			return iface, gateway
		}
	}
	return "", ""
}

func enrichTailscaleURLs(ts map[string]interface{}) {
	host := ""
	if dns, ok := ts["dns_name"].(string); ok && dns != "" {
		host = strings.TrimSuffix(dns, ".")
	}
	if host == "" {
		if hn, ok := ts["hostname"].(string); ok && hn != "" {
			host = hn
		}
	}
	if host == "" {
		host = tailscaleHostnameFromDeviceName(GetDeviceName())
	}

	port := 8080
	ts["dashboard_host"] = host
	if ip, _ := ts["ip"].(string); ip != "" {
		ts["dashboard_url_ip"] = fmt.Sprintf("http://%s:%d/dashboard.html", ip, port)
	}
	if host != "" {
		ts["dashboard_url_dns"] = fmt.Sprintf("http://%s:%d/dashboard.html", host, port)
		ts["ssh_hint"] = fmt.Sprintf("ssh %s@%s", os.Getenv("USER"), ipOrHost(ts, host))
	}
}

func ipOrHost(ts map[string]interface{}, host string) string {
	if ip, ok := ts["ip"].(string); ok && ip != "" {
		return ip
	}
	return host
}

func tailscaleHostnameFromDeviceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.ToLower(s)
}

func mergeAdminPlane(out map[string]interface{}) {
	admin, _ := out["admin"].(map[string]interface{})
	if admin == nil {
		admin = map[string]interface{}{}
	}

	tsLive := TailscaleLiveStatus()
	if tsBlock, ok := admin["tailscale"].(map[string]interface{}); ok {
		for k, v := range tsBlock {
			if tsLive[k] == nil || tsLive[k] == "" {
				tsLive[k] = v
			}
		}
		if rr, ok := tsBlock["reply_rule"].(string); ok {
			tsLive["reply_rule"] = rr
		}
	}
	admin["tailscale"] = tsLive
	out["admin"] = admin
	out["tailscale"] = tsLive

	drone := GetDeviceName()
	out["drone_name"] = drone
	out["device"] = deviceProfileResponse()

	if url, ok := tsLive["dashboard_url_dns"].(string); ok && url != "" {
		out["dashboard_url"] = url
	} else if url, ok := tsLive["dashboard_url_ip"].(string); ok && url != "" {
		out["dashboard_url"] = url
	}
}

func saveTailscaleAuthKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" || !strings.HasPrefix(key, "tskey-") {
		return fmt.Errorf("auth key phải bắt đầu bằng tskey-")
	}
	dir := filepath.Dir(tailscaleAuthKeyFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tailscaleAuthKeyFile, []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

func runTailscaleInstallJoin() (string, error) {
	script := tailscaleInstallSh
	if _, err := os.Stat(script); err != nil {
		script = projectPath("install", "network", "install_tailscale.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "bash", script)
	cmd.Env = append(os.Environ(), "DRONEBRIDGE_DIR=/opt/dronebridge")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tailscaleLoginURL(hostname string) (string, error) {
	if hostname == "" {
		hostname = tailscaleHostnameFromDeviceName(GetDeviceName())
	}
	args := []string{
		"tailscale", "up",
		"--accept-routes=false", "--accept-dns=true", "--ssh=false",
	}
	if hostname != "" {
		args = append(args, "--hostname="+hostname)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", args...)
	out, _ := cmd.CombinedOutput()
	text := string(out)
	if m := loginURLPattern.FindString(text); m != "" {
		return m, nil
	}
	if strings.Contains(text, "Logged in") || strings.Contains(text, "Success") {
		return "", nil
	}
	return "", fmt.Errorf("không lấy được URL đăng nhập — có thể đã đăng nhập hoặc tailscaled chưa chạy")
}
