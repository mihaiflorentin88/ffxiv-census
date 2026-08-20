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
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/karashiiro/bingode"
	"github.com/xivapi/godestone/v2"
	"golang.org/x/time/rate"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type scraper interface {
	FetchCharacter(id uint32) (*godestone.Character, error)
	FetchCharacterAchievements(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)
	FetchFreeCompany(id string) (*godestone.FreeCompany, error)
}

type httpGetter interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a contract.LodestoneClient backed by godestone.
type Client struct {
	scraper     scraper
	httpClient  httpGetter
	limiter     *rate.Limiter
	maxRetries  int
	backoffBase time.Duration
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

// per second). Lodestone is Cloudflare-fronted; 1 req/s is the established safe
// pace for FFXIV tooling. Higher rates risk HTTP 429s and IP bans. The
// configured rate is clamped to this ceiling.
const maxSafeRate = 1.0

// NewClient builds the real godestone scraper (bingode provider, EN locale).
func NewClient(cfg *config.LodestoneConfig, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return newClient(godestone.NewScraper(bingode.New(), godestone.EN), cfg, logger, rl)
}

// NewClientWithProxy creates a LodestoneClient that routes ALL requests
// (including godestone scraper calls) through the given proxy URL.
// The proxyURL must include the protocol (http://, socks5://).
// Uses the forked godestone with protocol-aware proxy support.
func NewClientWithProxy(cfg *config.LodestoneConfig, proxyURL string, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	sc := godestone.NewScraper(bingode.New(), godestone.EN, godestone.WithProxy(proxyURL))
	return newClient(sc, cfg, logger, rl)
}

func newClient(sc scraper, cfg *config.LodestoneConfig, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) (*Client, error) {
	if sc == nil {
		return nil, errors.New("lodestone scraper is nil")
	}
	if cfg == nil {
		return nil, errors.New("lodestone config is nil")
	}
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	rps := cfg.RateLimit
	if rps <= 0 {
		rps = maxSafeRate
	}
	if rps > maxSafeRate {
		rps = maxSafeRate
	}
	return &Client{
		scraper:     sc,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		limiter:     rate.NewLimiter(rate.Limit(rps), 1),
		maxRetries:  cfg.MaxRetries,
		backoffBase: 500 * time.Millisecond,
		logger:      loggerOrDiscard(logger),
		rateLimiter: rl,
	}, nil
}

func loggerOrDiscard(l contract.Logger) contract.Logger {
	if l == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return l
}

func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	// +/- 10% jitter: [0.9, 1.1] * d
	factor := 0.9 + 0.2*rand.Float64()
	return time.Duration(float64(d) * factor)
}

