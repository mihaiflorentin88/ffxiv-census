package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestUIStatsFakeReturnsDefensiveCopyAndRecordsCalls(t *testing.T) {
	generated := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	snapshot := &contract.UIStatsSnapshot{
		SchemaVersion:    contract.UIStatsSchemaVersion,
		GeneratedAt:      generated,
		ActivitySince:    generated.Add(-30 * 24 * time.Hour),
		SourceCharacters: 1,
		Summary:          contract.StatsSummary{Total: 1},
	}
	fake := NewUIStatsFake(snapshot)

	got, err := fake.LoadCurrent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got.Summary.Total = 999
	again, err := fake.LoadCurrent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Summary.Total != 1 {
		t.Fatalf("stored snapshot mutated: total = %d", again.Summary.Total)
	}
	if fake.LoadCalls != 2 {
		t.Fatalf("LoadCalls = %d, want 2", fake.LoadCalls)
	}
}
