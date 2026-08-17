package contract

import (
	"context"
	"errors"

	"github.com/xivapi/godestone/v2"
)

// ErrCharacterNotFound is returned by LodestoneClient.FetchCharacter when a
// character ID does not exist on The Lodestone (HTTP 404).
var ErrCharacterNotFound = errors.New("lodestone character not found")

// LodestoneClient reads character, achievement, and free-company data from
// The Lodestone. Returned types are godestone's model types: the adapter wraps
// godestone directly (mirroring how SQLiteDriver exposes *sql.DB).
type LodestoneClient interface {
	FetchCharacter(ctx context.Context, id uint32) (*godestone.Character, error)
	FetchAchievements(ctx context.Context, id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)
	FetchFreeCompany(ctx context.Context, id string) (*godestone.FreeCompany, error)
}
