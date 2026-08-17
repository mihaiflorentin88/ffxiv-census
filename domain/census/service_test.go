package census

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/data/gender"
	"github.com/xivapi/godestone/v2/provider/models"

	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestService(t *testing.T) (*Service, *mockrepo.CharacterRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	svc := NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return svc, chars
}

// newTestServiceAll returns the service plus every fake it depends on, so
// tests can seed jobs/milestones/free companies directly.
func newTestServiceAll(t *testing.T) (*Service, *mockrepo.CharacterRepository, *mockrepo.FreeCompanyRepository, *mockrepo.AchievementRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	fcs := mockrepo.NewFreeCompanyFake()
	ach := mockrepo.NewAchievementFake()
	svc := NewService(chars, fcs, ach, mockrepo.NewCensusRunFake())
	return svc, chars, fcs, ach
}

func TestService_UpsertCharacter(t *testing.T) {
	svc, chars := newTestService(t)

	char := &godestone.Character{
		ID:     123,
		Name:   "Tataru Taru",
		World:  "Ultros",
		DC:     "Primal",
		Gender: gender.Female,
		Race:   &models.GenderedEntity{Name: "Lalafell"},
		Tribe:  &models.GenderedEntity{Name: "Dunesfolk"},
		GrandCompanyInfo: &godestone.GrandCompanyInfo{
			GrandCompany: &models.NamedEntity{Name: "Maelstrom"},
		},
		FreeCompanyID:   "9234567890123456789",
		FreeCompanyName: "The Scions",
		ClassJobs: []*godestone.ClassJob{
			{JobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
			{JobID: 25, Name: "White Mage", Level: 90, ExpLevel: 0},
		},
	}

	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	got, err := chars.Get(context.Background(), 123)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.Region != "NA" {
		t.Errorf("region = %q, want NA (derived from Primal)", got.Region)
	}
	if got.Race != "Lalafell" || got.GrandCompany != "Maelstrom" {
		t.Errorf("got %+v", got)
	}
	if got.FreeCompanyID == nil || *got.FreeCompanyID != "9234567890123456789" {
		t.Errorf("free company id = %v", got.FreeCompanyID)
	}
	jobs, _ := chars.GetJobs(context.Background(), 123)
	if len(jobs) != 2 {
		t.Errorf("jobs = %d, want 2", len(jobs))
	}
}

func TestService_StreamCharacters(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 1, Name: "Char 1", World: "Ultros", FirstSeenAt: now}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 2, Name: "Char 2", World: "Leviathan", FirstSeenAt: now}, nil)

	var streamed []uint32
	err := svc.StreamCharacters(ctx, contract.CharacterFilter{World: "Ultros"}, func(rec contract.CharacterRecord) error {
		streamed = append(streamed, rec.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCharacters: %v", err)
	}
	if len(streamed) != 1 || streamed[0] != 1 {
		t.Fatalf("streamed = %v, want [1]", streamed)
	}
}

func TestService_UpsertCharacter_NilSafe(t *testing.T) {
	svc, _ := newTestService(t)
	// Minimal character with nil race/tribe/grand company must not panic.
	char := &godestone.Character{ID: 1, Name: "X", World: "W", DC: "Primal", Gender: gender.None}
	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
}
func TestService_UpsertTomestoneCharacter(t *testing.T) {
	svc, chars := newTestService(t)

	fcID := "9234567890123456789"
	fcName := "The Scions"
	tChar := &contract.TomestoneCharacter{
		ID:              456,
		Name:            "Alphinaud Leveilleur",
		Server:          "Cerberus",
		Datacenter:      "Chaos",
		Gender:          "male",
		Race:            "Elezen",
		Tribe:           "Wildwood",
		GrandCompany:    "Immortal Flames",
		FreeCompanyID:   &fcID,
		FreeCompanyName: &fcName,
		Jobs: []contract.TomestoneClassJob{
			{ID: 33, Name: "Sage", Level: 90, Exp: 5000, ExpMax: 10000},
			{ID: 26, Name: "Arcanist", Level: 50, Exp: 100, ExpMax: 2000},
		},
	}

	if err := svc.UpsertTomestoneCharacter(context.Background(), tChar); err != nil {
		t.Fatalf("UpsertTomestoneCharacter: %v", err)
	}

	got, err := chars.Get(context.Background(), 456)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.Region != "EU" {
		t.Errorf("region = %q, want EU (derived from Chaos)", got.Region)
	}
	if got.Name != "Alphinaud Leveilleur" || got.World != "Cerberus" || got.Datacenter != "Chaos" {
		t.Errorf("unexpected profile: %+v", got)
	}
	if got.Gender != 1 {
		t.Errorf("gender = %d, want 1 (male)", got.Gender)
	}
	if got.Race != "Elezen" || got.Tribe != "Wildwood" || got.GrandCompany != "Immortal Flames" {
		t.Errorf("unexpected identity: %+v", got)
	}
	if got.FreeCompanyID == nil || *got.FreeCompanyID != fcID {
		t.Errorf("free company id = %v, want %v", got.FreeCompanyID, fcID)
	}

	jobs, err := chars.GetJobs(context.Background(), 456)
	if err != nil {
		t.Fatalf("GetJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs len = %d, want 2", len(jobs))
	}
	if jobs[0].ClassJobID != 33 || jobs[0].Name != "Sage" || jobs[0].Level != 90 || jobs[0].ExpLevel != 5000 {
		t.Errorf("jobs[0] = %+v", jobs[0])
	}
}

func TestService_UpsertTomestoneCharacter_ProfileAndGear(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()

	dye := "Snow White"
	tChar := &contract.TomestoneCharacter{
		ID:          789,
		Name:        "Thancred Waters",
		Server:      "Ragnarok",
		Datacenter:  "Chaos",
		Gender:      "male",
		Race:        "Hyur",
		Tribe:       "Midlander",
		AvatarURL:   "https://tomestone.gg/avatar.png",
		PortraitURL: "https://tomestone.gg/portrait.png",
		Bio:         "Gunbreaker of the Scions",
		ActiveJob:   "Gunbreaker",
		Gear: []contract.TomestoneGear{
			{
				Slot:      "MainHand",
				ID:        50001,
				Name:      "Gunblade of the Round",
				ItemLevel: 660,
				Dye:       &dye,
				Materia:   []string{"Direct Hit Materia X"},
			},
			{
				Slot:      "Body",
				ID:        50002,
				Name:      "Coat of the Round",
				ItemLevel: 650,
				Dye:       nil,
				Materia:   []string{"Critical Hit Materia X"},
			},
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := svc.UpsertTomestoneCharacter(ctx, tChar); err != nil {
		t.Fatalf("UpsertTomestoneCharacter: %v", err)
	}

	got, err := chars.Get(ctx, 789)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.AvatarURL != tChar.AvatarURL || got.PortraitURL != tChar.PortraitURL || got.Bio != tChar.Bio || got.ActiveJob != tChar.ActiveJob {
		t.Errorf("profile fields mismatch: %+v", got)
	}
	// Average item level: (660 + 650) / 2 = 655
	if got.ItemLevel != 655 {
		t.Errorf("ItemLevel = %d, want 655", got.ItemLevel)
	}

	gear, err := chars.GetGear(ctx, 789)
	if err != nil {
		t.Fatalf("GetGear: %v", err)
	}
	if len(gear) != 2 {
		t.Fatalf("gear count = %d, want 2", len(gear))
	}

	detail, err := svc.CharacterDetail(ctx, 789)
	if err != nil || detail == nil {
		t.Fatalf("CharacterDetail: %v / %+v", err, detail)
	}
	if len(detail.Gear) != 2 {
		t.Errorf("detail.Gear count = %d, want 2", len(detail.Gear))
	}
}

func TestService_UpsertCharacter_Profile(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()

	char := &godestone.Character{
		ID:       555,
		Name:     "Y'shtola Rhul",
		World:    "Louisoix",
		DC:       "Chaos",
		Avatar:   "https://lodestone.com/avatar.jpg",
		Portrait: "https://lodestone.com/portrait.jpg",
		Bio:      "Sorceress of the Night's Blessed",
		ActiveClassJob: &godestone.ClassJob{
			JobID: 25,
			Name:  "Black Mage",
		},
	}

	if err := svc.UpsertCharacter(ctx, char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}

	got, err := chars.Get(ctx, 555)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.AvatarURL != char.Avatar || got.PortraitURL != char.Portrait || got.Bio != char.Bio || got.ActiveJob != "Black Mage" {
		t.Errorf("profile fields mismatch: %+v", got)
	}
}

func TestService_FindUnscannedIDGaps(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 2, FirstSeenAt: now}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 5, FirstSeenAt: now}, nil)

	gaps, err := svc.FindUnscannedIDGaps(ctx, 5, 10)
	if err != nil {
		t.Fatalf("FindUnscannedIDGaps: %v", err)
	}
	want := [][2]uint32{
		{1, 1},
		{3, 4},
	}
	if !reflect.DeepEqual(gaps, want) {
		t.Errorf("gaps = %v, want %v", gaps, want)
	}
}

