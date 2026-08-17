package config

import "testing"

func TestNewConfig_TomestoneDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Tomestone == nil {
		t.Fatal("expected tomestone section")
	}
	if cfg.Tomestone.APIToken != "" {
		t.Errorf("api_token = %q, want empty", cfg.Tomestone.APIToken)
	}
	if cfg.Tomestone.BaseURL != "https://tomestone.gg" {
		t.Errorf("base_url = %q, want https://tomestone.gg", cfg.Tomestone.BaseURL)
	}
	if cfg.Tomestone.RateLimit != 10.0 {
		t.Errorf("rate_limit = %v, want 10.0", cfg.Tomestone.RateLimit)
	}
	if cfg.Tomestone.Timeout != "10s" {
		t.Errorf("timeout = %q, want 10s", cfg.Tomestone.Timeout)
	}
}

func TestTomestoneConfig_EnvOverride(t *testing.T) {
	t.Setenv("TOMESTONE_API_TOKEN", "secret-token")
	t.Setenv("TOMESTONE_BASE_URL", "https://custom.tomestone.gg")
	t.Setenv("TOMESTONE_RATE_LIMIT", "20.5")
	t.Setenv("TOMESTONE_TIMEOUT", "30s")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Tomestone.APIToken != "secret-token" {
		t.Errorf("TOMESTONE_API_TOKEN override: got %q, want secret-token", cfg.Tomestone.APIToken)
	}
	if cfg.Tomestone.BaseURL != "https://custom.tomestone.gg" {
		t.Errorf("TOMESTONE_BASE_URL override: got %q, want https://custom.tomestone.gg", cfg.Tomestone.BaseURL)
	}
	if cfg.Tomestone.RateLimit != 20.5 {
		t.Errorf("TOMESTONE_RATE_LIMIT override: got %v, want 20.5", cfg.Tomestone.RateLimit)
	}
	if cfg.Tomestone.Timeout != "30s" {
		t.Errorf("TOMESTONE_TIMEOUT override: got %q, want 30s", cfg.Tomestone.Timeout)
	}
}
