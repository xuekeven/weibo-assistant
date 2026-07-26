package config

import (
	"strings"
	"testing"
	"time"
)

func TestCommandValidationIsScoped(t *testing.T) {
	server := Config{
		DatabaseURL: "postgres://example", BackendBindAddr: "0.0.0.0", BackendPort: 6001,
		FrontendPort: 6002, MonitorStaleAfter: 90 * time.Second,
	}
	if err := server.ValidateServer(); err != nil {
		t.Fatalf("server validation should not require browser or wework: %v", err)
	}

	frontend := Config{FrontendBindAddr: "0.0.0.0", FrontendPort: 6002, FrontendDistDir: "frontend/dist"}
	if err := frontend.ValidateFrontend(); err != nil {
		t.Fatalf("frontend validation failed: %v", err)
	}
}

func TestLoadRejectsMalformedTypedValues(t *testing.T) {
	t.Setenv("BACKEND_PORT", "six-thousand")
	t.Setenv("HEADLESS", "treu")

	_, err := Load("testdata/does-not-exist.env")
	if err == nil {
		t.Fatal("expected malformed environment variables to fail")
	}
	if !strings.Contains(err.Error(), "BACKEND_PORT") || !strings.Contains(err.Error(), "HEADLESS") {
		t.Fatalf("error should identify every malformed variable: %v", err)
	}
}

func TestValidateServerAcceptsTailscaleBindAddressAndOrigin(t *testing.T) {
	cfg := Config{
		DatabaseURL:       "postgres://example",
		BackendBindAddr:   "100.80.90.100",
		BackendPort:       6001,
		FrontendPort:      6002,
		MonitorStaleAfter: time.Minute,
		CORSAllowedOrigins: []string{
			"http://100.80.90.100:6002",
		},
	}
	if err := cfg.ValidateServer(); err != nil {
		t.Fatalf("tailscale server config should be valid: %v", err)
	}
}

func TestLoadHealthCheckIntervalUsesSeconds(t *testing.T) {
	t.Setenv("HEALTH_CHECK_INTERVAL_SECONDS", "12")

	cfg, err := Load("testdata/does-not-exist.env")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HealthCheckInterval != 12*time.Second {
		t.Fatalf("expected 12s health check interval, got %s", cfg.HealthCheckInterval)
	}
}
