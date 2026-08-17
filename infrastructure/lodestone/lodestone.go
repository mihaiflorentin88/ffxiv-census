// Package lodestone adapts godestone v2 (bingode data provider, EN locale)
// to the contract.LodestoneClient port, adding token-bucket rate limiting
// and exponential-backoff retries around every scrape.
//
// ctx is honored only at the limiter/backoff/retry boundaries: godestone's
// methods take no ctx and its colly collectors expose no HTTP timeout, so an
// in-flight request cannot be cancelled (see docs/lodestone.md).
package lodestone

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/karashiiro/bingode"
	"github.com/xivapi/godestone/v2"
	"golang.org/x/time/rate"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// scraper is the unexported seam over godestone's *Scraper, satisfied
// structurally, so tests can inject a fake without network access.
type scraper interface {
	FetchCharacter(id uint32) (*godestone.Character, error)
	FetchCharacterAchievements(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)
	FetchFreeCompany(id string) (*godestone.FreeCompany, error)
}

// Client is a contract.LodestoneClient backed by godestone.
type Client struct {
	scraper     scraper
	limiter     *rate.Limiter
	maxRetries  int
	backoffBase time.Duration
}

// NewClient builds the real godestone scraper (bingode provider, EN locale).
func NewClient(cfg *config.LodestoneConfig) (contract.LodestoneClient, error) {
	return newClient(godestone.NewScraper(bingode.New(), godestone.EN), cfg)
}

func newClient(sc scraper, cfg *config.LodestoneConfig) (*Client, error) {
	if sc == nil {
		return nil, errors.New("lodestone scraper is nil")
	}
	if cfg == nil {
		return nil, errors.New("lodestone config is nil")
	}
	rps := cfg.RateLimit
	if rps <= 0 {
		rps = 1.0
	}
	return &Client{
		scraper:     sc,
		limiter:     rate.NewLimiter(rate.Limit(rps), 1),
		maxRetries:  cfg.MaxRetries,
		backoffBase: 500 * time.Millisecond,
	}, nil
}

// fetchCharacter runs one scrape attempt. Attempts are counted from 0 to
// maxRetries inclusive (total attempts = maxRetries + 1); between attempts the
// client sleeps backoffBase·2^attempt and re-waits on the limiter.
func (c *Client) FetchCharacter(ctx context.Context, id uint32) (*godestone.Character, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		char, err := c.scraper.FetchCharacter(id)
		if err == nil {
			return char, nil
		}
		if isNotFound(err) {
			return nil, contract.ErrCharacterNotFound
		}
		lastErr = err
		if attempt < c.maxRetries {
			backoff := c.backoffBase * time.Duration(1<<uint(attempt))
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("fetch character %d: %w", id, lastErr)
}

// FetchAchievements mirrors FetchCharacter for a character's achievements.
func (c *Client) FetchAchievements(ctx context.Context, id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, nil, err
		}
		list, all, err := c.scraper.FetchCharacterAchievements(id)
		if err == nil {
			return list, all, nil
		}
		lastErr = err
		if attempt < c.maxRetries {
			backoff := c.backoffBase * time.Duration(1<<uint(attempt))
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, nil, fmt.Errorf("fetch achievements %d: %w", id, lastErr)
}

// FetchFreeCompany mirrors FetchCharacter for a free company (ID is the
// 19-digit Lodestone FC string, not a numeric character id).
func (c *Client) FetchFreeCompany(ctx context.Context, id string) (*godestone.FreeCompany, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		fc, err := c.scraper.FetchFreeCompany(id)
		if err == nil {
			return fc, nil
		}
		lastErr = err
		if attempt < c.maxRetries {
			backoff := c.backoffBase * time.Duration(1<<uint(attempt))
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("fetch free company %s: %w", id, lastErr)
}

// isNotFound reports whether a godestone scrape error is an HTTP 404. godestone's
// character collector forwards colly errors verbatim, and colly surfaces a 404 as
// http.StatusText(404) == "Not Found".
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), http.StatusText(http.StatusNotFound))
}
