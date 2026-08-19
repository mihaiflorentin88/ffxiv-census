package handler

import (
	"encoding/json"
	"testing"
)

func TestBuildDependentCharacterJobs_WithFC(t *testing.T) {
	jobs := BuildDependentCharacterJobs(12345, "9234567890123456789")
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	// 1. Achievement census job
	if jobs[0].Type != EventAchievementCensus {
		t.Errorf("job[0].Type = %q, want %q", jobs[0].Type, EventAchievementCensus)
	}
	var achPayload AchievementCensusPayload
	if err := json.Unmarshal(jobs[0].Payload, &achPayload); err != nil {
		t.Fatalf("unmarshal achievement payload: %v", err)
	}
	if achPayload.CharacterID != 12345 {
		t.Errorf("achievement payload character_id = %d, want 12345", achPayload.CharacterID)
	}

	// 2. FC census job
	if jobs[1].Type != EventFreeCompanyCensus {
		t.Errorf("job[1].Type = %q, want %q", jobs[1].Type, EventFreeCompanyCensus)
	}
	var fcPayload FreeCompanyCensusPayload
	if err := json.Unmarshal(jobs[1].Payload, &fcPayload); err != nil {
		t.Fatalf("unmarshal fc payload: %v", err)
	}
	if fcPayload.FCID != "9234567890123456789" {
		t.Errorf("fc payload fc_id = %q, want 9234567890123456789", fcPayload.FCID)
	}
}

func TestBuildDependentCharacterJobs_WithoutFC(t *testing.T) {
	jobs := BuildDependentCharacterJobs(67890, "")
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	if jobs[0].Type != EventAchievementCensus {
		t.Errorf("job[0].Type = %q, want %q", jobs[0].Type, EventAchievementCensus)
	}
	var achPayload AchievementCensusPayload
	if err := json.Unmarshal(jobs[0].Payload, &achPayload); err != nil {
		t.Fatalf("unmarshal achievement payload: %v", err)
	}
	if achPayload.CharacterID != 67890 {
		t.Errorf("achievement payload character_id = %d, want 67890", achPayload.CharacterID)
	}
}
