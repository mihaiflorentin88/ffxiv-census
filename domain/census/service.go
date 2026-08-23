package census

import (
	"context"
	"errors"
	"fmt"
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
	achievements   contract.AchievementRepository
	censusRuns     contract.CensusRunRepository
	mu             sync.RWMutex
	activityWindow time.Duration
	maxLevel       uint32
	achievementStalenessDays int
	expansions     []ExpansionConfig
	// milestoneCache holds the last-fetched milestone registry to avoid
	// re-querying the DB on every ProcessAchievements call.
	milestoneCache    []contract.MilestoneAchievement
	milestoneCacheAt  time.Time
	milestoneCacheTTL time.Duration
}

func NewService(
	characters contract.CharacterRepository,
	achievements contract.AchievementRepository,
	censusRuns contract.CensusRunRepository,
) *Service {
	return &Service{
		characters:        characters,
		achievements:      achievements,
		censusRuns:        censusRuns,
		activityWindow:    defaultActivityWindow,
		maxLevel:          100,
		achievementStalenessDays: 7,
		expansions:        DefaultExpansions,
		milestoneCacheTTL: 5 * time.Minute,
	}
}

// SetConfig configures max level, expansion milestones, and achievement staleness.
func (s *Service) SetConfig(maxLevel uint32, expansions []ExpansionConfig, stalenessDays ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxLevel > 0 {
		s.maxLevel = maxLevel
	}
	if len(expansions) > 0 {
		s.expansions = make([]ExpansionConfig, len(expansions))
		copy(s.expansions, expansions)
	}
	if len(stalenessDays) > 0 && stalenessDays[0] > 0 {
		s.achievementStalenessDays = stalenessDays[0]
	}
}

// MaxLevel returns the configured max level cap.
func (s *Service) MaxLevel() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxLevel
}

// AchievementStalenessDays returns the configured staleness threshold in days.
func (s *Service) AchievementStalenessDays() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.achievementStalenessDays
}

// Expansions returns a copy of the configured expansions.
func (s *Service) Expansions() []ExpansionConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ExpansionConfig, len(s.expansions))
	copy(out, s.expansions)
	return out
}

// Milestones returns the full milestone achievement registry based on configured expansions.
func (s *Service) Milestones() []contract.MilestoneAchievement {
	return BuildMilestones(s.Expansions())
}

// SyncMilestones seeds the milestone registry into the DB (idempotent).
func (s *Service) SyncMilestones(ctx context.Context) error {
	if err := s.achievements.SyncMilestones(ctx, s.Milestones()); err != nil {
		return err
	}
	// Invalidate the cache so the next ProcessAchievements picks up the fresh registry.
	s.mu.Lock()
	s.milestoneCache = nil
	s.milestoneCacheAt = time.Time{}
	s.mu.Unlock()
	return nil
}

// cachedMilestones returns the milestone registry, using a short-lived cache
// to avoid querying the DB on every ProcessAchievements call.
func (s *Service) cachedMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	s.mu.RLock()
	cached := s.milestoneCache
	cachedAt := s.milestoneCacheAt
	ttl := s.milestoneCacheTTL
	s.mu.RUnlock()

	if cached != nil && time.Since(cachedAt) < ttl {
		return cached, nil
	}

	fresh, err := s.achievements.ListMilestones(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.milestoneCache = fresh
	s.milestoneCacheAt = time.Now()
	s.mu.Unlock()

	return fresh, nil
}

// MilestoneIDs returns the set of tracked milestone achievement IDs from the
// cached registry. Used by handlers to pre-filter achievements before processing.
func (s *Service) MilestoneIDs(ctx context.Context) (map[uint32]bool, error) {
	registry, err := s.cachedMilestones(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[uint32]bool, len(registry))
	for _, m := range registry {
		ids[m.AchievementID] = true
	}
	return ids, nil
}

// ListCharacterMilestones returns the milestones already earned by a character.
// Used by handlers to pre-seed the stop function and skip scraping when all
// milestones are known.
func (s *Service) ListCharacterMilestones(ctx context.Context, charID uint32) ([]contract.CharacterMilestone, error) {
	return s.achievements.ListCharacterMilestones(ctx, charID)
}

// GetCharacter returns a character record by ID, or nil if not found.
// Used by handlers that need to check staleness or other character fields.
func (s *Service) GetCharacter(ctx context.Context, charID uint32) (*contract.CharacterRecord, error) {
	return s.characters.Get(ctx, charID)
}

