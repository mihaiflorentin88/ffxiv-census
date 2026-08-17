// Package tomestone provides an in-memory TomestoneClient fake for tests.
package tomestone

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Fake is an in-memory TomestoneClient for tests.
type Fake struct {
	mu                              sync.Mutex
	Configured                      bool
	Characters                      map[uint32]*contract.TomestoneCharacter
	FetchCharacterProfileFunc       func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error)
	FetchCharacterProfileByNameFunc func(ctx context.Context, server, name string, update bool) (*contract.TomestoneCharacter, error)
	ProfileCalls                    []uint32
	ProfileByNameCalls              []string
}

// NewFake returns a new configured Fake TomestoneClient.
func NewFake() *Fake {
	return &Fake{
		Configured: true,
		Characters: make(map[uint32]*contract.TomestoneCharacter),
	}
}

// IsConfigured reports whether the fake is configured.
func (f *Fake) IsConfigured() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Configured
}

// SetCharacter stores a character in the fake's in-memory store.
func (f *Fake) SetCharacter(char *contract.TomestoneCharacter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if char != nil {
		f.Characters[char.ID] = char
	}
}

// FetchCharacterProfile fetches character profile from the fake.
func (f *Fake) FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProfileCalls = append(f.ProfileCalls, id)

	if f.FetchCharacterProfileFunc != nil {
		return f.FetchCharacterProfileFunc(ctx, id, update)
	}

	if !f.Configured {
		return nil, contract.ErrTomestoneDisabled
	}

	char, ok := f.Characters[id]
	if !ok {
		return nil, contract.ErrCharacterNotFound
	}
	return char, nil
}

// FetchCharacterProfileByName fetches character profile by server and name from the fake.
func (f *Fake) FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*contract.TomestoneCharacter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%s/%s", server, name)
	f.ProfileByNameCalls = append(f.ProfileByNameCalls, key)

	if f.FetchCharacterProfileByNameFunc != nil {
		return f.FetchCharacterProfileByNameFunc(ctx, server, name, update)
	}

	if !f.Configured {
		return nil, contract.ErrTomestoneDisabled
	}

	for _, char := range f.Characters {
		if strings.EqualFold(char.Server, server) && strings.EqualFold(char.Name, name) {
			return char, nil
		}
	}
	return nil, contract.ErrCharacterNotFound
}

var _ contract.TomestoneClient = (*Fake)(nil)
