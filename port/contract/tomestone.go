package contract

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrTomestoneUnauthenticated is returned when the Tomestone API returns 401 Unauthenticated
	// due to a missing or invalid Laravel Sanctum Bearer token.
	ErrTomestoneUnauthenticated = errors.New("tomestone api unauthenticated: missing or invalid bearer token")

	// ErrTomestoneDisabled is returned when attempting to call Tomestone API without configuration.
	ErrTomestoneDisabled = errors.New("tomestone api is disabled or unconfigured")
)

// TomestoneCharacter is the domain representation of a character profile returned by Tomestone.gg.
type TomestoneCharacter struct {
	ID              uint32
	Name            string
	Server          string
	Datacenter      string
	Gender          string
	Race            string
	Tribe           string
	Title           string
	GrandCompany    string
	FreeCompanyID   *string
	FreeCompanyName *string
	Bio             string
	ActiveJob       string
	Jobs            []TomestoneClassJob
	Gear            []TomestoneGear
	UpdatedAt       time.Time
}

// TomestoneClassJob represents a job or class level for a Tomestone character.
type TomestoneClassJob struct {
	ID     uint8
	Name   string
	Abbr   string
	Role   string
	Level  uint8
	Exp    uint32
	ExpMax uint32
}

// TomestoneGear represents an equipped item on a character.
type TomestoneGear struct {
	Slot      string
	ID        uint32
	Name      string
	ItemLevel int
	Dye       *string
	Materia   []string
}

// TomestoneClient reads character profile and gear data from tomestone.gg REST API.
type TomestoneClient interface {
	// FetchCharacterProfile fetches a character's profile by their Lodestone ID.
	// If update is true, it requests an on-demand update from Lodestone.
	FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*TomestoneCharacter, error)

	// FetchCharacterProfileByName fetches a character's profile by server and character name.
	// If update is true, it requests an on-demand update from Lodestone.
	FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*TomestoneCharacter, error)

	// IsConfigured returns true if the client has a non-empty API token configured.
	IsConfigured() bool
}
