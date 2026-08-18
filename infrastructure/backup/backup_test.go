package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestBackup_GDrive_MissingCredentials(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "source.db")

	driver, err := sqlite.NewDriver(&config.SQLiteConfig{Path: dbPath}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("create sqlite driver: %v", err)
	}

	svc := backup.NewService(driver, nil)
	cfg := &backup.Config{
		Target: "gdrive",
	}

	_, err = svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error with missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "no Google Drive credentials found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBackup_GDrive_InvalidBase64Credentials(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "source.db")

	driver, err := sqlite.NewDriver(&config.SQLiteConfig{Path: dbPath}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("create sqlite driver: %v", err)
	}

	svc := backup.NewService(driver, nil)
	cfg := &backup.Config{
		Target:            "gdrive",
		ServiceAccountB64: "invalid-not-base64!@#$",
	}

	_, err = svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error with invalid base64 credentials, got nil")
	}
	if !strings.Contains(err.Error(), "decode base64 service account") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBackup_GDrive_IgnoresRawEnvDirectly(t *testing.T) {
	// Ensure infrastructure does not read GDRIVE_SERVICE_ACCOUNT_B64 directly from os.Getenv
	t.Setenv("GDRIVE_SERVICE_ACCOUNT_B64", "invalid-b64")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "source.db")

	driver, err := sqlite.NewDriver(&config.SQLiteConfig{Path: dbPath}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("create sqlite driver: %v", err)
	}

	svc := backup.NewService(driver, nil)
	cfg := &backup.Config{
		Target: "gdrive",
	}

	_, err = svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// If it was reading GDRIVE_SERVICE_ACCOUNT_B64, it would say "decode base64 service account"
	// Because it's removed, it should say "no Google Drive credentials found"
	if !strings.Contains(err.Error(), "no Google Drive credentials found") {
		t.Errorf("expected 'no Google Drive credentials found', got: %v", err)
	}
}
