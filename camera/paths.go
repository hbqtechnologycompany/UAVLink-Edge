package camera

import (
	"os"
	"path/filepath"
)

// FindLandingDir returns the directory containing camera_streamer.py.
func FindLandingDir() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "Find_landing"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "Find_landing"))
	}
	candidates = append(candidates, "/opt/dronebridge/Find_landing")

	for _, dir := range candidates {
		if st, err := os.Stat(filepath.Join(dir, "camera_streamer.py")); err == nil && !st.IsDir() {
			return dir
		}
	}
	return "Find_landing"
}
