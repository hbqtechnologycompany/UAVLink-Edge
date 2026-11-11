package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"DroneBridge/config"
)

var boardOnce struct {
	sync.Once
	info map[string]interface{}
}

// boardInfo mô tả board đang chạy (theo config.Load → ResolveBoardProfile), chỉ thông tin công khai.
func boardInfo() map[string]interface{} {
	boardOnce.Do(func() {
		hw := config.ResolveBoardProfile("")
		if cfg, err := loadAppConfig(); err == nil && cfg.Hardware.Name != "" {
			hw = cfg.Hardware
		}
		info := map[string]interface{}{
			"id":          hw.Name,
			"label":       "CM5",
			"name":        "Raspberry Pi CM5",
			"soc":         "BCM2712",
			"telem2_port": hw.Telem2Port,
			"telem3_port": hw.Telem3Port,
			"lcd_bus":     hw.LCDBus,
			"eth_iface":   hw.EthIface,
		}
		if hw.Name == config.BoardWalnutPi {
			info["label"] = "CM2"
			info["name"] = "WalnutPi CM2"
			info["soc"] = "Allwinner T527"
		}
		if model, err := os.ReadFile("/proc/device-tree/model"); err == nil {
			info["model"] = string(bytes.TrimRight(model, "\x00"))
		}
		boardOnce.info = info
	})
	return boardOnce.info
}

func boardLabel() string {
	label, _ := boardInfo()["label"].(string)
	return label
}

func handleSystemBoard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(boardInfo())
}
