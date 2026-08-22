// Package lodestone provides an in-memory LodestoneClient fake for tests.
//
// A NewFake() instance returns zero-values for every method until a *Func
// field is set; set a field to return canned godestone DTOs or an error.
// The matching Calls slice records the ids passed to each method.
package lodestone

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/xivapi/godestone/v2"
)

// Fake is an in-memory LodestoneClient for tests. Set a *Func field to return
// canned godestone DTOs or an error; the corresponding Calls slice records ids.
type Fake struct {
	mu                          sync.Mutex
	FetchCharacterFunc          func(id uint32) (*godestone.Character, error)
	FetchAchievementsFunc       func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)
	FetchFreeCompanyFunc        func(id string) (*godestone.FreeCompany, error)
	FetchFreeCompanyMembersFunc func(fcID string) ([]uint32, error)
	CharacterCalls              []uint32
	AchievementsCalls           []uint32
	FreeCompanyCalls            []string
	FreeCompanyMembersCalls     []string
	StopFn                      func([]*godestone.AchievementInfo) bool // captured by SetAchievementStopFn
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) FetchCharacter(ctx context.Context, id uint32) (*godestone.Character, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CharacterCalls = append(f.CharacterCalls, id)
	if f.FetchCharacterFunc == nil {
		return nil, nil
	}
	return f.FetchCharacterFunc(id)
}

func (f *Fake) FetchAchievements(ctx context.Context, id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AchievementsCalls = append(f.AchievementsCalls, id)
	if f.FetchAchievementsFunc == nil {
		return nil, nil, nil
	}
	return f.FetchAchievementsFunc(id)
}

func (f *Fake) FetchFreeCompany(ctx context.Context, id string) (*godestone.FreeCompany, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FreeCompanyCalls = append(f.FreeCompanyCalls, id)
	if f.FetchFreeCompanyFunc == nil {
		return nil, nil
	}
	return f.FetchFreeCompanyFunc(id)
}

func (f *Fake) FetchFreeCompanyMembers(ctx context.Context, fcID string) ([]uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FreeCompanyMembersCalls = append(f.FreeCompanyMembersCalls, fcID)
	if f.FetchFreeCompanyMembersFunc == nil {
		return nil, nil
	}
	return f.FetchFreeCompanyMembersFunc(fcID)
}

func (f *Fake) SetAchievementStopFn(fn func([]*godestone.AchievementInfo) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StopFn = fn
}

// GetStopFn returns the currently set stop function (thread-safe).
func (f *Fake) GetStopFn() func([]*godestone.AchievementInfo) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.StopFn
}

var _ contract.LodestoneClient = (*Fake)(nil)
