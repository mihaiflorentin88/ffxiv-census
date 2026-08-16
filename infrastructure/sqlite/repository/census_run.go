package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CensusRunRepository is a SQLite implementation of contract.CensusRunRepository.
type CensusRunRepository struct {
	driver contract.SQLiteDriver
}

func NewCensusRunRepository(driver contract.SQLiteDriver) contract.CensusRunRepository {
	return &CensusRunRepository{driver: driver}
}

func (r *CensusRunRepository) Start(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(timeLayout)
	res, err := r.driver.Execute(ctx,
		`INSERT INTO census_runs (started_at) VALUES (?)`, now)
	if err != nil {
		return 0, fmt.Errorf("census run start: %w", err)
	}
	return res.LastInsertId()
}

func (r *CensusRunRepository) Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error {
	now := time.Now().UTC().Format(timeLayout)
	_, err := r.driver.Execute(ctx,
		`UPDATE census_runs SET finished_at = ?, characters_seen = ?, new_characters = ?
		  WHERE id = ?`, now, charactersSeen, newCharacters, id)
	if err != nil {
		return fmt.Errorf("census run finish: %w", err)
	}
	return nil
}
