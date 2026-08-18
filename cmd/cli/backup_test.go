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

	clientIDFlag := cmd.Flags().Lookup("oauth-client-id")
	if clientIDFlag == nil {
		t.Fatal("expected --oauth-client-id flag")
	}

	clientSecretFlag := cmd.Flags().Lookup("oauth-client-secret")
	if clientSecretFlag == nil {
		t.Fatal("expected --oauth-client-secret flag")
	}

	refreshTokenFlag := cmd.Flags().Lookup("oauth-refresh-token")
	if refreshTokenFlag == nil {
		t.Fatal("expected --oauth-refresh-token flag")
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
	if cfg.OAuthClientID != "" {
		t.Errorf("expected OAuthClientID empty, got %q", cfg.OAuthClientID)
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

func TestBuildBackupConfig_OAuthFallback(t *testing.T) {
	cmd := newBackupCmd()
	sysCfg := &config.Config{
		Backup: &config.BackupConfig{
			OAuthClientID:     "cfg-client-id",
			OAuthClientSecret: "cfg-client-secret",
			OAuthRefreshToken: "cfg-refresh-token",
		},
	}

	cfg := buildBackupConfig(cmd, sysCfg)

	if cfg.OAuthClientID != "cfg-client-id" {
		t.Errorf("expected OAuthClientID %q, got %q", "cfg-client-id", cfg.OAuthClientID)
	}
	if cfg.OAuthClientSecret != "cfg-client-secret" {
		t.Errorf("expected OAuthClientSecret %q, got %q", "cfg-client-secret", cfg.OAuthClientSecret)
	}
	if cfg.OAuthRefreshToken != "cfg-refresh-token" {
		t.Errorf("expected OAuthRefreshToken %q, got %q", "cfg-refresh-token", cfg.OAuthRefreshToken)
	}
}

func TestBuildBackupConfig_OAuthFlagOverrides(t *testing.T) {
	cmd := newBackupCmd()
	_ = cmd.Flags().Set("oauth-client-id", "flag-client-id")
	_ = cmd.Flags().Set("oauth-client-secret", "flag-client-secret")
	_ = cmd.Flags().Set("oauth-refresh-token", "flag-refresh-token")

	sysCfg := &config.Config{
		Backup: &config.BackupConfig{
			OAuthClientID:     "cfg-client-id",
			OAuthClientSecret: "cfg-client-secret",
			OAuthRefreshToken: "cfg-refresh-token",
		},
	}

	cfg := buildBackupConfig(cmd, sysCfg)

	if cfg.OAuthClientID != "flag-client-id" {
		t.Errorf("expected OAuthClientID %q, got %q", "flag-client-id", cfg.OAuthClientID)
	}
	if cfg.OAuthClientSecret != "flag-client-secret" {
		t.Errorf("expected OAuthClientSecret %q, got %q", "flag-client-secret", cfg.OAuthClientSecret)
	}
	if cfg.OAuthRefreshToken != "flag-refresh-token" {
		t.Errorf("expected OAuthRefreshToken %q, got %q", "flag-refresh-token", cfg.OAuthRefreshToken)
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
