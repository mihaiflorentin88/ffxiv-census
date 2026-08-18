package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CensusRunRepository is a PostgreSQL implementation of contract.CensusRunRepository.
type CensusRunRepository struct {
	driver contract.DatabaseDriver
}

func NewCensusRunRepository(driver contract.DatabaseDriver) contract.CensusRunRepository {
	return &CensusRunRepository{driver: driver}
}

func (r *CensusRunRepository) Start(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	row, err := r.driver.FetchOne(ctx,
		`INSERT INTO census_runs (started_at) VALUES ($1) RETURNING id`, now)
	if err != nil {
		return 0, fmt.Errorf("census run start: %w", err)
	}
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, fmt.Errorf("census run scan id: %w", err)
	}
	return id, nil
}

func (r *CensusRunRepository) Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error {
	now := time.Now().UTC()
	_, err := r.driver.Execute(ctx,
		`UPDATE census_runs SET finished_at = $1, characters_seen = $2, new_characters = $3
		  WHERE id = $4`, now, charactersSeen, newCharacters, id)
	if err != nil {
		return fmt.Errorf("census run finish: %w", err)
	}
	return nil
}