func TestService_MaxCharacterID(t *testing.T) {
	svc, chars := newTestService(t)

	maxID, err := svc.MaxCharacterID(context.Background())
	if err != nil {
		t.Fatalf("MaxCharacterID: %v", err)
	}
	if maxID != 0 {
		t.Fatalf("MaxCharacterID = %d, want 0", maxID)
	}

	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 100, Name: "A"}, nil)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 999, Name: "B"}, nil)

	maxID, err = svc.MaxCharacterID(context.Background())
	if err != nil {
		t.Fatalf("MaxCharacterID: %v", err)
	}
	if maxID != 999 {
		t.Fatalf("MaxCharacterID = %d, want 999", maxID)
	}
}

func TestService_ProcessAchievements(t *testing.T) {
	svc, chars := newTestService(t)
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	// The character must exist before the summary update.
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 123, Name: "X", FirstSeenAt: time.Now()}, nil)

	earned := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: time.Now().Add(-48 * time.Hour)},
		{NamedEntity: &models.NamedEntity{ID: 999, Name: "Some Other Achievement"}, Date: time.Now().Add(-1 * time.Hour)},
	}
	all := &godestone.AllAchievementInfo{Private: false, TotalAchievements: 2, TotalAchievementPoints: 25}

	ms, err := svc.ProcessAchievements(context.Background(), 123, earned, all)
	if err != nil {
		t.Fatalf("ProcessAchievements: %v", err)
	}
	// Only the registered milestone (590) is kept; 999 is filtered out.
	if len(ms) != 1 || ms[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want only 590", ms)
	}
	// Latest achievement (any, not just milestone) is 999 at -1h.
	got, _ := chars.Get(context.Background(), 123)
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 999 {
		t.Errorf("latest achievement = %v, want 999", got.LatestAchievementID)
	}
	if got.AchievementsPrivate {
		t.Error("achievements_private = true, want false")
	}
}

