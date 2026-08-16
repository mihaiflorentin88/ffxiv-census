package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_SQLiteDriver(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	driver := Load.SQLite()
	if driver == nil {
		t.Fatal("expected non-nil sqlite driver")
	}
	defer driver.Close()
}

func TestServiceContainer_SQLiteDriverCached(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	first := Load.SQLite()
	second := Load.SQLite()
	if first != second {
		t.Fatal("expected cached driver instance")
	}
}
