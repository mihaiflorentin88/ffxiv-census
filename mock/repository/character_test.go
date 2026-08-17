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
