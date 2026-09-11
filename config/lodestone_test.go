package config

import "testing"

func TestNewConfig_LodestoneDefaults(t *testing.T) {
	cfg, _ := NewConfig()
	if cfg.Lodestone == nil {
		t.Fatal("expected lodestone section")
	}
	if cfg.Lodestone.RateLimit != 1.0 {
		t.Errorf("rate_limit = %v, want 1.0", cfg.Lodestone.RateLimit)
	}
	if cfg.Lodestone.MaxRetries != 1 {
		t.Errorf("max_retries = %d, want 1 (2 attempts; deeper retries are the queue ladder's job)", cfg.Lodestone.MaxRetries)
	}
}

func TestLodestoneConfig_EnvOverride(t *testing.T) {
	t.Setenv("LODESTONE_RATE_LIMIT", "2.5")
	cfg, _ := NewConfig()
	if cfg.Lodestone.RateLimit != 2.5 {
		t.Errorf("LODESTONE_RATE_LIMIT override: got %v, want 2.5", cfg.Lodestone.RateLimit)
	}
}
