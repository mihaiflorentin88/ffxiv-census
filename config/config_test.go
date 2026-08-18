package config

import (
	"os"
	"testing"
)

func TestNewConfig_Defaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}

	if cfg.App.Name != "ffxiv-census" {
		t.Errorf("expected App.Name 'ffxiv-census', got %q", cfg.App.Name)
	}
	if cfg.HTTP.Port != 8080 {
		t.Errorf("expected HTTP.Port 8080, got %d", cfg.HTTP.Port)
	}
	if cfg.SQLite.Path != "data/ffxiv-census.db" {
		t.Errorf("expected SQLite.Path 'data/ffxiv-census.db', got %q", cfg.SQLite.Path)
	}
	if cfg.Lodestone.RateLimit != 1.0 {
		t.Errorf("expected Lodestone.RateLimit 1.0, got %f", cfg.Lodestone.RateLimit)
	}
	if cfg.Queue.ClaimBatchSize != 4 {
		t.Errorf("expected Queue.ClaimBatchSize 4, got %d", cfg.Queue.ClaimBatchSize)
	}
	if cfg.Backup == nil {
		t.Fatalf("expected Backup config to not be nil")
	}
	if cfg.Backup.ServiceAccountB64 != "" {
		t.Errorf("expected Backup.ServiceAccountB64 '', got %q", cfg.Backup.ServiceAccountB64)
	}
	if cfg.Backup.GDriveFolderID != "" {
		t.Errorf("expected Backup.GDriveFolderID '', got %q", cfg.Backup.GDriveFolderID)
	}
}

func TestNewConfig_EnvOverrides(t *testing.T) {
	tests := []struct {
		name     string
		envKey   string
		envVal   string
		validate func(t *testing.T, cfg *Config)
	}{
		{
			name:   "sqlite path override",
			envKey: "SQLITE_PATH",
			envVal: "/tmp/custom.db",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.SQLite.Path != "/tmp/custom.db" {
					t.Errorf("expected SQLite.Path '/tmp/custom.db', got %q", cfg.SQLite.Path)
				}
			},
		},
		{
			name:   "http port override",
			envKey: "HTTP_PORT",
			envVal: "9090",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.HTTP.Port != 9090 {
					t.Errorf("expected HTTP.Port 9090, got %d", cfg.HTTP.Port)
				}
			},
		},
		{
			name:   "lodestone rate limit override",
			envKey: "LODESTONE_RATE_LIMIT",
			envVal: "5.5",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Lodestone.RateLimit != 5.5 {
					t.Errorf("expected Lodestone.RateLimit 5.5, got %f", cfg.Lodestone.RateLimit)
				}
			},
		},
		{
			name:   "queue backoff base seconds override",
			envKey: "QUEUE_BACKOFF_BASE_SECONDS",
			envVal: "15",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Queue.BackoffBaseSeconds != 15 {
					t.Errorf("expected Queue.BackoffBaseSeconds 15, got %d", cfg.Queue.BackoffBaseSeconds)
				}
			},
		},
		{
			name:   "app env override",
			envKey: "APP_ENV",
			envVal: "production",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.App.Env != "production" {
					t.Errorf("expected App.Env 'production', got %q", cfg.App.Env)
				}
			},
		},
		{
			name:   "backup service account b64 override",
			envKey: "BACKUP_SERVICE_ACCOUNT_B64",
			envVal: "b64string==",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Backup == nil || cfg.Backup.ServiceAccountB64 != "b64string==" {
					t.Errorf("expected Backup.ServiceAccountB64 'b64string==', got %+v", cfg.Backup)
				}
			},
		},
		{
			name:   "backup gdrive folder id override",
			envKey: "BACKUP_GDRIVE_FOLDER_ID",
			envVal: "folder123",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Backup == nil || cfg.Backup.GDriveFolderID != "folder123" {
					t.Errorf("expected Backup.GDriveFolderID 'folder123', got %+v", cfg.Backup)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVal, exists := os.LookupEnv(tt.envKey)
			os.Setenv(tt.envKey, tt.envVal)
			defer func() {
				if exists {
					os.Setenv(tt.envKey, oldVal)
				} else {
					os.Unsetenv(tt.envKey)
				}
			}()

			cfg, err := NewConfig()
			if err != nil {
				t.Fatalf("unexpected error loading config: %v", err)
			}
			tt.validate(t, cfg)
		})
	}
}
