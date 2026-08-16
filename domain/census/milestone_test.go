package census

import (
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestMilestoneSet_HasChocobo(t *testing.T) {
	found := false
	for _, m := range MilestoneSet {
		if m.AchievementID == 590 && m.Kind == contract.MilestoneKindChocobo {
			found = true
		}
	}
	if !found {
		t.Fatal("MilestoneSet must contain chocobo achievement 590")
	}
}

func TestMilestoneSet_KindsValid(t *testing.T) {
	for _, m := range MilestoneSet {
		switch m.Kind {
		case contract.MilestoneKindExpansion, contract.MilestoneKindJobLevel, contract.MilestoneKindChocobo:
		default:
			t.Errorf("milestone %d has invalid kind %q", m.AchievementID, m.Kind)
		}
		if m.Kind == contract.MilestoneKindExpansion && m.Expansion == nil {
			t.Errorf("expansion milestone %d missing expansion label", m.AchievementID)
		}
	}
}

func TestMilestoneSet_NoDuplicateIDs(t *testing.T) {
	seen := map[uint32]bool{}
	for _, m := range MilestoneSet {
		if seen[m.AchievementID] {
			t.Errorf("duplicate milestone ID %d", m.AchievementID)
		}
		seen[m.AchievementID] = true
	}
}
