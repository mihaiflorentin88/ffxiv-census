package handler

import "testing"

func TestEventConstants(t *testing.T) {
	if EventIDSweep != "id-sweep" {
		t.Errorf("EventIDSweep = %q", EventIDSweep)
	}
	if EventAchievementCensus != "achievement-census" {
		t.Errorf("EventAchievementCensus = %q", EventAchievementCensus)
	}
}

func TestRegistry_Lookup(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get(EventIDSweep); ok {
		t.Fatal("expected no handler before registration")
	}
}
