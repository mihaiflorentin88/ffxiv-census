package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/backup"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
)

func TestBackup_LocalSnapshotCreation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "source.db")

	driver, err := sqlite.NewDriver(&config.SQLiteConfig{Path: dbPath}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("create sqlite driver: %v", err)
	}

	// Insert test data
	db, err := driver.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire db: %v", err)
	}
	_, err = db.Exec("CREATE TABLE test_data (id INT, val TEXT); INSERT INTO test_data VALUES (1, 'hello')")
	if err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	backupDir := filepath.Join(tempDir, "backups")
	svc := backup.NewService(driver, nil)

	cfg := &backup.Config{
		Target:    "local",
		OutputDir: backupDir,
	}

	backupPath, err := svc.PerformBackup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("perform backup: %v", err)
	}

	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file does not exist at %s: %v", backupPath, err)
	}

	// Verify backup DB is valid SQLite and contains test data
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer backupDB.Close()

	var val string
	err = backupDB.QueryRow("SELECT val FROM test_data WHERE id = 1").Scan(&val)
	if err != nil {
		t.Fatalf("query backup db: %v", err)
	}
	if val != "hello" {
		t.Fatalf("expected val='hello', got %s", val)
	}
}
