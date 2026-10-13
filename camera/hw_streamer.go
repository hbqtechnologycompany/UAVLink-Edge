package camera

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"DroneBridge/config"
	"DroneBridge/logger"
)

const defaultHWRpicamBufferCount = 3

// StreamUsesHWPassthrough reports whether a stream can use rpicam-vid HW H.264 (no Python CV).
func StreamUsesHWPassthrough(stream config.CameraStreamConfig) bool {
	return !stream.DetectionEnabled && !stream.OverlayEnabled
}

func allStreamsHWCapable(streams []config.CameraStreamConfig) bool {
	if len(streams) == 0 {
		return false
	}
	for _, s := range streams {
		if !StreamUsesHWPassthrough(s) {
			return false
		}
	}
	return hwToolsAvailable()
}

func hwToolsAvailable() bool {
	rpicam := resolveRpicamVid()
	ffmpeg := resolveFFmpeg()
	return rpicam != "" && ffmpeg != ""
}

func resolveRpicamVid() string {
	if p, err := exec.LookPath("rpicam-vid"); err == nil {
		return p
	}
	if _, err := os.Stat("/usr/bin/rpicam-vid"); err == nil {
		return "/usr/bin/rpicam-vid"
	}
	return ""
}

func resolveFFmpeg() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	if _, err := os.Stat("/usr/bin/ffmpeg"); err == nil {
		return "/usr/bin/ffmpeg"
	}
	return ""
}

func loadStreamerJSON(cameraID int) (streamerJSON, error) {
	path := filepath.Join(FindLandingDir(), streamConfigFilename(cameraID))
	data, err := os.ReadFile(path)
	if err != nil {
		return streamerJSON{}, err
	}
	var payload streamerJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return streamerJSON{}, err
	}
	return payload, nil
}

func libcameraIndexForCamera(cameraID int) int {
	st, err := LoadDetected()
	if err != nil {
		return cameraID
	}
	port := fmt.Sprintf("cam%d", cameraID)
	for _, cam := range st.LastConnected {
		if cam.PhysicalPort == port {
			return cam.LibcameraIndex
		}
	}
	return cameraID
}

func rtspURLFromJSON(sc streamerJSON) string {
	return fmt.Sprintf("rtsp://%s:%d%s", sc.MediamtxHost, sc.MediamtxPort, sc.PublishPath)
}

func buildHWFFmpegCmd(rtspURL string) []string {
	ffmpeg := resolveFFmpeg()
	return []string{
		ffmpeg, "-loglevel", "warning", "-nostats",
		"-probesize", "32768", "-analyzeduration", "0",
		"-fflags", "+genpts+igndts+nobuffer",
		"-use_wallclock_as_timestamps", "1",
		"-flags", "low_delay",
		"-f", "h264", "-i", "pipe:0",
		"-c:v", "copy",
		"-muxdelay", "0", "-muxpreload", "0",
		"-max_interleave_delta", "0",
		"-f", "rtsp", "-rtsp_transport", "tcp",
		rtspURL,
	}
}

func hwEncodeBitrateKbps(sc streamerJSON) int {
	if sc.Bitrate > 0 {
		return sc.Bitrate
	}
	return 4000
}

func hwKeyframeInterval(sc streamerJSON) int {
	gop := sc.KeyframeInterval
	if gop <= 0 {
		gop = 15
	}
	if gop > 30 {
		gop = 30
	}
	return gop
}

func buildHWRpicamCmd(sc streamerJSON, libIdx int) []string {
	rpicam := resolveRpicamVid()
	w, h := 640, 480
	if len(sc.EncodeSize) >= 2 {
		w, h = sc.EncodeSize[0], sc.EncodeSize[1]
	} else if len(sc.Size) >= 2 {
		w, h = sc.Size[0], sc.Size[1]
	}
	fps := sc.Framerate
	if fps <= 0 {
		fps = 30
	}
	bitrate := hwEncodeBitrateKbps(sc)
	gop := hwKeyframeInterval(sc)
	bufCount := sc.BufferCount
	if bufCount <= 0 {
		bufCount = defaultHWRpicamBufferCount
	}
	return []string{
		rpicam, "-t", "0", "-n",
		"--camera", strconv.Itoa(libIdx),
		"--width", strconv.Itoa(w),
		"--height", strconv.Itoa(h),
		"--framerate", strconv.Itoa(fps),
		"--codec", "h264", "--inline", "--flush",
		"--libav-format", "h264",
		"--profile", "baseline",
		"-b", fmt.Sprintf("%dk", bitrate),
		"-g", strconv.Itoa(gop),
		"--buffer-count", strconv.Itoa(bufCount),
		"-o", "-",
	}
}

