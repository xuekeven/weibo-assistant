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
	// 基础服务配置。
	AppEnv          string
	DatabaseURL     string
	BackendPort     int
	FrontendPort    int
	FrontendDistDir string

	// Chromium 与微博页面配置。
	WeiboChatURL     string
	WeiboProfileDir  string
	Headless         bool
	ChromeExecutable string
	ChromeNoSandbox  bool
	ChromeDebugPort  int

	// 可选的采集范围。ID 比名称稳定，因此同时配置时优先使用 ID。
	TargetGroupName  string
	TargetGroupID    string
	TargetSenderName string
	TargetSenderID   string

	// 企业微信通知与 outbox worker 配置。
	WeworkRelayURL           string
	WeworkRelayAPIKey        string
	WeworkRelayToUser        string
	NotificationPollInterval time.Duration
	NotificationMaxAttempts  int

	// 长期运行、健康检查及内存上限相关配置。
	HealthCheckInterval time.Duration
	MonitorStaleAfter   time.Duration
	PageReloadInterval  time.Duration
	ImageCacheTTL       time.Duration
	ImageCacheMaxItems  int

	LogLevel string
}

func Load(envPath string) (Config, error) {
	// godotenv 不会覆盖进程中已经存在的环境变量，部署平台传入的值仍然优先。
	_ = godotenv.Load(envPath)

	cfg := Config{
		AppEnv:                   getString("APP_ENV", "development"),
		DatabaseURL:              getString("DATABASE_URL", ""),
		BackendPort:              getInt("BACKEND_PORT", 6001),
		FrontendPort:             getInt("FRONTEND_PORT", 6002),
		FrontendDistDir:          getString("FRONTEND_DIST_DIR", "frontend/dist"),
		WeiboChatURL:             getString("WEIBO_CHAT_URL", "https://api.weibo.com/chat#/chat"),
		WeiboProfileDir:          getString("WEIBO_PROFILE_DIR", ".weibo-profile"),
		Headless:                 getBool("HEADLESS", true),
		ChromeExecutable:         getString("CHROME_EXECUTABLE", ""),
		ChromeNoSandbox:          getBool("CHROME_NO_SANDBOX", false),
		ChromeDebugPort:          getInt("CHROME_DEBUG_PORT", 9222),
		TargetGroupName:          getString("TARGET_GROUP_NAME", ""),
		TargetGroupID:            getString("TARGET_GROUP_ID", ""),
		TargetSenderName:         getString("TARGET_SENDER_NAME", ""),
		TargetSenderID:           getString("TARGET_SENDER_ID", ""),
		WeworkRelayURL:           strings.TrimRight(getString("WEWORK_RELAY_URL", "http://aliserver.tailed5977.ts.net:5001"), "/"),
		WeworkRelayAPIKey:        getString("WEWORK_RELAY_API_KEY", ""),
		WeworkRelayToUser:        getString("WEWORK_RELAY_TOUSER", ""),
		NotificationPollInterval: time.Duration(getInt("NOTIFICATION_POLL_INTERVAL_MS", 1000)) * time.Millisecond,
		NotificationMaxAttempts:  getInt("NOTIFICATION_MAX_ATTEMPTS", 8),
		HealthCheckInterval:      time.Duration(getInt("HEALTH_CHECK_INTERVAL_SECONDS", 60)) * time.Second,
		MonitorStaleAfter:        time.Duration(getInt("MONITOR_STALE_AFTER_SECONDS", 180)) * time.Second,
		PageReloadInterval:       time.Duration(getInt("PAGE_RELOAD_INTERVAL_MINUTES", 0)) * time.Minute,
		ImageCacheTTL:            time.Duration(getInt("IMAGE_CACHE_TTL_SECONDS", 86400)) * time.Second,
		ImageCacheMaxItems:       getInt("IMAGE_CACHE_MAX_ITEMS", 1000),
		LogLevel:                 getString("LOG_LEVEL", "info"),
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
	if c.BackendPort <= 0 || c.BackendPort > 65535 {
		errs = append(errs, fmt.Errorf("BACKEND_PORT must be between 1 and 65535"))
	}
	if c.FrontendPort <= 0 || c.FrontendPort > 65535 {
		errs = append(errs, fmt.Errorf("FRONTEND_PORT must be between 1 and 65535"))
	}
	if c.MonitorStaleAfter <= 0 {
		errs = append(errs, errors.New("MONITOR_STALE_AFTER_SECONDS must be greater than 0"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateMonitor() error {
	var errs []error
	errs = append(errs, c.ValidateDatabase(), c.ValidateBrowser())
	if c.HealthCheckInterval <= 0 {
		errs = append(errs, errors.New("HEALTH_CHECK_INTERVAL_SECONDS must be greater than 0"))
	}
	if c.MonitorStaleAfter <= 0 {
		errs = append(errs, errors.New("MONITOR_STALE_AFTER_SECONDS must be greater than 0"))
	}
	if c.NotificationPollInterval <= 0 {
		errs = append(errs, errors.New("NOTIFICATION_POLL_INTERVAL_MS must be greater than 0"))
	}
	if c.NotificationMaxAttempts <= 0 {
		errs = append(errs, errors.New("NOTIFICATION_MAX_ATTEMPTS must be greater than 0"))
	}
	if c.ImageCacheTTL <= 0 || c.ImageCacheMaxItems <= 0 {
		errs = append(errs, errors.New("image cache TTL and max items must be greater than 0"))
	}
	return errors.Join(errs...)
}

func (c Config) ValidateFrontend() error {
	var errs []error
	if c.FrontendPort <= 0 || c.FrontendPort > 65535 {
		errs = append(errs, fmt.Errorf("FRONTEND_PORT must be between 1 and 65535"))
	}
	if strings.TrimSpace(c.FrontendDistDir) == "" {
		errs = append(errs, errors.New("FRONTEND_DIST_DIR is required"))
	}
	return errors.Join(errs...)
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
