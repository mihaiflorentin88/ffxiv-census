package container

import (
	"testing"
)

func TestServiceContainer_CensusRepositories(t *testing.T) {
	Load = NewServiceContainer()
	if Load.Database() == nil {
		t.Skip("postgres not available")
	}

	if Load.CharacterRepository() == nil {
		t.Fatal("CharacterRepository nil")
	}
	if Load.AchievementRepository() == nil {
		t.Fatal("AchievementRepository nil")
	}
	if Load.CensusRunRepository() == nil {
		t.Fatal("CensusRunRepository nil")
	}
}
