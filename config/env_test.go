package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv_LoadsUnsetVariables(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	content := []byte("TEST_LOAD_DOTENV_KEY=hello_world\nTEST_QUOTED_KEY=\"quoted_val\"\n# Comment line\nINVALID_LINE\n")
	if err := os.WriteFile(envPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Change cwd temporarily
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	loadDotEnv()

	if val := os.Getenv("TEST_LOAD_DOTENV_KEY"); val != "hello_world" {
		t.Errorf("TEST_LOAD_DOTENV_KEY = %q, want hello_world", val)
	}
	if val := os.Getenv("TEST_QUOTED_KEY"); val != "quoted_val" {
		t.Errorf("TEST_QUOTED_KEY = %q, want quoted_val", val)
	}
}

func TestLoadDotEnv_DoesNotOverwriteExisting(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	content := []byte("TEST_EXISTING_KEY=new_val\n")
	if err := os.WriteFile(envPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("TEST_EXISTING_KEY", "original_val")

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	loadDotEnv()

	if val := os.Getenv("TEST_EXISTING_KEY"); val != "original_val" {
		t.Errorf("TEST_EXISTING_KEY = %q, want original_val", val)
	}
}