func TestService_ProcessAchievements_Private(t *testing.T) {
	svc, chars := newTestService(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 5, Name: "X", FirstSeenAt: time.Now()}, nil)

	all := &godestone.AllAchievementInfo{Private: true}
	if _, err := svc.ProcessAchievements(context.Background(), 5, nil, all); err != nil {
		t.Fatalf("ProcessAchievements: %v", err)
	}
	got, _ := chars.Get(context.Background(), 5)
	if !got.AchievementsPrivate {
		t.Error("achievements_private = false, want true")
	}
}

func TestService_IsActive(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now().UTC()
	if !svc.IsActive(now.Add(-time.Hour)) {
		t.Error("achievement 1h ago should be active within 30d window")
	}
	if svc.IsActive(now.Add(-31 * 24 * time.Hour)) {
		t.Error("achievement 31d ago should not be active")
	}
}

func TestService_IsActive_ZeroTime(t *testing.T) {
	svc, _ := newTestService(t)
	if svc.IsActive(time.Time{}) {
		t.Error("zero time should not be active")
	}
}

func TestService_ProcessAchievements_PreservesMilestonesOnPrivate(t *testing.T) {
	svc, chars := newTestService(t)
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 7, Name: "X", FirstSeenAt: time.Now()}, nil)

	// First, process public achievements so a milestone + latest are recorded.
	earned := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: time.Now()},
	}
	if _, err := svc.ProcessAchievements(context.Background(), 7, earned, &godestone.AllAchievementInfo{}); err != nil {
		t.Fatalf("ProcessAchievements (public): %v", err)
	}

	// Now the profile goes private: milestones and latest must be preserved.
	if _, err := svc.ProcessAchievements(context.Background(), 7, nil, &godestone.AllAchievementInfo{Private: true}); err != nil {
		t.Fatalf("ProcessAchievements (private): %v", err)
	}
	got, _ := chars.Get(context.Background(), 7)
	if !got.AchievementsPrivate {
		t.Error("achievements_private = false, want true")
	}
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 590 {
		t.Errorf("latest achievement was wiped on private turn: %v", got.LatestAchievementID)
	}
	milestones, err := svc.achievements.ListCharacterMilestones(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListCharacterMilestones: %v", err)
	}
	if len(milestones) != 1 || milestones[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want only 590 preserved", milestones)
	}
}

