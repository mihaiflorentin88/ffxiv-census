package cli

import (
	"testing"
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
