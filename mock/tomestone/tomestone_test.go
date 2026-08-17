package tomestone

import (
	"context"
	"errors"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestFake_FetchCharacterProfile(t *testing.T) {
	fake := NewFake()
	ctx := context.Background()

	// Initially not found
	_, err := fake.FetchCharacterProfile(ctx, 123, false)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("err = %v, want ErrCharacterNotFound", err)
	}

	char := &contract.TomestoneCharacter{
		ID:     123,
		Name:   "Alphinaud Leveilleur",
		Server: "Balmung",
	}
	fake.SetCharacter(char)

	got, err := fake.FetchCharacterProfile(ctx, 123, true)
	if err != nil {
		t.Fatalf("FetchCharacterProfile: %v", err)
	}
	if got.ID != 123 || got.Name != "Alphinaud Leveilleur" {
		t.Errorf("got = %+v, want char", got)
	}
	if len(fake.ProfileCalls) != 2 || fake.ProfileCalls[1] != 123 {
		t.Errorf("ProfileCalls = %v, want [123, 123]", fake.ProfileCalls)
	}

	// By name
	gotByName, err := fake.FetchCharacterProfileByName(ctx, "balmung", "alphinaud leveilleur", false)
	if err != nil {
		t.Fatalf("FetchCharacterProfileByName: %v", err)
	}
	if gotByName.ID != 123 {
		t.Errorf("gotByName.ID = %d, want 123", gotByName.ID)
	}

	// Unconfigured
	fake.Configured = false
	_, err = fake.FetchCharacterProfile(ctx, 123, false)
	if !errors.Is(err, contract.ErrTomestoneDisabled) {
		t.Fatalf("err = %v, want ErrTomestoneDisabled", err)
	}
}

func TestFake_CustomFunc(t *testing.T) {
	fake := NewFake()
	customErr := errors.New("custom error")
	fake.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, customErr
	}

	_, err := fake.FetchCharacterProfile(context.Background(), 999, false)
	if !errors.Is(err, customErr) {
		t.Fatalf("err = %v, want custom error", err)
	}
}