func TestService_ProcessAchievements_EmptyRegistry(t *testing.T) {
	svc, chars := newTestService(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 9, Name: "X", FirstSeenAt: time.Now()}, nil)

	// No SyncMilestones: registry is empty, processing must error rather than wipe.
	if _, err := svc.ProcessAchievements(context.Background(), 9, nil, &godestone.AllAchievementInfo{}); err == nil {
		t.Fatal("expected error for empty milestone registry")
	}
}

func TestService_Summary(t *testing.T) {
	svc, chars := newTestService(t)
	svc.SetActivityWindow(30 * 24 * time.Hour)
	ctx := context.Background()
	now := time.Now().UTC()
	active := now.Add(-time.Hour)
	inactive := now.Add(-60 * 24 * time.Hour)

	seed := func(id uint32, lat *time.Time) {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: "Ultros", FirstSeenAt: now}, nil)
		if lat != nil {
			_ = chars.UpdateAchievementSummary(ctx, id, false, nil, lat)
		}
	}
	seed(1, &active)   // active within window
	seed(2, &inactive) // outside window
	seed(3, nil)       // never achievement-censused
	seed(4, &active)   // active, but deleted below
	_ = chars.MarkDeleted(ctx, 4, now)

	total, gotActive, err := svc.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (deleted excluded)", total)
	}
	if gotActive != 1 {
		t.Errorf("active = %d, want 1", gotActive)
	}
}

func TestService_ListCharacters_Pagination(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []uint32{1, 2, 3} {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: "Ultros", FirstSeenAt: now}, nil)
	}
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 4, Name: "gone", World: "Ultros", FirstSeenAt: now}, nil)
	_ = chars.MarkDeleted(ctx, 4, now)

	page, total, err := svc.ListCharacters(ctx, contract.CharacterFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (deleted excluded)", total)
	}
	if len(page) != 2 || page[0].ID != 1 || page[1].ID != 2 {
		t.Errorf("page(2,0) = %+v, want ids [1 2]", page)
	}

	page, total, err = svc.ListCharacters(ctx, contract.CharacterFilter{}, 2, 2)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(page) != 1 || page[0].ID != 3 {
		t.Errorf("page(2,2) = %+v, want id [3]", page)
	}
}

func TestService_ListCharacters_Filter(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 1, Name: "Feed How", World: "Louisoix", Race: "Au Ra", FirstSeenAt: now}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 2, Name: "Ninto Thegen", World: "Louisoix", Race: "Miqo'te", FirstSeenAt: now}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 3, Name: "Ahribella White", World: "Zodiark", Race: "Miqo'te", FirstSeenAt: now}, nil)

	page, total, err := svc.ListCharacters(ctx, contract.CharacterFilter{World: "Louisoix"}, 10, 0)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("total = %d, len = %d, want 2", total, len(page))
	}
	if page[0].ID != 1 || page[1].ID != 2 {
		t.Errorf("got ids [%d, %d], want [1, 2]", page[0].ID, page[1].ID)
	}
}

func TestService_CharacterDetail_Missing(t *testing.T) {
	svc, _ := newTestService(t)
	got, err := svc.CharacterDetail(context.Background(), 999)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for unknown id", got)
	}
}

