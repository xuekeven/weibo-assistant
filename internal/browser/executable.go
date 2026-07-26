package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func findExecutable(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}

	for _, candidate := range executableCandidates() {
		if filepath.IsAbs(candidate) {
			if isExecutableFile(candidate) {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", errors.New("Chrome/Chromium executable not found; set CHROME_EXECUTABLE in .env")
}

func executableCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"google-chrome",
			"chromium",
		}
	case "linux":
		return []string{
			"google-chrome-stable",
			"google-chrome",
			"chromium-browser",
			"chromium",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
		}
	default:
		return []string{
			"google-chrome",
			"chromium",
			"chrome",
			"msedge",
		}
	}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}
