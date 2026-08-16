package contract

import "context"

// CensusRunRepository records census sweeps for operational tracking.
type CensusRunRepository interface {
	// Start creates a run and returns its ID.
	Start(ctx context.Context) (int64, error)
	// Finish records completion with per-run counters.
	Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error
}
