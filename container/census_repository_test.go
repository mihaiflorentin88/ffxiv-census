package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_CensusRepositories(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	if Load.CharacterRepository() == nil {
		t.Fatal("CharacterRepository nil")
	}
	if Load.FreeCompanyRepository() == nil {
		t.Fatal("FreeCompanyRepository nil")
	}
	if Load.AchievementRepository() == nil {
		t.Fatal("AchievementRepository nil")
	}
	if Load.CensusRunRepository() == nil {
		t.Fatal("CensusRunRepository nil")
	}
}