func TestService_CharacterDetail_WithFreeCompany(t *testing.T) {
	svc, chars, fcs, ach := newTestServiceAll(t)
	ctx := context.Background()
	now := time.Now().UTC()
	fcID := "9234567890123456789"
	fcName := "The Scions"
	_ = chars.Upsert(ctx, contract.CharacterRecord{
		ID: 123, Name: "Tataru Taru", World: "Ultros", Datacenter: "Primal", Region: "NA",
		Race: "Lalafell", Gender: 2, FirstSeenAt: now,
		FreeCompanyID:       &fcID,
		FreeCompanyName:     &fcName,
		LatestAchievementAt: &now,
	}, []contract.ClassJobRecord{
		{CharacterID: 123, ClassJobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
	})
	_ = fcs.Upsert(ctx, contract.FreeCompanyRecord{ID: fcID, Name: fcName, World: "Ultros", Datacenter: "Primal", MemberCount: 100, LastSeenAt: now})
	_ = ach.UpsertCharacterMilestones(ctx, 123, []contract.CharacterMilestone{
		{CharacterID: 123, AchievementID: 590, AchievedAt: now},
	})

	got, err := svc.CharacterDetail(ctx, 123)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got == nil {
		t.Fatal("CharacterDetail = nil, want detail")
	}
	if got.Character.ID != 123 || got.Character.Name != "Tataru Taru" {
		t.Errorf("Character = %+v", got.Character)
	}
	if len(got.Jobs) != 1 || got.Jobs[0].ClassJobID != 19 || got.Jobs[0].Level != 90 {
		t.Errorf("Jobs = %+v", got.Jobs)
	}
	if len(got.Milestones) != 1 || got.Milestones[0].AchievementID != 590 {
		t.Errorf("Milestones = %+v", got.Milestones)
	}
	if got.FreeCompany == nil || got.FreeCompany.ID != fcID || got.FreeCompany.Name != fcName {
		t.Errorf("FreeCompany = %+v, want id %q", got.FreeCompany, fcID)
	}
}

func TestService_CharacterDetail_MissingFreeCompany(t *testing.T) {
	svc, chars, _, _ := newTestServiceAll(t)
	ctx := context.Background()
	fcID := "9234567890123456789"
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 7, Name: "X", World: "Ultros", FirstSeenAt: time.Now(), FreeCompanyID: &fcID}, nil)

	got, err := svc.CharacterDetail(ctx, 7)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got == nil {
		t.Fatal("CharacterDetail = nil, want detail")
	}
	if got.FreeCompany != nil {
		t.Errorf("FreeCompany = %+v, want nil when the FC was never ingested", got.FreeCompany)
	}
}

func TestService_Breakdown_InvalidDimension(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Breakdown(context.Background(), "level")
	if !errors.Is(err, ErrInvalidDimension) {
		t.Fatalf("err = %v, want ErrInvalidDimension", err)
	}
}

func TestService_Breakdown_Delegates(t *testing.T) {
	svc, chars := newTestService(t)
	svc.SetActivityWindow(30 * 24 * time.Hour)
	ctx := context.Background()
	now := time.Now().UTC()
	active := now.Add(-time.Hour)
	inactive := now.Add(-60 * 24 * time.Hour)

	seed := func(id uint32, world string, lat *time.Time) {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: world, FirstSeenAt: now}, nil)
		if lat != nil {
			_ = chars.UpdateAchievementSummary(ctx, id, false, nil, lat)
		}
	}
	seed(1, "Ultros", &active)
	seed(2, "Ultros", &inactive)
	seed(3, "Leviathan", &active)

	got, err := svc.Breakdown(ctx, "world")
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	want := []contract.GroupCount{
		{Key: "Ultros", Total: 2, Active: 1},
		{Key: "Leviathan", Total: 1, Active: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Breakdown = %+v, want %+v", got, want)
	}
}

