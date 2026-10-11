package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	// 基础服务配置。
	AppEnv             string
	DatabaseURL        string
	BackendBindAddr    string
	BackendPort        int
	FrontendBindAddr   string
	FrontendPort       int
	FrontendDistDir    string
	CORSAllowedOrigins []string

	// Chromium 与微博页面配置。
	WeiboChatURL        string
	WeiboProfileDir     string
	Headless            bool
	ChromeExecutable    string
	ChromeNoSandbox     bool
	ChromeDebugPort     int
	RendererCPULimit    int
	RendererCPUChecks   int
	RendererCPUInterval time.Duration

	// 可选的采集范围。ID 比名称稳定，因此同时配置时优先使用 ID。
	TargetGroupName  string
	TargetGroupID    string
	TargetSenderName string
	TargetSenderID   string

	// 企业微信通知与 outbox worker 配置。
	WeworkRelayURL           string
	WeworkRelayAPIKey        string
	WeworkRelayApp           string
	WeworkRelayToUser        string
	NotificationPollInterval time.Duration
	NotificationMaxAttempts  int

	// 长期运行、健康检查及内存上限相关配置。
	HealthCheckInterval  time.Duration
	MonitorStaleAfter    time.Duration
	PageReloadInterval   time.Duration
	CPUPowerAlertWatts   int
	CPUTemperatureAlertC int
	ImageCacheTTL        time.Duration
	ImageCacheMaxItems   int

	LogLevel string
}

func Load(envPath string) (Config, error) {
	// godotenv 不会覆盖进程中已经存在的环境变量，部署平台传入的值仍然优先。
	_ = godotenv.Load(envPath)

	var loadErrs []error
	readInt := func(key string, fallback int) int {
		value, err := getInt(key, fallback)
		if err != nil {
			loadErrs = append(loadErrs, err)
		}
		return value
	}
	readBool := func(key string, fallback bool) bool {
		value, err := getBool(key, fallback)
		if err != nil {
			loadErrs = append(loadErrs, err)
		}
		return value
	}

	cfg := Config{
		AppEnv:                   getString("APP_ENV", "development"),
		DatabaseURL:              getString("DATABASE_URL", ""),
		BackendBindAddr:          getString("BACKEND_BIND_ADDRESS", "0.0.0.0"),
		BackendPort:              readInt("BACKEND_PORT", 6001),
		FrontendBindAddr:         getString("FRONTEND_BIND_ADDRESS", "0.0.0.0"),
		FrontendPort:             readInt("FRONTEND_PORT", 6002),
		FrontendDistDir:          getString("FRONTEND_DIST_DIR", "frontend/dist"),
		CORSAllowedOrigins:       getStringList("CORS_ALLOWED_ORIGINS"),
		WeiboChatURL:             getString("WEIBO_CHAT_URL", "https://api.weibo.com/chat#/chat"),
		WeiboProfileDir:          getString("WEIBO_PROFILE_DIR", ".weibo-profile"),
		Headless:                 readBool("HEADLESS", true),
		ChromeExecutable:         getString("CHROME_EXECUTABLE", ""),
		ChromeNoSandbox:          readBool("CHROME_NO_SANDBOX", false),
		ChromeDebugPort:          readInt("CHROME_DEBUG_PORT", 9222),
		RendererCPULimit:         readInt("CHROME_RENDERER_CPU_LIMIT_PERCENT", 50),
		RendererCPUChecks:        readInt("CHROME_RENDERER_CPU_CONSECUTIVE_CHECKS", 3),
		RendererCPUInterval:      time.Duration(readInt("CHROME_RENDERER_CPU_CHECK_INTERVAL_SECONDS", 60)) * time.Second,
		TargetGroupName:          getString("TARGET_GROUP_NAME", ""),
		TargetGroupID:            getString("TARGET_GROUP_ID", ""),
		TargetSenderName:         getString("TARGET_SENDER_NAME", ""),
		TargetSenderID:           getString("TARGET_SENDER_ID", ""),
		WeworkRelayURL:           strings.TrimRight(getString("WEWORK_RELAY_URL", "http://aliserver.tailed5977.ts.net:5001"), "/"),
		WeworkRelayAPIKey:        getString("WEWORK_RELAY_API_KEY", ""),
		WeworkRelayApp:           getString("WEWORK_RELAY_APP", ""),
		WeworkRelayToUser:        getString("WEWORK_RELAY_TOUSER", ""),
		NotificationPollInterval: time.Duration(readInt("NOTIFICATION_POLL_INTERVAL_MS", 1000)) * time.Millisecond,
		NotificationMaxAttempts:  readInt("NOTIFICATION_MAX_ATTEMPTS", 8),
		HealthCheckInterval:      time.Duration(readInt("HEALTH_CHECK_INTERVAL_SECONDS", 60)) * time.Second,
		MonitorStaleAfter:        time.Duration(readInt("MONITOR_STALE_AFTER_SECONDS", 180)) * time.Second,
		PageReloadInterval:       time.Duration(readInt("PAGE_RELOAD_INTERVAL_MINUTES", 360)) * time.Minute,
		CPUPowerAlertWatts:       readInt("CPU_POWER_ALERT_WATTS", 25),
		CPUTemperatureAlertC:     readInt("CPU_TEMPERATURE_ALERT_CELSIUS", 75),
		ImageCacheTTL:            time.Duration(readInt("IMAGE_CACHE_TTL_SECONDS", 86400)) * time.Second,
		ImageCacheMaxItems:       readInt("IMAGE_CACHE_MAX_ITEMS", 1000),
		LogLevel:                 getString("LOG_LEVEL", "info"),
	}

	return cfg, errors.Join(loadErrs...)
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
	if c.RendererCPULimit <= 0 {
		errs = append(errs, errors.New("CHROME_RENDERER_CPU_LIMIT_PERCENT must be greater than 0"))
	}
	if c.RendererCPUChecks <= 0 {
		errs = append(errs, errors.New("CHROME_RENDERER_CPU_CONSECUTIVE_CHECKS must be greater than 0"))
	}
	if c.RendererCPUInterval <= 0 {
		errs = append(errs, errors.New("CHROME_RENDERER_CPU_CHECK_INTERVAL_SECONDS must be greater than 0"))
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
	if net.ParseIP(c.BackendBindAddr) == nil {
		errs = append(errs, errors.New("BACKEND_BIND_ADDRESS must be an IP address"))
	}
	if c.BackendPort <= 0 || c.BackendPort > 65535 {
		errs = append(errs, fmt.Errorf("BACKEND_PORT must be between 1 and 65535"))
	}
	if c.FrontendPort <= 0 || c.FrontendPort > 65535 {
		errs = append(errs, fmt.Errorf("FRONTEND_PORT must be between 1 and 65535"))
	}
	if c.MonitorStaleAfter <= 0 {
		errs = append(errs, errors.New("MONITOR_STALE_AFTER_SECONDS must be greater than 0"))
	}
	errs = append(errs, validateOrigins(c.CORSAllowedOrigins)...)
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
	if net.ParseIP(c.FrontendBindAddr) == nil {
		errs = append(errs, errors.New("FRONTEND_BIND_ADDRESS must be an IP address"))
	}
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

func getStringList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimRight(strings.TrimSpace(value), "/"); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func getInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return value, nil
}

func getBool(key string, fallback bool) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return fallback, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}
}

func validateOrigins(origins []string) []error {
	var errs []error
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			errs = append(errs, fmt.Errorf("invalid CORS_ALLOWED_ORIGINS entry %q; expected scheme://host:port", origin))
		}
	}
	return errs
}
