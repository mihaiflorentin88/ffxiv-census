package cli

import "testing"

func TestMigrateCmdRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "migrate" {
			found = true
		}
	}
	if !found {
		t.Fatal("migrate command not registered on root")
	}
}

func TestRunMigrateInvalidDirection(t *testing.T) {
	if err := runMigrate("sideways"); err == nil {
		t.Fatal("expected error for invalid direction")
	}
}
