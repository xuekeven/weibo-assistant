package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type LoginOptions struct {
	URL        string
	ProfileDir string
	Executable string
	NoSandbox  bool
}

func RunLogin(ctx context.Context, opts LoginOptions) error {
	if strings.TrimSpace(opts.URL) == "" {
		return errors.New("browser login URL is required")
	}
	if strings.TrimSpace(opts.ProfileDir) == "" {
		return errors.New("browser profile dir is required")
	}

	profileDir, err := filepath.Abs(opts.ProfileDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("create browser profile dir: %w", err)
	}

	executable, err := findExecutable(opts.Executable)
	if err != nil {
		return err
	}

	args := []string{
		"--user-data-dir=" + profileDir,
		"--profile-directory=Default",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		opts.URL,
	}
	if opts.NoSandbox {
		args = append([]string{"--no-sandbox"}, args...)
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("starting browser for weibo login", "executable", executable, "profileDir", profileDir, "url", opts.URL)
	slog.Info("login in the opened browser, then close the browser window to finish")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("browser exited with error: %w", err)
	}

	slog.Info("browser closed; login state should be saved in profile dir", "profileDir", profileDir)
	return nil
}

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
