package config

import (
	"testing"
)

func TestNewConfig_PostgresDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Postgres == nil {
		t.Fatal("expected postgres section to be present")
	}
	if cfg.Postgres.Host != "localhost" {
		t.Errorf("host = %q, want localhost", cfg.Postgres.Host)
	}
	if cfg.Postgres.Port != 5432 {
		t.Errorf("port = %d, want 5432", cfg.Postgres.Port)
	}
	if cfg.Postgres.User != "census" {
		t.Errorf("user = %q, want census", cfg.Postgres.User)
	}
	if cfg.Postgres.Database != "ffxiv_census" {
		t.Errorf("database = %q, want ffxiv_census", cfg.Postgres.Database)
	}
	if cfg.Postgres.MaxOpenConns != 10 {
		t.Errorf("max_open_conns = %d, want 10", cfg.Postgres.MaxOpenConns)
	}
	if cfg.Postgres.MaxIdleConns != 5 {
		t.Errorf("max_idle_conns = %d, want 5", cfg.Postgres.MaxIdleConns)
	}
}

func TestPostgresConfig_EnvOverride(t *testing.T) {
	dsn := "postgres://user:pass@remote:5432/mydb?sslmode=require"
	t.Setenv("POSTGRES_DSN", dsn)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Postgres.DSN != dsn {
		t.Errorf("POSTGRES_DSN override: got %q, want %q", cfg.Postgres.DSN, dsn)
	}
}
