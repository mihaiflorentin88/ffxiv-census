package lodestone

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/provider/models"
)

var errScraper = errors.New("scraper boom")

// fakeScraper implements the unexported scraper seam; each method delegates to
// a func field so tests control behavior and count calls.
type fakeScraper struct {
	charCalls int
	achCalls  int
	fcCalls   int
	fetchChar func(id uint32) (*godestone.Character, error)
	fetchAch  func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)
	fetchFC   func(id string) (*godestone.FreeCompany, error)
}

func (f *fakeScraper) FetchCharacter(id uint32) (*godestone.Character, error) {
	f.charCalls++
	return f.fetchChar(id)
}

func (f *fakeScraper) FetchCharacterAchievements(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
	f.achCalls++
	return f.fetchAch(id)
}

func (f *fakeScraper) FetchFreeCompany(id string) (*godestone.FreeCompany, error) {
	f.fcCalls++
	return f.fetchFC(id)
}

// fastClient builds a client wired to sc with backoff disabled and a high rate
// limit so tests run instantly.
func fastClient(sc scraper, maxRetries int) *Client {
	c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 1000, MaxRetries: maxRetries})
	if err != nil {
		panic(err)
	}
	c.backoffBase = 0
	return c
}

func TestFetchCharacterPassthrough(t *testing.T) {
	want := &godestone.Character{ID: 123, Name: "Test Char"}
	sc := &fakeScraper{fetchChar: func(id uint32) (*godestone.Character, error) { return want, nil }}
	c := fastClient(sc, 0)

	got, err := c.FetchCharacter(context.Background(), 123)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got != want {
		t.Errorf("character = %v, want the scraper's instance unchanged", got)
	}
	if sc.charCalls != 1 {
		t.Errorf("scraper calls = %d, want 1", sc.charCalls)
	}
}

func TestFetchCharacterRetriesThenSucceeds(t *testing.T) {
	calls := 0
	sc := &fakeScraper{fetchChar: func(id uint32) (*godestone.Character, error) {
		calls++
		if calls == 1 {
			return nil, errScraper
		}
		return &godestone.Character{ID: id}, nil
	}}
	c := fastClient(sc, 1)

	got, err := c.FetchCharacter(context.Background(), 7)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("character id = %d, want 7", got.ID)
	}
	if calls != 2 {
		t.Errorf("scraper calls = %d, want 2 (fail then succeed)", calls)
	}
}

func TestFetchCharacterRetriesExhausted(t *testing.T) {
	sc := &fakeScraper{fetchChar: func(id uint32) (*godestone.Character, error) { return nil, errScraper }}
	c := fastClient(sc, 2)

	got, err := c.FetchCharacter(context.Background(), 7)
	if got != nil {
		t.Errorf("character = %v, want nil on failure", got)
	}
	if !errors.Is(err, errScraper) {
		t.Errorf("error = %v, want it to wrap errScraper", err)
	}
	if sc.charCalls != 3 {
		t.Errorf("scraper calls = %d, want 3 (maxRetries=2 → 3 attempts)", sc.charCalls)
	}
}

func TestFetchFreeCompanyAndAchievements(t *testing.T) {
	fcWant := &godestone.FreeCompany{ID: "9234567890123456789", Name: "Test FC"}
	fcSc := &fakeScraper{fetchFC: func(id string) (*godestone.FreeCompany, error) { return fcWant, nil }}
	fcClient := fastClient(fcSc, 0)

	fcGot, err := fcClient.FetchFreeCompany(context.Background(), "9234567890123456789")
	if err != nil {
		t.Fatalf("fetch fc: %v", err)
	}
	if fcGot != fcWant {
		t.Errorf("fc = %v, want the scraper's instance unchanged", fcGot)
	}
	if fcSc.fcCalls != 1 {
		t.Errorf("fc scraper calls = %d, want 1", fcSc.fcCalls)
	}

	listWant := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 1, Name: "One"}},
		{NamedEntity: &models.NamedEntity{ID: 2, Name: "Two"}},
	}
	allWant := &godestone.AllAchievementInfo{Private: true}
	achSc := &fakeScraper{fetchAch: func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		return listWant, allWant, nil
	}}
	achClient := fastClient(achSc, 0)

	listGot, allGot, err := achClient.FetchAchievements(context.Background(), 42)
	if err != nil {
		t.Fatalf("fetch achievements: %v", err)
	}
	if len(listGot) != 2 || listGot[0] != listWant[0] || listGot[1] != listWant[1] {
		t.Errorf("achievement list = %v, want the scraper's instances unchanged", listGot)
	}
	if allGot != allWant {
		t.Errorf("all achievements = %v, want the scraper's instance unchanged", allGot)
	}
	if achSc.achCalls != 1 {
		t.Errorf("achievements scraper calls = %d, want 1", achSc.achCalls)
	}
}

func TestNewClientRateLimit(t *testing.T) {
	c, err := newClient(&fakeScraper{}, &config.LodestoneConfig{RateLimit: 2.5, MaxRetries: 1})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if got := c.limiter.Limit(); got != 2.5 {
		t.Errorf("limiter limit = %v, want 2.5", got)
	}
	if got := c.limiter.Burst(); got != 1 {
		t.Errorf("limiter burst = %d, want 1", got)
	}
}

func TestNewClientNilChecks(t *testing.T) {
	if _, err := newClient(nil, &config.LodestoneConfig{}); err == nil {
		t.Error("newClient(nil, cfg) expected an error")
	}
	if _, err := newClient(&fakeScraper{}, nil); err == nil {
		t.Error("newClient(sc, nil) expected an error")
	}
}

func TestFetchCharacterNotFound(t *testing.T) {
	sc := &fakeScraper{fetchChar: func(id uint32) (*godestone.Character, error) {
		return nil, errors.New(http.StatusText(http.StatusNotFound))
	}}
	c := fastClient(sc, 3)

	got, err := c.FetchCharacter(context.Background(), 99999999)
	if got != nil {
		t.Errorf("character = %v, want nil on 404", got)
	}
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Errorf("error = %v, want ErrCharacterNotFound", err)
	}
	// A 404 is not transient: it must not retry.
	if sc.charCalls != 1 {
		t.Errorf("scraper calls = %d, want 1 (404 must not retry)", sc.charCalls)
	}
}
