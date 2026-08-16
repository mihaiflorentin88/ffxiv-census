package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_CensusService(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	svc := Load.CensusService()
	if svc == nil {
		t.Fatal("CensusService nil")
	}
	if Load.CensusService() != svc {
		t.Fatal("expected cached CensusService instance")
	}
}
