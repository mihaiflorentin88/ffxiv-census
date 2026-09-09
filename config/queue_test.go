package config

import (
	"testing"
)

func TestQueueConfig_DefaultMaxAttempts(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue == nil {
		t.Fatal("expected [queue] section to be present")
	}
	if cfg.Queue.MaxAttempts != 50 {
		t.Fatalf("default queue.max_attempts = %d, want 50", cfg.Queue.MaxAttempts)
	}
}

func TestQueueConfig_EnvOverride(t *testing.T) {
	t.Setenv("QUEUE_MAX_ATTEMPTS", "9")
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue == nil || cfg.Queue.MaxAttempts != 9 {
		t.Fatalf("QUEUE_MAX_ATTEMPTS override: got %+v, want max_attempts=9", cfg.Queue)
	}
}
