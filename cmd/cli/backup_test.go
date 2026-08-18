package cli

import (
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/config"
)

func TestBackupCmd_Flags(t *testing.T) {
	cmd := backupCmd

	if cmd.Use != "backup" {
		t.Fatalf("unexpected Use: %s", cmd.Use)
	}

	targetFlag := cmd.Flags().Lookup("target")
	if targetFlag == nil || targetFlag.DefValue != "local" {
		t.Fatalf("expected --target default 'local', got %v", targetFlag)
	}

	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag == nil || outputFlag.DefValue != "./backups" {
		t.Fatalf("expected --output default './backups', got %v", outputFlag)
	}

	gdriveFolderFlag := cmd.Flags().Lookup("gdrive-folder-id")
	if gdriveFolderFlag == nil {
		t.Fatal("expected --gdrive-folder-id flag")
	}

	saFileFlag := cmd.Flags().Lookup("service-account-file")
	if saFileFlag == nil {
		t.Fatal("expected --service-account-file flag")
	}

	saB64Flag := cmd.Flags().Lookup("service-account-b64")
	if saB64Flag == nil {
		t.Fatal("expected --service-account-b64 flag")
	}
}

func TestBuildBackupConfig_DefaultsFromConfig(t *testing.T) {
	cmd := newBackupCmd()
	sysCfg := &config.Config{
		Backup: &config.BackupConfig{
			ServiceAccountB64: "config-sa-b64",
			GDriveFolderID:    "config-folder-id",
		},
	}

	cfg := buildBackupConfig(cmd, sysCfg)

	if cfg.GDriveFolderID != "config-folder-id" {
		t.Errorf("expected GDriveFolderID %q, got %q", "config-folder-id", cfg.GDriveFolderID)
	}
	if cfg.ServiceAccountB64 != "config-sa-b64" {
		t.Errorf("expected ServiceAccountB64 %q, got %q", "config-sa-b64", cfg.ServiceAccountB64)
	}
}

func TestBuildBackupConfig_FlagOverridesConfig(t *testing.T) {
	cmd := newBackupCmd()
	_ = cmd.Flags().Set("gdrive-folder-id", "flag-folder-id")
	_ = cmd.Flags().Set("service-account-b64", "flag-sa-b64")

	sysCfg := &config.Config{
		Backup: &config.BackupConfig{
			ServiceAccountB64: "config-sa-b64",
			GDriveFolderID:    "config-folder-id",
		},
	}

	cfg := buildBackupConfig(cmd, sysCfg)

	if cfg.GDriveFolderID != "flag-folder-id" {
		t.Errorf("expected GDriveFolderID %q, got %q", "flag-folder-id", cfg.GDriveFolderID)
	}
	if cfg.ServiceAccountB64 != "flag-sa-b64" {
		t.Errorf("expected ServiceAccountB64 %q, got %q", "flag-sa-b64", cfg.ServiceAccountB64)
	}
}

func TestBuildBackupConfig_NilConfig(t *testing.T) {
	cmd := newBackupCmd()
	_ = cmd.Flags().Set("gdrive-folder-id", "flag-folder-id")

	cfg := buildBackupConfig(cmd, nil)

	if cfg.GDriveFolderID != "flag-folder-id" {
		t.Errorf("expected GDriveFolderID %q, got %q", "flag-folder-id", cfg.GDriveFolderID)
	}
	if cfg.ServiceAccountB64 != "" {
		t.Errorf("expected ServiceAccountB64 empty, got %q", cfg.ServiceAccountB64)
	}
}
