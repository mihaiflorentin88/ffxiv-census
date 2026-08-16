package repository

import (
	"path/filepath"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// newTestDriver returns a SQLite driver backed by a temp-file DB with the real
// embedded migrations applied, plus a cleanup func.
func newTestDriver(t *testing.T) (contract.SQLiteDriver, func()) {
	t.Helper()
	cfg := &config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}
	driver, err := sqlite.NewDriver(cfg, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	return driver, func() { _ = driver.Close() }
}

func newTestCharacterRepo(t *testing.T) contract.CharacterRepository {
	t.Helper()
	driver, cleanup := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewCharacterRepository(driver)
}