func startHWStreamer(stream config.CameraStreamConfig) (int, error) {
	sc, err := loadStreamerJSON(stream.CameraID)
	if err != nil {
		return 0, err
	}
	libIdx := libcameraIndexForCamera(stream.CameraID)
	rtspURL := rtspURLFromJSON(sc)

	ffmpegArgs := buildHWFFmpegCmd(rtspURL)
	ffmpegCmd := exec.Command(ffmpegArgs[0], ffmpegArgs[1:]...)
	ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	ffmpegCmd.Stdout = nil
	ffmpegCmd.Stderr = nil

	stdin, err := ffmpegCmd.StdinPipe()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg stdin pipe: %w", err)
	}

	if err := ffmpegCmd.Start(); err != nil {
		return 0, fmt.Errorf("start ffmpeg: %w", err)
	}

	pgid := ffmpegCmd.Process.Pid
	if g, gerr := syscall.Getpgid(ffmpegCmd.Process.Pid); gerr == nil {
		pgid = g
	}

	rpicamArgs := buildHWRpicamCmd(sc, libIdx)
	rpicamCmd := exec.Command(rpicamArgs[0], rpicamArgs[1:]...)
	rpicamCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	rpicamCmd.Stdout = stdin
	rpicamCmd.Stderr = nil

	if err := rpicamCmd.Start(); err != nil {
		_ = killProcessGroup(ffmpegCmd)
		return 0, fmt.Errorf("start rpicam-vid: %w", err)
	}
	_ = stdin.Close()

	streamerMu.Lock()
	streamerProcs = append(streamerProcs, &streamerProc{
		cameraID:   stream.CameraID,
		cmd:        ffmpegCmd,
		helperCmds: []*exec.Cmd{rpicamCmd},
		mode:       "hw",
	})
	streamerMu.Unlock()

	go waitHWStreamer(ffmpegCmd, rpicamCmd, stream.CameraID)

	pid := ffmpegCmd.Process.Pid
	w, h := 640, 480
	if len(sc.EncodeSize) >= 2 {
		w, h = sc.EncodeSize[0], sc.EncodeSize[1]
	} else if len(sc.Size) >= 2 {
		w, h = sc.Size[0], sc.Size[1]
	}
	fps := sc.Framerate
	if fps <= 0 {
		fps = 30
	}
	bufCount := sc.BufferCount
	if bufCount <= 0 {
		bufCount = defaultHWRpicamBufferCount
	}
	logger.Info("[CAMERA] HW streamer cam%d started (ffmpeg PID %d, rpicam PID %d, %dx%d @ %dfps, buffer=%d%s)",
		stream.CameraID, pid, rpicamCmd.Process.Pid, w, h, fps, bufCount, lowMemNote(sc))
	return pid, nil
}

func lowMemNote(sc streamerJSON) string {
	if sc.LowMemoryMode {
		return ", low_memory"
	}
	return ""
}

func startHWStreamers(cfg *config.Config, streams []config.CameraStreamConfig) ([]int, error) {
	multiCam := len(streams) > 1
	var started []int
	for i, stream := range streams {
		pid, err := startHWStreamer(stream)
		if err != nil {
			logger.Warn("[CAMERA] HW streamer cam%d failed: %v", stream.CameraID, err)
			continue
		}
		started = append(started, pid)
		if i < len(streams)-1 {
			delay := streamerStaggerFirst
			if multiCam && stream.CameraID == 0 {
				delay = streamerStaggerFollower
			}
			time.Sleep(delay)
		}
	}
	if len(started) == 0 {
		logger.Warn("[CAMERA] HW launch failed for all streams — falling back to Python")
		return startPythonStreamers(cfg, streams)
	}
	return started, nil
}

func waitHWStreamer(ffmpegCmd, rpicamCmd *exec.Cmd, camID int) {
	_ = rpicamCmd.Wait()
	waitErr := ffmpegCmd.Wait()

	streamerMu.Lock()
	defer streamerMu.Unlock()
	for i, sp := range streamerProcs {
		if sp != nil && sp.cmd == ffmpegCmd {
			streamerProcs = append(streamerProcs[:i], streamerProcs[i+1:]...)
			break
		}
	}
	if waitErr != nil {
		logger.Warn("[CAMERA] HW streamer cam%d exited: %v", camID, waitErr)
	} else {
		logger.Info("[CAMERA] HW streamer cam%d stopped", camID)
	}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

func stopTrackedStreamers() {
	streamerMu.Lock()
	procs := append([]*streamerProc(nil), streamerProcs...)
	streamerProcs = nil
	streamerMu.Unlock()

	for _, sp := range procs {
		if sp == nil {
			continue
		}
		for _, helper := range sp.helperCmds {
			_ = killProcessGroup(helper)
		}
		if sp.cmd != nil {
			_ = killProcessGroup(sp.cmd)
			_, _ = sp.cmd.Process.Wait()
		}
	}
}
