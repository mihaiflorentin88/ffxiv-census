package contract

import "testing"

func TestMilestoneKindConstants(t *testing.T) {
	if MilestoneKindExpansion != "expansion_msq" {
		t.Errorf("expansion kind = %q", MilestoneKindExpansion)
	}
	if MilestoneKindJobLevel != "job_level" {
		t.Errorf("job level kind = %q", MilestoneKindJobLevel)
	}
	if MilestoneKindChocobo != "chocobo" {
		t.Errorf("chocobo kind = %q", MilestoneKindChocobo)
	}
}
