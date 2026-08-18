package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/backup"
)

func TestBackup_CleanOldBackups(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy old and new backup files
	oldFile := filepath.Join(tempDir, "ffxiv_census_backup_20200101_000000.sql.gz")
	newFile := filepath.Join(tempDir, "ffxiv_census_backup_20260818_000000.sql.gz")

	_ = os.WriteFile(oldFile, []byte("old"), 0644)
	_ = os.WriteFile(newFile, []byte("new"), 0644)

	// Set mtime for old file to 30 days ago
	oldTime := time.Now().AddDate(0, 0, -30)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	svc := backup.NewService(nil, "", nil)
	cfg := &backup.Config{
		Target:        "local",
		OutputDir:     tempDir,
		RetentionDays: 7,
	}

	// Test local retention cleaning
	_, _ = svc.PerformBackup(context.Background(), cfg)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("expected old backup to be deleted, but still exists")
	}
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("expected new backup to be retained, but was deleted")
	}
}

func TestBackup_GDrive_MissingCredentials(t *testing.T) {
	svc := backup.NewService(nil, "", nil)
	cfg := &backup.Config{
		Target: "gdrive",
	}

	_, err := svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error with missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "no Google Drive credentials configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBackup_GDrive_InvalidBase64Credentials(t *testing.T) {
	svc := backup.NewService(nil, "", nil)
	cfg := &backup.Config{
		Target:            "gdrive",
		ServiceAccountB64: "invalid-not-base64!@#$",
	}

	_, err := svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error with invalid base64 credentials, got nil")
	}
	if !strings.Contains(err.Error(), "decode base64 service account") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBackup_GDrive_OAuthCredentials(t *testing.T) {
	svc := backup.NewService(nil, "", nil)
	cfg := &backup.Config{
		Target:            "gdrive",
		OAuthClientID:     "mock-client-id",
		OAuthClientSecret: "mock-client-secret",
		OAuthRefreshToken: "mock-refresh-token",
	}

	// Should resolve credentials and attempt to create drive client / upload
	_, err := svc.PerformBackup(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error connecting to mock gdrive with dummy token, got nil")
	}
	// It should NOT say "no Google Drive credentials configured"
	if strings.Contains(err.Error(), "no Google Drive credentials configured") {
		t.Errorf("expected OAuth credentials to be recognized, got: %v", err)
	}
}
