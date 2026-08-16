package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"00001_create_probe.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nCREATE TABLE probe (id INTEGER PRIMARY KEY, name TEXT);\n-- +goose Down\nDROP TABLE probe;\n"),
		},
	}
}

func testConfig(t *testing.T) *config.SQLiteConfig {
	t.Helper()
	return &config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}
}

func TestDriver_InitAppliesMigrations(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	row, rowErr := driver.FetchOne(context.Background(), "SELECT name FROM probe WHERE id = ?", 999)
	if rowErr != nil {
		t.Fatalf("FetchOne: %v", rowErr)
	}
	var name string
	err = row.Scan(&name)
	// Scan must surface "no rows" (table exists, query ran) — NOT "no such table".
	if err == nil || strings.Contains(err.Error(), "no such table") {
		t.Fatalf("probe table missing after init (migrations not applied?): %v", err)
	}
}

func TestDriver_MigrateDownRollsBack(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	if err := driver.MigrateDown(context.Background()); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	row, rowErr := driver.FetchOne(context.Background(), "SELECT COUNT(*) FROM probe")
	if rowErr != nil {
		t.Fatalf("FetchOne: %v", rowErr)
	}
	var n int
	err = row.Scan(&n)
	if err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected probe table dropped after MigrateDown, got: %v", err)
	}
}

func TestDriver_ExecuteRoundtrip(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	ctx := context.Background()
	if _, err := driver.Execute(ctx, "INSERT INTO probe (name) VALUES (?)", "alpha"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var name string
	row, rowErr := driver.FetchOne(ctx, "SELECT name FROM probe WHERE id = ?", 1)
	if rowErr != nil {
		t.Fatalf("FetchOne: %v", rowErr)
	}
	if err := row.Scan(&name); err != nil {
		t.Fatalf("select one: %v", err)
	}
	if name != "alpha" {
		t.Errorf("name = %q, want alpha", name)
	}
	rows, err := driver.FetchMany(ctx, "SELECT name FROM probe")
	if err != nil {
		t.Fatalf("select many: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestDriver_NilConfigFails(t *testing.T) {
	if _, err := NewDriver(nil, testFS()); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestDriver_RealEmbedFS(t *testing.T) {
	cfg := testConfig(t)
	driver, err := NewDriver(cfg, migration.FS())
	if err != nil {
		t.Fatalf("NewDriver with real migration.FS(): %v", err)
	}
	defer driver.Close()
	// If we get here, the embed path works
}
