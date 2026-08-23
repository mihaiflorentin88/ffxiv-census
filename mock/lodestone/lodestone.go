// Package lodestone provides an in-memory LodestoneClient fake for tests.
//
// A NewFake() instance returns zero-values for every method until a *Func
// field is set; set a field to return canned data or an error.
// The matching Calls slice records the ids passed to each method.
package lodestone

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Fake is an in-memory LodestoneClient for tests. Set a *Func field to return
// canned data or an error; the corresponding Calls slice records ids.
type Fake struct {
	mu                    sync.Mutex
	FetchCharacterFunc    func(ctx context.Context, id uint32) (*contract.CharacterProfile, error)
	FetchAchievementsFunc func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error)
	CharacterCalls        []uint32
	AchievementsCalls     []uint32
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) FetchCharacter(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CharacterCalls = append(f.CharacterCalls, id)
	if f.FetchCharacterFunc == nil {
		return nil, nil
	}
	return f.FetchCharacterFunc(ctx, id)
}

func (f *Fake) FetchAchievements(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AchievementsCalls = append(f.AchievementsCalls, id)
	if f.FetchAchievementsFunc == nil {
		return nil, nil
	}
	return f.FetchAchievementsFunc(ctx, id, milestoneIDs)
}

var _ contract.LodestoneClient = (*Fake)(nil)