// fetchCharacter runs one scrape attempt. Attempts are counted from 0 to
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
			c.logger.DebugContext(ctx, "lodestone.fetch_character.success", slog.Uint64("id", uint64(id)), slog.Int("attempt", attempt+1))
			return char, nil
		}
		if isNotFound(err) {
			c.logger.DebugContext(ctx, "lodestone.not_found", slog.Uint64("id", uint64(id)), slog.String("reason", err.Error()))
			return nil, contract.ErrCharacterNotFound
		}
		lastErr = err
		backoff := c.backoffBase * time.Duration(1<<uint(attempt))
		if isRateLimited(err) {
			if c.rateLimiter != nil {
				c.rateLimiter.Pause(contract.ProviderLodestone, 30*time.Second, "lodestone 429 / rate limited")
			}
			c.logger.WarnContext(ctx, "lodestone.rate_limited",
				slog.Uint64("id", uint64(id)),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		} else {
			c.logger.WarnContext(ctx, "lodestone.scrape_retry",
				slog.Uint64("id", uint64(id)),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		}
		if attempt < c.maxRetries {
			timer := time.NewTimer(jittered(backoff))
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
			c.logger.DebugContext(ctx, "lodestone.fetch_achievements.success", slog.Uint64("id", uint64(id)), slog.Int("attempt", attempt+1))
			return list, all, nil
		}
		lastErr = err
		if isNotFound(err) {
			c.logger.InfoContext(ctx, "lodestone.fetch_achievements.not_found", slog.Uint64("id", uint64(id)))
			return nil, nil, contract.ErrCharacterNotFound
		}
		backoff := c.backoffBase * time.Duration(1<<uint(attempt))
		if isRateLimited(err) {
			if c.rateLimiter != nil {
				c.rateLimiter.Pause(contract.ProviderLodestone, 30*time.Second, "lodestone 429 / rate limited")
			}
			c.logger.WarnContext(ctx, "lodestone.rate_limited",
				slog.Uint64("id", uint64(id)),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		} else {
			c.logger.WarnContext(ctx, "lodestone.scrape_retry",
				slog.Uint64("id", uint64(id)),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		}
		if attempt < c.maxRetries {
			timer := time.NewTimer(jittered(backoff))
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
			c.logger.DebugContext(ctx, "lodestone.fetch_free_company.success", slog.String("id", id), slog.Int("attempt", attempt+1))
			return fc, nil
		}
		if isNotFound(err) {
			c.logger.InfoContext(ctx, "lodestone.fetch_free_company.not_found", slog.String("id", id))
			return nil, contract.ErrFreeCompanyNotFound
		}
		lastErr = err
		backoff := c.backoffBase * time.Duration(1<<uint(attempt))
		if isRateLimited(err) {
			if c.rateLimiter != nil {
				c.rateLimiter.Pause(contract.ProviderLodestone, 30*time.Second, "lodestone 429 / rate limited")
			}
			c.logger.WarnContext(ctx, "lodestone.rate_limited",
				slog.String("id", id),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		} else {
			c.logger.WarnContext(ctx, "lodestone.scrape_retry",
				slog.String("id", id),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		}
		if attempt < c.maxRetries {
			timer := time.NewTimer(jittered(backoff))
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

var fcMemberRegex = regexp.MustCompile(`/lodestone/character/(\d+)/`)

// FetchFreeCompanyMembers scrapes all member character IDs for a free company.
func (c *Client) FetchFreeCompanyMembers(ctx context.Context, id string) ([]uint32, error) {
	var lastErr error
	url := fmt.Sprintf("https://na.finalfantasyxiv.com/lodestone/freecompany/%s/member/", id)
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FFXIV-Census-Engine/1.0)")
		resp, err := c.httpClient.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
				_ = resp.Body.Close()
				return nil, contract.ErrFreeCompanyNotFound
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				_ = resp.Body.Close()
				err = errors.New("429 Too Many Requests")
			} else if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				err = fmt.Errorf("unexpected status %d", resp.StatusCode)
			} else {
				bodyBytes, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					err = fmt.Errorf("read body: %w", readErr)
				} else {
					matches := fcMemberRegex.FindAllStringSubmatch(string(bodyBytes), -1)
					seen := make(map[uint32]bool)
					var members []uint32
					for _, m := range matches {
						if len(m) > 1 {
							if parsed, parseErr := strconv.ParseUint(m[1], 10, 32); parseErr == nil {
								uid := uint32(parsed)
								if !seen[uid] {
									seen[uid] = true
									members = append(members, uid)
								}
							}
						}
					}
					c.logger.DebugContext(ctx, "lodestone.fetch_free_company_members.success", slog.String("id", id), slog.Int("members_count", len(members)), slog.Int("attempt", attempt+1))
					return members, nil
				}
			}
		}
		lastErr = err
		backoff := c.backoffBase * time.Duration(1<<uint(attempt))
		if isRateLimited(err) {
			c.logger.WarnContext(ctx, "lodestone.rate_limited",
				slog.String("id", id),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		} else {
			c.logger.WarnContext(ctx, "lodestone.scrape_retry",
				slog.String("id", id),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
		}
		if attempt < c.maxRetries {
			timer := time.NewTimer(jittered(backoff))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("fetch free company members %s: %w", id, lastErr)
}

// isNotFound reports whether a godestone scrape error indicates the resource
// does not exist on Lodestone (HTTP 404 "Not Found" or HTTP 403 "Forbidden").
// Lodestone returns 403 for deleted/banned/terminated legacy character profiles.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, http.StatusText(http.StatusNotFound)) ||
		strings.Contains(msg, http.StatusText(http.StatusForbidden))
}

func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "Too Many Requests") ||
		strings.Contains(msg, http.StatusText(http.StatusTooManyRequests))
}
