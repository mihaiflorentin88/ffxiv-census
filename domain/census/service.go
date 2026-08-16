package census

import (
	"context"
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

// classJobKey returns a stable per-entry key: JobID when present, else ClassID
// (a base class has no job crystal, so JobID is 0).
func classJobKey(j *godestone.ClassJob) uint8 {
	if j.JobID != 0 {
		return j.JobID
	}
	return j.ClassID
}
