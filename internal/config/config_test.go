package config

import (
	"testing"
	"time"
)

func TestCommandValidationIsScoped(t *testing.T) {
	server := Config{DatabaseURL: "postgres://example", BackendPort: 6001, FrontendPort: 6002, MonitorStaleAfter: 90 * time.Second}
	if err := server.ValidateServer(); err != nil {
		t.Fatalf("server validation should not require browser or wework: %v", err)
	}

	frontend := Config{FrontendPort: 6002, FrontendDistDir: "frontend/dist"}
	if err := frontend.ValidateFrontend(); err != nil {
		t.Fatalf("frontend validation failed: %v", err)
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
