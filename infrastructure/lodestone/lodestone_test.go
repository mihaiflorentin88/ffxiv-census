package lodestone

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/time/rate"

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
	c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 1000, MaxRetries: maxRetries}, nil)
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

func TestNewClient_ClampsRateToMaxSafe(t *testing.T) {
	sc := &fakeScraper{}
	t.Run("above cap clamps", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 50.0}, nil)
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(maxSafeRate) {
			t.Fatalf("limiter rate = %v, want %v (clamped)", got, maxSafeRate)
		}
	})
	t.Run("below cap respected", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 0.5}, nil)
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(0.5) {
			t.Fatalf("limiter rate = %v, want 0.5", got)
		}
	})
	t.Run("zero defaults to cap", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 0}, nil)
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(maxSafeRate) {
			t.Fatalf("limiter rate = %v, want %v (default)", got, maxSafeRate)
		}
	})
}

func TestNewClientNilChecks(t *testing.T) {
	if _, err := newClient(nil, &config.LodestoneConfig{}, nil); err == nil {
		t.Error("newClient(nil, cfg) expected an error")
	}
	if _, err := newClient(&fakeScraper{}, nil, nil); err == nil {
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

func TestClient_FetchCharacter_ForbiddenTreatedAsNotFound(t *testing.T) {
	sc := &fakeScraper{
		fetchChar: func(id uint32) (*godestone.Character, error) {
			return nil, errors.New("fetch character 75: Forbidden")
		},
	}
	c := fastClient(sc, 3)
	_, err := c.FetchCharacter(context.Background(), 75)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("err = %v, want ErrCharacterNotFound", err)
	}
	if sc.charCalls != 1 {
		t.Errorf("scraper calls = %d, want 1 (403 must not retry)", sc.charCalls)
	}
}

func TestClient_LogsRateLimitedWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	attempts := 0
	sc := &fakeScraper{
		fetchChar: func(id uint32) (*godestone.Character, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("HTTP 429 Too Many Requests")
			}
			return &godestone.Character{ID: id, Name: "Recovered"}, nil
		},
	}
	c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 1000, MaxRetries: 2}, logger)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	c.backoffBase = 0

	got, err := c.FetchCharacter(context.Background(), 100)
	if err != nil {
		t.Fatalf("FetchCharacter: %v", err)
	}
	if got.Name != "Recovered" {
		t.Fatalf("got name = %s, want Recovered", got.Name)
	}
	logs := buf.String()
	for _, want := range []string{"lodestone.rate_limited", "WARN", "id=100", "attempt=1", "max_attempts=3"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestClient_LogsScrapeRetryWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	attempts := 0
	sc := &fakeScraper{
		fetchChar: func(id uint32) (*godestone.Character, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("connection reset by peer")
			}
			return &godestone.Character{ID: id, Name: "Recovered"}, nil
		},
	}
	c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 1000, MaxRetries: 2}, logger)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	c.backoffBase = 0

	got, err := c.FetchCharacter(context.Background(), 200)
	if err != nil {
		t.Fatalf("FetchCharacter: %v", err)
	}
	if got.Name != "Recovered" {
		t.Fatalf("got name = %s, want Recovered", got.Name)
	}
	logs := buf.String()
	for _, want := range []string{"lodestone.scrape_retry", "WARN", "id=200", "attempt=1", "max_attempts=3", "connection reset by peer"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

type fakeHTTPGetter struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (f *fakeHTTPGetter) Do(req *http.Request) (*http.Response, error) {
	return f.doFunc(req)
}

func TestClient_FetchFreeCompanyMembers(t *testing.T) {
	html := `
		<html>
		<body>
			<div class="entry">
				<a href="/lodestone/character/12345/">Character One</a>
				<a href="/lodestone/character/67890/">Character Two</a>
				<a href="/lodestone/character/12345/">Duplicate Character</a>
			</div>
		</body>
		</html>
	`
	sc := &fakeScraper{}
	c := fastClient(sc, 0)
	c.httpClient = &fakeHTTPGetter{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(html)),
			}, nil
		},
	}

	members, err := c.FetchFreeCompanyMembers(context.Background(), "9234567890123456789")
	if err != nil {
		t.Fatalf("FetchFreeCompanyMembers: %v", err)
	}
	if len(members) != 2 || members[0] != 12345 || members[1] != 67890 {
		t.Errorf("members = %v, want [12345, 67890]", members)
	}
}

func TestClient_FetchFreeCompanyMembers_NotFound(t *testing.T) {
	sc := &fakeScraper{}
	c := fastClient(sc, 0)
	c.httpClient = &fakeHTTPGetter{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	}

	_, err := c.FetchFreeCompanyMembers(context.Background(), "9234567890123456789")
	if !errors.Is(err, contract.ErrFreeCompanyNotFound) {
		t.Fatalf("err = %v, want ErrFreeCompanyNotFound", err)
	}
}

func TestFetchFreeCompany_NotFound_ReturnsSentinelWithoutRetry(t *testing.T) {
	sc := &fakeScraper{
		fetchFC: func(id string) (*godestone.FreeCompany, error) {
			return nil, errors.New("404 Not Found")
		},
	}
	c := fastClient(sc, 3)
	_, err := c.FetchFreeCompany(context.Background(), "9234567890123456789")
	if !errors.Is(err, contract.ErrFreeCompanyNotFound) {
		t.Fatalf("expected ErrFreeCompanyNotFound, got: %v", err)
	}
	if sc.fcCalls != 1 {
		t.Errorf("fcCalls = %d, want 1 (fast-fail on 404 without retries)", sc.fcCalls)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read stream broken")
}
func (errReader) Close() error { return nil }

func TestFetchFreeCompanyMembers_BodyReadError_Retries(t *testing.T) {
	sc := &fakeScraper{}
	c := fastClient(sc, 2)
	calls := 0
	c.httpClient = &fakeHTTPGetter{
		doFunc: func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReader{},
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<a href="/lodestone/character/111/">One</a>`)),
			}, nil
		},
	}

	members, err := c.FetchFreeCompanyMembers(context.Background(), "123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 1 || members[0] != 111 {
		t.Errorf("members = %v, want [111]", members)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}
