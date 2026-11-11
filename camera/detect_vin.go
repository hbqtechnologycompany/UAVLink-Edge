package camera

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const v4l2SysDir = "/sys/class/video4linux"

func readSysName(node string) string {
	b, err := os.ReadFile(filepath.Join(v4l2SysDir, node, "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// vinVideoIndexes returns N for every /dev/videoN owned by Allwinner sunxi-vin (WalnutPi CSI).
func vinVideoIndexes() []int {
	entries, err := os.ReadDir(v4l2SysDir)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "video") {
			continue
		}
		if strings.HasPrefix(readSysName(name), "vin_video") {
			out = append(out, atoi(strings.TrimPrefix(name, "video")))
		}
	}
	sort.Ints(out)
	return out
}

// HasVinCamera reports a WalnutPi/Allwinner CSI stack (no libcamera).
func HasVinCamera() bool {
	return len(vinVideoIndexes()) > 0
}

// vinSensorNames lists sensor subdevs registered by sunxi-vin (only probed sensors get a subdev).
func vinSensorNames() []string {
	entries, err := os.ReadDir(v4l2SysDir)
	if err != nil {
		return nil
	}
	type sd struct {
		idx  int
		name string
	}
	var sensors []sd
	for _, e := range entries {
		node := e.Name()
		if !strings.HasPrefix(node, "v4l-subdev") {
			continue
		}
		n := readSysName(node)
		if n == "" || strings.HasPrefix(n, "sunxi_") || strings.HasPrefix(n, "vin_") {
			continue
		}
		sensors = append(sensors, sd{atoi(strings.TrimPrefix(node, "v4l-subdev")), n})
	}
	sort.Slice(sensors, func(i, j int) bool { return sensors[i].idx < sensors[j].idx })
	out := make([]string, 0, len(sensors))
	for _, s := range sensors {
		out = append(out, s.name)
	}
	return out
}

// parseVinList maps probed sensors onto vin video nodes in order: camN ↔ /dev/videoN.
func parseVinList() []ConnectedCamera {
	reg, _ := LoadRegistry()
	videos := vinVideoIndexes()
	sensors := vinSensorNames()
	var cams []ConnectedCamera
	for i, idx := range videos {
		if i >= len(sensors) {
			break
		}
		conn := "J30"
		if idx == 1 {
			conn = "J29"
		}
		cams = append(cams, ConnectedCamera{
			LibcameraIndex: idx,
			Sensor:         sensors[i],
			SensorName:     sensorDisplayName(reg, sensors[i]),
			PhysicalPort:   "cam" + strconv.Itoa(idx),
			Connector:      conn,
			I2CBus:         "vin",
			Status:         "ok",
		})
	}
	return cams
}
