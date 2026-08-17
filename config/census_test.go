package config

import "testing"

func TestCensusConfig_Defaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Census == nil || cfg.Census.ActivityWindowDays != 30 {
		t.Fatalf("activity_window_days = %v, want 30", cfg.Census)
	}
}

func TestCensusConfig_EnvOverride(t *testing.T) {
	t.Setenv("CENSUS_ACTIVITY_WINDOW_DAYS", "45")
	cfg, _ := NewConfig()
	if cfg.Census.ActivityWindowDays != 45 {
		t.Fatalf("CENSUS_ACTIVITY_WINDOW_DAYS override: got %v, want 45", cfg.Census.ActivityWindowDays)
	}
}
