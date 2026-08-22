package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestMockCharacterRepository_ListFilter(t *testing.T) {
	repo := NewCharacterFake()
	ctx := context.Background()
	seed := func(id uint32, world, dc, region, race, name string) {
		rec := contract.CharacterRecord{ID: id, Name: name, World: world, Datacenter: dc, Region: region, Race: race, FirstSeenAt: time.Now().UTC()}
		if err := repo.Upsert(ctx, rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}
	seed(1, "Louisoix", "Chaos", "EU", "Au Ra", "Feed How")
	seed(2, "Louisoix", "Chaos", "EU", "Miqo'te", "Ninto Thegen")
	seed(3, "Zodiark", "Light", "EU", "Miqo'te", "Ahribella White")
	seed(4, "Ultros", "Primal", "NA", "Hyur", "Alpha Test")

	cases := []struct {
		name   string
		filter contract.CharacterFilter
		want   []uint32
	}{
		{"world exact", contract.CharacterFilter{World: "Louisoix"}, []uint32{1, 2}},
		{"race exact", contract.CharacterFilter{Race: "Miqo'te"}, []uint32{2, 3}},
		{"name substring case-insensitive", contract.CharacterFilter{Name: "feed"}, []uint32{1}},
		{"combined AND", contract.CharacterFilter{World: "Louisoix", Race: "Miqo'te"}, []uint32{2}},
		{"no match", contract.CharacterFilter{World: "Balmung"}, nil},
		{"empty filter returns all", contract.CharacterFilter{}, []uint32{1, 2, 3, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.List(ctx, tc.filter, 10, 0)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var ids []uint32
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if len(ids) != len(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("ids = %v, want %v", ids, tc.want)
				}
			}
			n, err := repo.Count(ctx, tc.filter)
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			if n != int64(len(tc.want)) {
				t.Fatalf("Count = %d, want %d", n, len(tc.want))
			}
		})
	}
}

func TestMockCharacterRepository_GearAndGaps(t *testing.T) {
	repo := NewCharacterFake()
	ctx := context.Background()
	now := time.Now().UTC()

	dye := "Jet Black"
	gear := []contract.CharacterGearRecord{
		{
			CharacterID: 50,
			Slot:        "MainHand",
			ItemID:      1234,
			Name:        "Test Sword",
			ItemLevel:   660,
			Dye:         &dye,
			Materia:     []string{"Materia A"},
			UpdatedAt:   now,
		},
	}

	if err := repo.UpsertGear(ctx, 50, gear); err != nil {
		t.Fatalf("UpsertGear: %v", err)
	}
	gotGear, err := repo.GetGear(ctx, 50)
	if err != nil {
		t.Fatalf("GetGear: %v", err)
	}
	if len(gotGear) != 1 || gotGear[0].Name != "Test Sword" || gotGear[0].Dye == nil || *gotGear[0].Dye != dye {
		t.Fatalf("GetGear mismatch: %+v", gotGear)
	}

	// Test FindIDGaps
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 3, FirstSeenAt: now}, nil)
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 4, FirstSeenAt: now}, nil)
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 8, FirstSeenAt: now}, nil)
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 15, FirstSeenAt: now}, nil)

	gaps, err := repo.FindIDGaps(ctx, 15, 10)
	if err != nil {
		t.Fatalf("FindIDGaps: %v", err)
	}
	want := [][2]uint32{
		{1, 2},
		{5, 7},
		{9, 14},
	}
	if len(gaps) != len(want) {
		t.Fatalf("gaps = %v, want %v", gaps, want)
	}
	for i := range gaps {
		if gaps[i] != want[i] {
			t.Errorf("gap[%d] = %v, want %v", i, gaps[i], want[i])
		}
	}
}

func TestMockCharacterRepository_MinLevelFilter(t *testing.T) {
	repo := NewCharacterFake()
	ctx := context.Background()
	now := time.Now().UTC()

	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 1, Name: "Char 1", FirstSeenAt: now}, []contract.ClassJobRecord{
		{CharacterID: 1, Level: 100},
	})
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 2, Name: "Char 2", FirstSeenAt: now}, []contract.ClassJobRecord{
		{CharacterID: 2, Level: 90},
	})
	_ = repo.Upsert(ctx, contract.CharacterRecord{ID: 3, Name: "Char 3", FirstSeenAt: now}, []contract.ClassJobRecord{
		{CharacterID: 3, Level: 50},
	})

	count100, err := repo.Count(ctx, contract.CharacterFilter{MinLevel: 100})
	if err != nil {
		t.Fatalf("Count(MinLevel: 100): %v", err)
	}
	if count100 != 1 {
		t.Errorf("Count(MinLevel: 100) = %d, want 1", count100)
	}

	count90, err := repo.Count(ctx, contract.CharacterFilter{MinLevel: 90})
	if err != nil {
		t.Fatalf("Count(MinLevel: 90): %v", err)
	}
	if count90 != 2 {
		t.Errorf("Count(MinLevel: 90) = %d, want 2", count90)
	}

	list100, err := repo.List(ctx, contract.CharacterFilter{MinLevel: 100}, 10, 0)
	if err != nil {
		t.Fatalf("List(MinLevel: 100): %v", err)
	}
	if len(list100) != 1 || list100[0].ID != 1 {
		t.Errorf("List(MinLevel: 100) = %v, want [ID: 1]", list100)
	}
}

func TestMockCharacterRepository_ListStale(t *testing.T) {
	repo := NewCharacterFake()
	ctx := context.Background()

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)

	// id=1: NULL last_census_at
	_ = repo.Upsert(ctx, contract.CharacterRecord{
		ID: 1, Name: "NullCensus", World: "Balmung", FirstSeenAt: old,
	}, nil)
	// id=2: old last_census_at
	_ = repo.Upsert(ctx, contract.CharacterRecord{
		ID: 2, Name: "OldCensus", World: "Balmung", FirstSeenAt: old, LastCensusAt: &old,
	}, nil)
	// id=3: recent last_census_at
	_ = repo.Upsert(ctx, contract.CharacterRecord{
		ID: 3, Name: "RecentCensus", World: "Balmung", FirstSeenAt: old, LastCensusAt: &recent,
	}, nil)

	// Zero cutoff, limit 2: NULL first, then oldest.
	got, err := repo.ListStale(ctx, time.Time{}, 2)
	if err != nil {
		t.Fatalf("ListStale zero cutoff: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("got[0].ID = %d, want 1 (NULL first)", got[0].ID)
	}
	if got[1].ID != 2 {
		t.Errorf("got[1].ID = %d, want 2 (oldest timestamp)", got[1].ID)
	}

	// Positive cutoff at recent: excludes id=3 (recent.Before(recent) is false).
	cutoff := recent
	got, err = repo.ListStale(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("ListStale positive cutoff: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("got IDs [%d, %d], want [1, 2]", got[0].ID, got[1].ID)
	}

	// Zero cutoff, limit 10: all three eligible.
	got, err = repo.ListStale(ctx, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListStale zero cutoff all: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3", len(got))
	}
	if got[2].ID != 3 {
		t.Errorf("got[2].ID = %d, want 3", got[2].ID)
	}
}
