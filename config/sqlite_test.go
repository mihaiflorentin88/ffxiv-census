package config

import (
	"path/filepath"
	"testing"
)

func TestNewConfig_SQLiteDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.SQLite == nil {
		t.Fatal("expected sqlite section to be present")
	}
	if cfg.SQLite.Path != "data/ffxiv-census.db" {
		t.Errorf("path = %q, want data/ffxiv-census.db", cfg.SQLite.Path)
	}
	if cfg.SQLite.JournalMode != "WAL" {
		t.Errorf("journal_mode = %q, want WAL", cfg.SQLite.JournalMode)
	}
	if cfg.SQLite.MaxOpenConns != 4 {
		t.Errorf("max_open_conns = %d, want 4", cfg.SQLite.MaxOpenConns)
	}
}

func TestSQLiteConfig_EnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("SQLITE_PATH", path)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.SQLite.Path != path {
		t.Errorf("SQLITE_PATH override: got %q, want %q", cfg.SQLite.Path, path)
	}
}
