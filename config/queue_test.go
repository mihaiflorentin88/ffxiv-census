package config

import "testing"

func TestNewConfig_QueueDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue == nil {
		t.Fatal("expected queue section to be present")
	}
	if cfg.Queue.ClaimBatchSize != 4 {
		t.Errorf("claim_batch_size = %d, want 4", cfg.Queue.ClaimBatchSize)
	}
	if cfg.Queue.MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", cfg.Queue.MaxAttempts)
	}
	if cfg.Queue.BackoffBaseSeconds != 5 {
		t.Errorf("backoff_base_seconds = %d, want 5", cfg.Queue.BackoffBaseSeconds)
	}
}

func TestQueueConfig_EnvOverride(t *testing.T) {
	t.Setenv("QUEUE_MAX_ATTEMPTS", "9")
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue.MaxAttempts != 9 {
		t.Errorf("QUEUE_MAX_ATTEMPTS override: got %d, want 9", cfg.Queue.MaxAttempts)
	}
}
