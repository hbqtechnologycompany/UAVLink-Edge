package camera

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"DroneBridge/config"
	"DroneBridge/logger"
)

// MemoryTier is the CM5 RAM class (2 / 4 / 8 GiB).
type MemoryTier int

const (
	Tier2GB MemoryTier = 2
	Tier4GB MemoryTier = 4
	Tier8GB MemoryTier = 8
)

// StreamMemoryProfile holds effective stream limits after RAM tier tuning.
type StreamMemoryProfile struct {
	Tier             MemoryTier
	Applied          bool
	BufferCount      int
	LoresSize        []int
	DetectFrameSkip  int
	OverlayFrameSkip int
	Bitrate          int
	Size             []int
	LowMemoryMode    bool
	StreamRole       string // "primary" | "secondary"
}

// DetectMemoryTier reads MemTotal from /proc/meminfo.
func DetectMemoryTier() MemoryTier {
	kb := memTotalKB()
	switch {
	case kb <= 0:
		return Tier4GB
	case kb <= 2_750_000: // ≤ ~2.7 GiB → 2 GB CM5
		return Tier2GB
	case kb <= 5_750_000: // ≤ ~5.6 GiB → 4 GB CM5
		return Tier4GB
	default:
		return Tier8GB
	}
}

func memTotalKB() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

func streamUsesCV(s config.CameraStreamConfig) bool {
	return s.DetectionEnabled || s.OverlayEnabled
}

func countCVStreams(streams []config.CameraStreamConfig) int {
	n := 0
	for _, s := range streams {
		if s.Enabled && streamUsesCV(s) {
			n++
		}
	}
	return n
}

func capSizeTo(size []int, maxW, maxH int) []int {
	if len(size) < 2 {
		return []int{maxW, maxH}
	}
	w, h := size[0], size[1]
	if w*h <= maxW*maxH {
		return []int{w, h}
	}
	ar := float64(w) / float64(h)
	w, h = maxW, maxH
	if ar > 4.0/3.0+0.05 {
		h = int(float64(w) / ar)
	} else if ar < 4.0/3.0-0.05 {
		w = int(float64(h) * ar)
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return []int{w, h}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func loresForTier(tier MemoryTier, mainSize []int, user []int) []int {
	if len(user) >= 2 && user[0] > 0 && user[1] > 0 {
		lw, lh := user[0], user[1]
		switch tier {
		case Tier2GB:
			if lw*lh > 240*180 {
				return []int{240, 180}
			}
		case Tier4GB:
			if lw*lh > 320*240 {
				return []int{320, 240}
			}
		}
		return []int{lw, lh}
	}
	if len(mainSize) >= 2 && mainSize[0]*mainSize[1] > 640*480 {
		switch tier {
		case Tier2GB:
			return []int{240, 180}
		case Tier4GB:
			return []int{320, 240}
		}
	}
	return []int{320, 240}
}

// BuildStreamMemoryProfile merges user yaml with RAM-tier limits (gentle — avoids over-downgrade).
func BuildStreamMemoryProfile(cfg *config.Config, stream config.CameraStreamConfig) StreamMemoryProfile {
	size := stream.Size
	if len(size) < 2 {
		size = []int{640, 480}
	}
	lores := stream.LoresSize
	if len(lores) < 2 {
		lores = []int{320, 240}
	}
	detectSkip := stream.DetectFrameSkip
	if detectSkip <= 0 {
		detectSkip = 3
	}
	overlaySkip := stream.OverlayFrameSkip
	if overlaySkip <= 0 {
		overlaySkip = 5
	}
	bitrate := stream.Bitrate
	if bitrate <= 0 {
		bitrate = 5000
	}
	bufCount := streamBufferCount(cfg)
	lowMem := cfg != nil && cfg.Camera.LowMemoryMode

	role := "secondary"
	if cfg != nil && stream.CameraID == cfg.Camera.PrimaryCameraID {
		role = "primary"
	}

	out := StreamMemoryProfile{
		Tier:             Tier4GB,
		Applied:          false,
		BufferCount:      bufCount,
		LoresSize:        append([]int(nil), lores...),
		DetectFrameSkip:  detectSkip,
		OverlayFrameSkip: overlaySkip,
		Bitrate:          bitrate,
		Size:             append([]int(nil), size...),
		LowMemoryMode:    lowMem,
		StreamRole:       role,
	}

	if cfg == nil || !cfg.Camera.AutoMemoryProfile {
		return out
	}

	tier := DetectMemoryTier()
	out.Tier = tier
	out.Applied = true

	enabled := enabledStreamsSorted(cfg)
	multiCam := len(enabled) > 1
	cvCount := countCVStreams(enabled)
	mainPixels := size[0] * size[1]
	isPrimary := stream.CameraID == cfg.Camera.PrimaryCameraID
	hasCV := streamUsesCV(stream)

	switch tier {
	case Tier2GB:
		out.BufferCount = 2
		out.LowMemoryMode = true
		out.LoresSize = loresForTier(tier, size, lores)
		out.DetectFrameSkip = maxInt(detectSkip, 5)
		out.OverlayFrameSkip = maxInt(overlaySkip, 8)
		out.Bitrate = minInt(bitrate, 3500)
		if hasCV {
			if multiCam || mainPixels > 640*480 {
				out.Size = capSizeTo(size, 640, 480)
				out.Bitrate = minInt(out.Bitrate, 3000)
			} else if mainPixels > 960*720 {
				out.Size = capSizeTo(size, 960, 720)
			}
		}
	case Tier4GB:
		out.BufferCount = 2
		if !multiCam || cvCount <= 1 {
			out.BufferCount = 3
		}
		out.LoresSize = loresForTier(tier, size, lores)
		if hasCV && multiCam && cvCount >= 2 {
			out.DetectFrameSkip = maxInt(detectSkip, 4)
			if !isPrimary && mainPixels > 640*480 {
				out.Size = capSizeTo(size, 640, 480)
				out.Bitrate = minInt(bitrate, 4000)
			}
		}
		if mainPixels > 960*720 {
			out.Size = capSizeTo(size, 960, 720)
			out.Bitrate = minInt(bitrate, 5000)
		}
	case Tier8GB:
		out.BufferCount = 3
		if multiCam && cvCount >= 2 {
			out.BufferCount = 3
		} else {
			out.BufferCount = minInt(maxInt(bufCount, 3), 4)
		}
		out.LoresSize = loresForTier(tier, size, lores)
		if mainPixels > 1920*1080 {
			out.Size = capSizeTo(size, 1920, 1080)
		}
	}

	if lowMem {
		out.BufferCount = minInt(out.BufferCount, 2)
		if out.Size[0]*out.Size[1] > 640*480 {
			out.Size = capSizeTo(out.Size, 640, 480)
		}
	}

	logger.Info("[CAMERA] RAM tier %dGB cam%d role=%s buf=%d size=%dx%d lores=%dx%d det_skip=%d ovl_skip=%d bitrate=%d",
		int(tier), stream.CameraID, role,
		out.BufferCount, out.Size[0], out.Size[1],
		out.LoresSize[0], out.LoresSize[1],
		out.DetectFrameSkip, out.OverlayFrameSkip, out.Bitrate)

	return out
}
