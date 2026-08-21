package handler

import (
	"encoding/json"
	"testing"
)

func TestBuildDependentCharacterJobs(t *testing.T) {
	jobs := BuildDependentCharacterJobs(12345)
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
	if achPayload.CharacterID != 12345 {
		t.Errorf("achievement payload character_id = %d, want 12345", achPayload.CharacterID)
	}
}
