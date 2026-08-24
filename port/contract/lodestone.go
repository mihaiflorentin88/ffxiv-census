package contract

import (
	"context"
	"errors"
	"time"
)

// ErrCharacterNotFound is returned by LodestoneClient.FetchCharacter when a
// character ID does not exist on The Lodestone (HTTP 404).
var ErrCharacterNotFound = errors.New("lodestone character not found")

// ErrAchievementsPrivate is returned by LodestoneClient.FetchAchievements when
// a character's achievement list is hidden (HTTP 403 on the achievement page).
var ErrAchievementsPrivate = errors.New("lodestone achievements private")

// ErrFreeCompanyNotFound is returned by the legacy LodestoneClient when a free company
// ID does not exist on The Lodestone (HTTP 404). Deprecated: will be removed with the old client.
var ErrFreeCompanyNotFound = errors.New("lodestone free company not found")

// CharacterProfile holds the subset of Lodestone character data we persist.
type CharacterProfile struct {
	ID              uint32
	Name            string
	World           string
	Datacenter      string
	Gender          uint8
	Bio             string
	Race            string
	Tribe           string
	GrandCompany    string
	FreeCompanyID   string
	FreeCompanyName string
	ActiveJob       string
	ClassJobs       []ClassJobRecord
}

// AchievementResult holds the result of checking a single achievement.
type AchievementResult struct {
	AchievementID uint32
	Name          string
	Earned        bool
	EarnedAt      time.Time // zero if not earned
}

// AchievementSummary holds the result of a full milestone achievement check.
type AchievementSummary struct {
	Private           bool
	TotalAchievements uint32
	TotalPoints       uint32
	Milestones        []AchievementResult // only milestones that were checked
	LatestAchievement *AchievementResult  // latest earned tracked milestone checked in this request
}

// LodestoneClient reads character and achievement data from The Lodestone.
type LodestoneClient interface {
	FetchCharacter(ctx context.Context, id uint32) (*CharacterProfile, error)
	FetchAchievements(ctx context.Context, id uint32, milestoneIDs []uint32) (*AchievementSummary, error)
}
