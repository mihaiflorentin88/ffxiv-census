package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CensusRunRepository is an in-memory fake.
type CensusRunRepository struct {
	mu       sync.Mutex
	nextID   int64
	started  []int64
	finished map[int64][2]int // id -> {charactersSeen, newCharacters}
}

func NewCensusRunFake() *CensusRunRepository {
	return &CensusRunRepository{nextID: 1, finished: map[int64][2]int{}}
}

func (f *CensusRunRepository) Start(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	f.nextID++
	f.started = append(f.started, id)
	return id, nil
}

func (f *CensusRunRepository) Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished[id] = [2]int{charactersSeen, newCharacters}
	return nil
}

var _ contract.CensusRunRepository = (*CensusRunRepository)(nil)