func TestService_WorldDetail(t *testing.T) {
	svc, chars, _, ach := newTestServiceAll(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = chars.Upsert(ctx, contract.CharacterRecord{
		ID: 1, Name: "Char 1", World: "Balmung", Datacenter: "Crystal", Region: "NA", Race: "Hyur", FirstSeenAt: now,
		LatestAchievementAt: &now,
	}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{
		ID: 2, Name: "Char 2", World: "Balmung", Datacenter: "Crystal", Region: "NA", Race: "Elezen", FirstSeenAt: now,
	}, nil)
	_ = chars.Upsert(ctx, contract.CharacterRecord{
		ID: 3, Name: "Char 3", World: "Mateus", Datacenter: "Crystal", Region: "NA", Race: "Hyur", FirstSeenAt: now,
	}, nil)

	ach.ChocoboCountResponse = 2
	ach.ExpansionsResponse = []contract.ExpansionCount{{Expansion: "Endwalker", Count: 1}}
	ach.NewCharactersResponse = []contract.DailyCount{{Day: "2026-08-17", Count: 2}}

	detail, err := svc.WorldDetail(ctx, "Balmung")
	if err != nil {
		t.Fatalf("WorldDetail: %v", err)
	}
	if detail.World != "Balmung" {
		t.Errorf("World = %q, want Balmung", detail.World)
	}
	if detail.TotalCharacters != 2 {
		t.Errorf("TotalCharacters = %d, want 2", detail.TotalCharacters)
	}
	if detail.ActiveCharacters != 1 {
		t.Errorf("ActiveCharacters = %d, want 1", detail.ActiveCharacters)
	}
	if detail.NewCharacters30d != 2 {
		t.Errorf("NewCharacters30d = %d, want 2", detail.NewCharacters30d)
	}
	if len(detail.Races) != 2 {
		t.Errorf("Races len = %d, want 2", len(detail.Races))
	}
	if len(detail.MSQCompletions) != 1 {
		t.Errorf("MSQCompletions len = %d, want 1", len(detail.MSQCompletions))
	}
	if len(detail.NewCharactersTimeline) != 1 {
		t.Errorf("NewCharactersTimeline len = %d, want 1", len(detail.NewCharactersTimeline))
	}
}

func TestService_NewCharacters(t *testing.T) {
	svc, _, _, ach := newTestServiceAll(t)
	ctx := context.Background()
	mk := func(day int) time.Time { return time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC) }

	ach.NewCharactersResponse = []contract.DailyCount{
		{Day: "2026-08-11", Count: 2},
		{Day: "2026-08-13", Count: 1},
	}

	got, err := svc.NewCharacters(ctx, mk(10), mk(14))
	if err != nil {
		t.Fatalf("NewCharacters: %v", err)
	}
	want := []contract.DailyCount{
		{Day: "2026-08-11", Count: 2},
		{Day: "2026-08-13", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewCharacters = %+v, want %+v", got, want)
	}
}

func TestService_ExpansionCompletions(t *testing.T) {
	svc, _, _, ach := newTestServiceAll(t)
	ctx := context.Background()
	if err := svc.SyncMilestones(ctx); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	now := time.Now().UTC()
	_ = ach.UpsertCharacterMilestones(ctx, 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now}, // Heavensward
		{CharacterID: 1, AchievementID: 1794, AchievedAt: now}, // Stormblood
	})
	_ = ach.UpsertCharacterMilestones(ctx, 2, []contract.CharacterMilestone{
		{CharacterID: 2, AchievementID: 1139, AchievedAt: now},
	})
	_ = ach.UpsertCharacterMilestones(ctx, 3, []contract.CharacterMilestone{
		{CharacterID: 3, AchievementID: 590, AchievedAt: now}, // non-expansion milestone
	})

	got, err := svc.ExpansionCompletions(ctx)
	if err != nil {
		t.Fatalf("ExpansionCompletions: %v", err)
	}
	want := []contract.ExpansionCount{
		{Expansion: "Heavensward", Count: 2},
		{Expansion: "Stormblood", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpansionCompletions = %+v, want %+v", got, want)
	}
}

func TestService_SetActivityWindow_Noop(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SetActivityWindow(0)
	svc.SetActivityWindow(-time.Hour)
	if !svc.IsActive(time.Now().UTC().Add(-15 * 24 * time.Hour)) {
		t.Error("no-op SetActivityWindow should keep the default 30-day window")
	}
}

func TestService_IsActive_ConfiguredWindow(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SetActivityWindow(7 * 24 * time.Hour)
	now := time.Now().UTC()
	if !svc.IsActive(now.Add(-6 * 24 * time.Hour)) {
		t.Error("achievement 6d ago should be active within a 7d window")
	}
	if svc.IsActive(now.Add(-8 * 24 * time.Hour)) {
		t.Error("achievement 8d ago should not be active within a 7d window")
	}
}
