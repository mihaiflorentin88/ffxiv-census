package census

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service is the domain brain of the census. It converts Lodestone DTOs into
// persisted records and computes milestone/activity facts. It depends only on
// contracts, never on SQL or HTTP.
type Service struct {
	characters     contract.CharacterRepository
	freeCompanies  contract.FreeCompanyRepository
	achievements   contract.AchievementRepository
	censusRuns     contract.CensusRunRepository
	mu             sync.RWMutex
	activityWindow time.Duration
}

func NewService(
	characters contract.CharacterRepository,
	freeCompanies contract.FreeCompanyRepository,
	achievements contract.AchievementRepository,
	censusRuns contract.CensusRunRepository,
) *Service {
	return &Service{
		characters:     characters,
		freeCompanies:  freeCompanies,
		achievements:   achievements,
		censusRuns:     censusRuns,
		activityWindow: defaultActivityWindow,
	}
}

// SyncMilestones seeds the milestone registry into the DB (idempotent).
func (s *Service) SyncMilestones(ctx context.Context) error {
	return s.achievements.SyncMilestones(ctx, MilestoneSet)
}

// UpsertCharacter converts a Lodestone character into a CharacterRecord and
// persists it (profile + jobs) atomically. Region is derived from the
// datacenter. nil race/tribe/grand-company are tolerated (stored empty).
func (s *Service) UpsertCharacter(ctx context.Context, char *godestone.Character) error {
	if char == nil {
		return errors.New("cannot upsert nil character")
	}
	rec := toCharacterRecord(char)
	jobs := toJobRecords(char)
	return s.characters.Upsert(ctx, rec, jobs)
}

// UpsertTomestoneCharacter converts a Tomestone character into a CharacterRecord and
// persists it (profile + jobs + gear) atomically. Region is derived from the datacenter.
func (s *Service) UpsertTomestoneCharacter(ctx context.Context, char *contract.TomestoneCharacter) error {
	if char == nil {
		return errors.New("cannot upsert nil tomestone character")
	}
	rec := toTomestoneCharacterRecord(char)
	jobs := toTomestoneJobRecords(char)
	if err := s.characters.Upsert(ctx, rec, jobs); err != nil {
		return err
	}
	if len(char.Gear) > 0 {
		gearRecords := make([]contract.CharacterGearRecord, 0, len(char.Gear))
		for _, g := range char.Gear {
			gearRecords = append(gearRecords, contract.CharacterGearRecord{
				CharacterID: char.ID,
				Slot:        g.Slot,
				ItemID:      g.ID,
				Name:        g.Name,
				ItemLevel:   g.ItemLevel,
				Dye:         g.Dye,
				Materia:     g.Materia,
				UpdatedAt:   char.UpdatedAt,
			})
		}
		if err := s.characters.UpsertGear(ctx, char.ID, gearRecords); err != nil {
			return err
		}
	}
	return nil
}

// MaxCharacterID returns the maximum character ID known to the census.
func (s *Service) MaxCharacterID(ctx context.Context) (uint32, error) {
	return s.characters.MaxID(ctx)
}

// FindUnscannedIDGaps returns missing/unscanned ID ranges between 1 and maxID.
func (s *Service) FindUnscannedIDGaps(ctx context.Context, maxID uint32, limit int) ([][2]uint32, error) {
	return s.characters.FindIDGaps(ctx, maxID, limit)
}

func parseTomestoneGender(g string) uint8 {
	if strings.EqualFold(g, "female") {
		return 2
	} else if strings.EqualFold(g, "male") {
		return 1
	}
	return 0
}

