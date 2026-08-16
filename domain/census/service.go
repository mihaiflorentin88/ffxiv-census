package census

import (
	"context"
	"errors"
	"time"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service is the domain brain of the census. It converts Lodestone DTOs into
// persisted records and computes milestone/activity facts. It depends only on
// contracts, never on SQL or HTTP.
type Service struct {
	characters    contract.CharacterRepository
	freeCompanies contract.FreeCompanyRepository
	achievements  contract.AchievementRepository
	censusRuns    contract.CensusRunRepository
}

func NewService(
	characters contract.CharacterRepository,
	freeCompanies contract.FreeCompanyRepository,
	achievements contract.AchievementRepository,
	censusRuns contract.CensusRunRepository,
) *Service {
	return &Service{
		characters:    characters,
		freeCompanies: freeCompanies,
		achievements:  achievements,
		censusRuns:    censusRuns,
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
	rec := toCharacterRecord(char)
	jobs := toJobRecords(char)
	return s.characters.Upsert(ctx, rec, jobs)
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
// census activity window (default 30 days).
func (s *Service) IsActive(latestAt time.Time) bool {
	return !latestAt.IsZero() && time.Since(latestAt) <= defaultActivityWindow
}
