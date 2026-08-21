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
	if cfg.Postgres.User != "census" {
		t.Errorf("expected Postgres.User 'census', got %q", cfg.Postgres.User)
	}
	if cfg.Postgres.Database != "ffxiv_census" {
		t.Errorf("expected Postgres.Database 'ffxiv_census', got %q", cfg.Postgres.Database)
	}
	if cfg.Lodestone.RateLimit != 1.0 {
		t.Errorf("expected Lodestone.RateLimit 1.0, got %f", cfg.Lodestone.RateLimit)
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
			name:   "postgres dsn override",
			envKey: "POSTGRES_DSN",
			envVal: "postgres://custom:pass@localhost:5432/custom_db",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Postgres.DSN != "postgres://custom:pass@localhost:5432/custom_db" {
					t.Errorf("expected Postgres.DSN 'postgres://custom:pass@localhost:5432/custom_db', got %q", cfg.Postgres.DSN)
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
			name:   "app env override",
			envKey: "APP_ENV",
			envVal: "production",
			validate: func(t *testing.T, cfg *Config) {
				if cfg.App.Env != "production" {
					t.Errorf("expected App.Env 'production', got %q", cfg.App.Env)
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