func calculateAverageItemLevel(gear []contract.TomestoneGear) int {
	if len(gear) == 0 {
		return 0
	}
	sum := 0
	count := 0
	for _, g := range gear {
		if g.ItemLevel > 0 {
			sum += g.ItemLevel
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / count
}

func toTomestoneCharacterRecord(char *contract.TomestoneCharacter) contract.CharacterRecord {
	now := time.Now().UTC()
	return contract.CharacterRecord{
		ID:              char.ID,
		Name:            char.Name,
		World:           char.Server,
		Datacenter:      char.Datacenter,
		Region:          RegionForDatacenter(char.Datacenter),
		Gender:          parseTomestoneGender(char.Gender),
		Race:            char.Race,
		Tribe:           char.Tribe,
		GrandCompany:    char.GrandCompany,
		FreeCompanyID:   char.FreeCompanyID,
		FreeCompanyName: char.FreeCompanyName,
		AvatarURL:       char.AvatarURL,
		PortraitURL:     char.PortraitURL,
		Bio:             char.Bio,
		ActiveJob:       char.ActiveJob,
		ItemLevel:       calculateAverageItemLevel(char.Gear),
		FirstSeenAt:     now,
		LastCensusAt:    &now,
	}
}

func toTomestoneJobRecords(char *contract.TomestoneCharacter) []contract.ClassJobRecord {
	jobs := make([]contract.ClassJobRecord, 0, len(char.Jobs))
	for _, j := range char.Jobs {
		jobs = append(jobs, contract.ClassJobRecord{
			CharacterID: char.ID,
			ClassJobID:  j.ID,
			Name:        j.Name,
			Level:       j.Level,
			ExpLevel:    j.Exp,
		})
	}
	return jobs
}

func toCharacterRecord(char *godestone.Character) contract.CharacterRecord {
	now := time.Now().UTC()
	rec := contract.CharacterRecord{
		ID:           char.ID,
		Name:         char.Name,
		World:        char.World,
		Datacenter:   char.DC,
		Region:       RegionForDatacenter(char.DC),
		Gender:       uint8(char.Gender),
		AvatarURL:    char.Avatar,
		PortraitURL:  char.Portrait,
		Bio:          char.Bio,
		FirstSeenAt:  now,
		LastCensusAt: &now,
	}
	if char.Race != nil {
		rec.Race = char.Race.Name
	}
	if char.Tribe != nil {
		rec.Tribe = char.Tribe.Name
	}
	if char.GrandCompanyInfo != nil && char.GrandCompanyInfo.GrandCompany != nil {
		rec.GrandCompany = char.GrandCompanyInfo.GrandCompany.Name
	}
	if char.FreeCompanyID != "" {
		rec.FreeCompanyID = &char.FreeCompanyID
	}
	if char.FreeCompanyName != "" {
		rec.FreeCompanyName = &char.FreeCompanyName
	}
	if char.ActiveClassJob != nil {
		rec.ActiveJob = char.ActiveClassJob.Name
	}
	return rec
}
func toJobRecords(char *godestone.Character) []contract.ClassJobRecord {
	jobs := make([]contract.ClassJobRecord, 0, len(char.ClassJobs))
	for _, j := range char.ClassJobs {
		if j == nil {
			continue
		}
		jobs = append(jobs, contract.ClassJobRecord{
			CharacterID: char.ID,
			ClassJobID:  classJobKey(j),
			Name:        j.Name,
			Level:       j.Level,
			ExpLevel:    j.ExpLevel,
		})
	}
	return jobs
}

// classJobKey returns a stable per-entry key for a class/job entry. godestone
// reports the corresponding job's ID in JobID for both classes and jobs, so
// JobID is the primary key; ClassID (the base class) is the fallback when
// JobID is absent.
func classJobKey(j *godestone.ClassJob) uint8 {
	if j.JobID != 0 {
		return j.JobID
	}
	return j.ClassID
}

const defaultActivityWindow = 30 * 24 * time.Hour

// ProcessAchievements filters earned achievements against the registry, persists
// only the matching milestones, and updates the character's achievement summary
// (private flag + latest achievement, which may be any achievement). Returns the
// milestones that matched the registry.
func (s *Service) ProcessAchievements(ctx context.Context, charID uint32, earned []*godestone.AchievementInfo, all *godestone.AllAchievementInfo) ([]contract.CharacterMilestone, error) {
	// A private profile hides its achievements: preserve any prior milestones and
	// latest achievement, only mark the profile private.
	if all != nil && all.Private {
		if err := s.characters.SetAchievementsPrivate(ctx, charID, true); err != nil {
			return nil, err
		}
		return nil, nil
	}

	registry, err := s.achievements.ListMilestones(ctx)
	if err != nil {
		return nil, err
	}
	if len(registry) == 0 {
		return nil, errors.New("milestone registry is empty; run SyncMilestones before processing achievements")
	}
	byID := make(map[uint32]contract.MilestoneAchievement, len(registry))
	for _, m := range registry {
		byID[m.AchievementID] = m
	}

	var milestones []contract.CharacterMilestone
	var latest *godestone.AchievementInfo
	for _, a := range earned {
		if a == nil || a.NamedEntity == nil {
			continue
		}
		if _, ok := byID[a.ID]; ok {
			milestones = append(milestones, contract.CharacterMilestone{
				CharacterID:   charID,
				AchievementID: a.ID,
				AchievedAt:    a.Date,
			})
		}
		if latest == nil || a.Date.After(latest.Date) {
			latest = a
		}
	}

	if err := s.achievements.UpsertCharacterMilestones(ctx, charID, milestones); err != nil {
		return nil, err
	}

	var latestID *uint32
	var latestAt *time.Time
	if latest != nil {
		id := latest.ID
		at := latest.Date
		latestID = &id
		latestAt = &at
	}
	if err := s.characters.UpdateAchievementSummary(ctx, charID, false, latestID, latestAt); err != nil {
		return nil, err
	}
	return milestones, nil
}

// IsActive reports whether a latest-achievement timestamp falls within the
// census activity window (default 30 days, overridable via SetActivityWindow).
func (s *Service) IsActive(latestAt time.Time) bool {
	s.mu.RLock()
	window := s.activityWindow
	s.mu.RUnlock()
	return !latestAt.IsZero() && time.Since(latestAt) <= window
}

// SetActivityWindow overrides the activity window used by IsActive, Summary,
// and Breakdown. Non-positive durations are ignored, keeping the current
// window.
func (s *Service) SetActivityWindow(d time.Duration) {
	if d > 0 {
		s.mu.Lock()
		s.activityWindow = d
		s.mu.Unlock()
	}
}

// activitySince returns the UTC instant marking the start of the activity
// window: anything at or after it counts as active.
func (s *Service) activitySince() time.Time {
	s.mu.RLock()
	window := s.activityWindow
	s.mu.RUnlock()
	return time.Now().UTC().Add(-window)
}

// UpsertFreeCompany converts a Lodestone free company into a record and persists it.
func (s *Service) UpsertFreeCompany(ctx context.Context, fc *godestone.FreeCompany) error {
	if fc == nil {
		return errors.New("cannot upsert nil free company")
	}
	return s.freeCompanies.Upsert(ctx, toFreeCompanyRecord(fc))
}

// MarkCharacterDeleted records that a character no longer exists on Lodestone.
func (s *Service) MarkCharacterDeleted(ctx context.Context, id uint32, at time.Time) error {
	return s.characters.MarkDeleted(ctx, id, at)
}

func toFreeCompanyRecord(fc *godestone.FreeCompany) contract.FreeCompanyRecord {
	rec := contract.FreeCompanyRecord{
		ID:          fc.ID,
		Name:        fc.Name,
		World:       fc.World,
		Datacenter:  fc.DC,
		MemberCount: fc.ActiveMemberCount,
		LastSeenAt:  time.Now().UTC(),
	}
	if !fc.Formed.IsZero() {
		rec.FormedAt = &fc.Formed
	}
	return rec
}

// ErrInvalidDimension is returned by Breakdown when by is not one of
// race|world|datacenter|region.
var ErrInvalidDimension = errors.New("invalid breakdown dimension: want race|world|datacenter|region")

// CharacterDetail aggregates a character's persisted profile, jobs, milestones,
// and free company into one response payload. FreeCompany is nil when the
// character is not in an FC or the referenced FC was never ingested.
type CharacterDetail struct {
	Character   contract.CharacterRecord
	Jobs        []contract.ClassJobRecord
	Gear        []contract.CharacterGearRecord
	Milestones  []contract.CharacterMilestone
	FreeCompany *contract.FreeCompanyRecord
}

// Summary returns the total number of non-deleted characters and how many of
// them are active (latest achievement within the activity window).
func (s *Service) Summary(ctx context.Context) (total, active int64, err error) {
	total, err = s.characters.Count(ctx, contract.CharacterFilter{})
	if err != nil {
		return 0, 0, err
	}
	active, err = s.characters.CountActive(ctx, s.activitySince())
	if err != nil {
		return 0, 0, err
	}
	return total, active, nil
}

// ListCharacters returns one page of non-deleted characters matching filter
// (ordered by id, limited/offset) plus the matching count for pagination.
func (s *Service) ListCharacters(ctx context.Context, f contract.CharacterFilter, limit, offset int) ([]contract.CharacterRecord, int64, error) {
	chars, err := s.characters.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.characters.Count(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return chars, total, nil
}

// StreamCharacters streams non-deleted characters matching filter in ID order, invoking fn for each record.
func (s *Service) StreamCharacters(ctx context.Context, f contract.CharacterFilter, fn func(rec contract.CharacterRecord) error) error {
	return s.characters.Stream(ctx, f, fn)
}

// CharacterDetail returns the full profile for one character, or nil (no
// error) when the id is unknown.
func (s *Service) CharacterDetail(ctx context.Context, id uint32) (*CharacterDetail, error) {
	rec, err := s.characters.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	jobs, err := s.characters.GetJobs(ctx, id)
	if err != nil {
		return nil, err
	}
	gear, err := s.characters.GetGear(ctx, id)
	if err != nil {
		return nil, err
	}
	milestones, err := s.achievements.ListCharacterMilestones(ctx, id)
	if err != nil {
		return nil, err
	}
	detail := &CharacterDetail{Character: *rec, Jobs: jobs, Gear: gear, Milestones: milestones}
	if rec.FreeCompanyID != nil {
		fc, err := s.freeCompanies.Get(ctx, *rec.FreeCompanyID)
		if err != nil {
			return nil, err
		}
		detail.FreeCompany = fc
	}
	return detail, nil
}

// ListFreeCompanies returns a page of free companies matching filter and the total count.
func (s *Service) ListFreeCompanies(ctx context.Context, f contract.FreeCompanyFilter, limit, offset int) ([]contract.FreeCompanyRecord, int64, error) {
	fcs, err := s.freeCompanies.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.freeCompanies.Count(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return fcs, total, nil
}

// FreeCompanyDetail returns the free company record for the given id, or nil if not found.
func (s *Service) FreeCompanyDetail(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	return s.freeCompanies.Get(ctx, id)
}

// WorldDetailStats aggregates high-level census facts for a specific world.
type WorldDetailStats struct {
	World                 string
	Datacenter            string
	Region                string
	TotalCharacters       int64
	ActiveCharacters      int64
	NewCharacters30d      int64
	Races                 []contract.GroupCount
	MSQCompletions        []contract.ExpansionCount
	NewCharactersTimeline []contract.DailyCount
}

// WorldDetail returns full census stats for a specific world.
func (s *Service) WorldDetail(ctx context.Context, worldName string) (*WorldDetailStats, error) {
	filter := contract.CharacterFilter{World: worldName}
	total, err := s.characters.Count(ctx, filter)
	if err != nil {
		return nil, err
	}

	filterActive := contract.CharacterFilter{World: worldName, ActiveOnly: true}
	active, err := s.characters.Count(ctx, filterActive)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	since30d := now.Add(-30 * 24 * time.Hour)
	new30d, err := s.achievements.CountChocoboMilestones(ctx, since30d, filter)
	if err != nil {
		return nil, err
	}

	races, err := s.characters.Breakdown(ctx, "race", s.activitySince(), filter)
	if err != nil {
		return nil, err
	}

	msq, err := s.achievements.CountExpansionsFiltered(ctx, filter)
	if err != nil {
		return nil, err
	}

	timeline, err := s.achievements.NewCharactersPerDay(ctx, since30d, now, filter)
	if err != nil {
		return nil, err
	}

	dc := ""
	region := ""
	// If we have characters, grab the DC from the first character in the list
	chars, err := s.characters.List(ctx, filter, 1, 0)
	if err == nil && len(chars) > 0 {
		dc = chars[0].Datacenter
		region = chars[0].Region
	}

	return &WorldDetailStats{
		World:                 worldName,
		Datacenter:            dc,
		Region:                region,
		TotalCharacters:       total,
		ActiveCharacters:      active,
		NewCharacters30d:      new30d,
		Races:                 races,
		MSQCompletions:        msq,
		NewCharactersTimeline: timeline,
	}, nil
}

// Breakdown groups non-deleted characters by by (race|world|datacenter|region)
// with total and activity-window counts per group. Unknown dimensions return
// ErrInvalidDimension without touching the repository.
func (s *Service) Breakdown(ctx context.Context, by string, filter ...contract.CharacterFilter) ([]contract.GroupCount, error) {
	switch by {
	case "race", "world", "datacenter", "region":
	default:
		return nil, ErrInvalidDimension
	}
	f := contract.CharacterFilter{}
	if len(filter) > 0 {
		f = filter[0]
	}
	return s.characters.Breakdown(ctx, by, s.activitySince(), f)
}

// NewCharacters returns daily counts of new characters in [since, until),
// using the early-game Chocobo milestone (achievement ID 590).
func (s *Service) NewCharacters(ctx context.Context, since, until time.Time, filter ...contract.CharacterFilter) ([]contract.DailyCount, error) {
	f := contract.CharacterFilter{}
	if len(filter) > 0 {
		f = filter[0]
	}
	return s.achievements.NewCharactersPerDay(ctx, since, until, f)
}

// ExpansionCompletions returns how many distinct characters completed each
// expansion's MSQ.
func (s *Service) ExpansionCompletions(ctx context.Context) ([]contract.ExpansionCount, error) {
	return s.achievements.CountExpansions(ctx)
}
