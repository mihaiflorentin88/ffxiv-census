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
	if cfg.Lodestone.MaxRetries != 3 {
		t.Errorf("max_retries = %d, want 3", cfg.Lodestone.MaxRetries)
	}
}

func TestLodestoneConfig_EnvOverride(t *testing.T) {
	t.Setenv("LODESTONE_RATE_LIMIT", "2.5")
	cfg, _ := NewConfig()
	if cfg.Lodestone.RateLimit != 2.5 {
		t.Errorf("LODESTONE_RATE_LIMIT override: got %v, want 2.5", cfg.Lodestone.RateLimit)
	}
}