// UpsertCharacter converts a Lodestone character into a CharacterRecord and
// persists it (profile + jobs) atomically. Region is derived from the
// datacenter. nil race/tribe/grand-company are tolerated (stored empty).
func (s *Service) UpsertCharacter(ctx context.Context, char *godestone.Character) error {
	if char == nil {
		return errors.New("cannot upsert nil character")
	}
	if strings.TrimSpace(char.Name) == "" {
		return fmt.Errorf("cannot upsert character %d: name is empty", char.ID)
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
	if strings.TrimSpace(char.Name) == "" {
		return fmt.Errorf("cannot upsert character %d: name is empty", char.ID)
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

	registry, err := s.cachedMilestones(ctx)
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

// ActivitySince returns the UTC instant marking the start of the activity
// window. Exported for use by handlers that need to construct filters.
func (s *Service) ActivitySince() time.Time {
	return s.activitySince()
}

// activitySince returns the UTC instant marking the start of the activity
// window: anything at or after it counts as active.
func (s *Service) activitySince() time.Time {
	s.mu.RLock()
	window := s.activityWindow
	s.mu.RUnlock()
	return time.Now().UTC().Add(-window)
}

// MarkCharacterDeleted records that a character no longer exists on Lodestone.
func (s *Service) MarkCharacterDeleted(ctx context.Context, id uint32, at time.Time) error {
	return s.characters.MarkDeleted(ctx, id, at)
}

// ErrInvalidDimension is returned by Breakdown when by is not one of
// race|world|datacenter|region.
var ErrInvalidDimension = errors.New("invalid breakdown dimension: want race|world|datacenter|region")

// CharacterDetail aggregates a character's persisted profile, jobs, and milestones.
type CharacterDetail struct {
	Character  contract.CharacterRecord
	Jobs       []contract.ClassJobRecord
	Gear       []contract.CharacterGearRecord
	Milestones []contract.CharacterMilestone
}

// Summary returns the total number of non-deleted characters and how many of
// them are active (latest achievement within the activity window).
func (s *Service) Summary(ctx context.Context) (total, active, maxLevelCount int64, err error) {
	since := s.activitySince()
	maxLvl := s.MaxLevel()

	var wg sync.WaitGroup
	var totalErr, activeErr, maxErr error

	wg.Add(3)
	go func() {
		defer wg.Done()
		total, totalErr = s.characters.Count(ctx, contract.CharacterFilter{})
	}()
	go func() {
		defer wg.Done()
		active, activeErr = s.characters.CountActive(ctx, since)
	}()
	go func() {
		defer wg.Done()
		maxLevelCount, maxErr = s.characters.Count(ctx, contract.CharacterFilter{MinLevel: maxLvl})
	}()
	wg.Wait()

	// Deterministic error precedence: total, active, max-level.
	if totalErr != nil {
		return 0, 0, 0, totalErr
	}
	if activeErr != nil {
		return 0, 0, 0, activeErr
	}
	if maxErr != nil {
		return 0, 0, 0, maxErr
	}
	return total, active, maxLevelCount, nil
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
	return detail, nil
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
	since := s.ActivitySince()
	filterActive := contract.CharacterFilter{World: worldName, Since: &since}
	now := time.Now().UTC()
	since30d := now.Add(-30 * 24 * time.Hour)

	var total, active, new30d int64
	var races []contract.GroupCount
	var msq []contract.ExpansionCount
	var timeline []contract.DailyCount
	var chars []contract.CharacterRecord
	var totalErr, activeErr, new30dErr, racesErr, msqErr, timelineErr, listErr error

	var wg sync.WaitGroup
	wg.Add(7)
	go func() {
		defer wg.Done()
		total, totalErr = s.characters.Count(ctx, filter)
	}()
	go func() {
		defer wg.Done()
		active, activeErr = s.characters.Count(ctx, filterActive)
	}()
	go func() {
		defer wg.Done()
		new30d, new30dErr = s.achievements.CountChocoboMilestones(ctx, since30d, filter)
	}()
	go func() {
		defer wg.Done()
		races, racesErr = s.characters.Breakdown(ctx, "race", since, filter)
	}()
	go func() {
		defer wg.Done()
		msq, msqErr = s.achievements.CountExpansionsFiltered(ctx, filter)
	}()
	go func() {
		defer wg.Done()
		timeline, timelineErr = s.achievements.NewCharactersPerDay(ctx, since30d, now, filter)
	}()
	go func() {
		defer wg.Done()
		chars, listErr = s.characters.List(ctx, filter, 1, 0)
	}()
	wg.Wait()

	// Deterministic error precedence for the first six calls.
	if totalErr != nil {
		return nil, totalErr
	}
	if activeErr != nil {
		return nil, activeErr
	}
	if new30dErr != nil {
		return nil, new30dErr
	}
	if racesErr != nil {
		return nil, racesErr
	}
	if msqErr != nil {
		return nil, msqErr
	}
	if timelineErr != nil {
		return nil, timelineErr
	}

	dc := ""
	region := ""
	// Metadata lookup is non-fatal.
	if listErr == nil && len(chars) > 0 {
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
