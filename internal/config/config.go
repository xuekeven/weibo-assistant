package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv       string
	DatabaseURL  string
	BackendPort  int
	FrontendPort int

	WeiboChatURL     string
	WeiboProfileDir  string
	Headless         bool
	ChromeExecutable string
	ChromeNoSandbox  bool
	ChromeDebugPort  int

	TargetGroupName  string
	TargetGroupID    string
	TargetSenderName string
	TargetSenderID   string

	WeworkRelayURL    string
	WeworkRelayAPIKey string
	WeworkRelayToUser string

	MonitorPollInterval time.Duration
	HealthCheckInterval time.Duration
	PageReloadInterval  time.Duration

	DedupeTTL      time.Duration
	DedupeMaxItems int
	LogLevel       string
}

func Load(envPath string) (Config, error) {
	_ = godotenv.Load(envPath)

	cfg := Config{
		AppEnv:              getString("APP_ENV", "development"),
		DatabaseURL:         getString("DATABASE_URL", ""),
		BackendPort:         getInt("BACKEND_PORT", 6001),
		FrontendPort:        getInt("FRONTEND_PORT", 6002),
		WeiboChatURL:        getString("WEIBO_CHAT_URL", "https://api.weibo.com/chat#/chat"),
		WeiboProfileDir:     getString("WEIBO_PROFILE_DIR", ".weibo-profile"),
		Headless:            getBool("HEADLESS", true),
		ChromeExecutable:    getString("CHROME_EXECUTABLE", ""),
		ChromeNoSandbox:     getBool("CHROME_NO_SANDBOX", false),
		ChromeDebugPort:     getInt("CHROME_DEBUG_PORT", 9222),
		TargetGroupName:     getString("TARGET_GROUP_NAME", ""),
		TargetGroupID:       getString("TARGET_GROUP_ID", ""),
		TargetSenderName:    getString("TARGET_SENDER_NAME", ""),
		TargetSenderID:      getString("TARGET_SENDER_ID", ""),
		WeworkRelayURL:      strings.TrimRight(getString("WEWORK_RELAY_URL", "http://aliserver.tailed5977.ts.net:5001"), "/"),
		WeworkRelayAPIKey:   getString("WEWORK_RELAY_API_KEY", ""),
		WeworkRelayToUser:   getString("WEWORK_RELAY_TOUSER", ""),
		MonitorPollInterval: time.Duration(getInt("MONITOR_POLL_INTERVAL_MS", 1000)) * time.Millisecond,
		HealthCheckInterval: time.Duration(getInt("HEALTH_CHECK_INTERVAL_MS", 30000)) * time.Millisecond,
		PageReloadInterval:  time.Duration(getInt("PAGE_RELOAD_INTERVAL_MINUTES", 0)) * time.Minute,
		DedupeTTL:           time.Duration(getInt("DEDUPE_TTL_SECONDS", 86400)) * time.Second,
		DedupeMaxItems:      getInt("DEDUPE_MAX_ITEMS", 1000),
		LogLevel:            getString("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

func (c Config) ValidateDatabase() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateBrowser() error {
	var errs []error
	if c.WeiboProfileDir == "" {
		errs = append(errs, errors.New("WEIBO_PROFILE_DIR is required"))
	}
	if c.WeiboChatURL == "" {
		errs = append(errs, errors.New("WEIBO_CHAT_URL is required"))
	}
	if c.ChromeDebugPort <= 0 || c.ChromeDebugPort > 65535 {
		errs = append(errs, fmt.Errorf("CHROME_DEBUG_PORT must be between 1 and 65535"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateWework() error {
	var errs []error
	if c.WeworkRelayURL == "" {
		errs = append(errs, errors.New("WEWORK_RELAY_URL is required"))
	}
	if c.WeworkRelayAPIKey == "" {
		errs = append(errs, errors.New("WEWORK_RELAY_API_KEY is required"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateServer() error {
	var errs []error
	errs = append(errs, c.ValidateDatabase())
	errs = append(errs, c.ValidateBrowser())
	errs = append(errs, c.ValidateWework())
	if c.BackendPort <= 0 || c.BackendPort > 65535 {
		errs = append(errs, fmt.Errorf("BACKEND_PORT must be between 1 and 65535"))
	}
	if c.FrontendPort <= 0 || c.FrontendPort > 65535 {
		errs = append(errs, fmt.Errorf("FRONTEND_PORT must be between 1 and 65535"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateMonitor() error {
	return errors.Join(c.ValidateDatabase(), c.ValidateBrowser())
}

func getString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}
