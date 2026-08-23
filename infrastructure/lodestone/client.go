// Package lodestone provides a custom HTTP client for scraping The Lodestone.
// This replaces the godestone-based client with direct HTML scraping using
// the achievement detail endpoint for sequential milestone checking.
package lodestone

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/time/rate"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	userAgent      = "Mozilla/5.0 (compatible; FFXIV-Census-Engine/1.0)"
	requestTimeout = 30 * time.Second
	rateLimitPause = 30 * time.Second
	maxSafeRate    = 1.0
)

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
	factor := 0.9 + 0.2*rand.Float64()
	return time.Duration(float64(d) * factor)
}

// stripTags removes HTML tags, decodes common entities, and collapses whitespace.
var (
	tagRe     = regexp.MustCompile(`<[^>]*>`)
	entityMap = map[string]string{
		"&#39;":  "'",
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": `"`,
		"&#34;":  `"`,
		"&nbsp;": " ",
	}
	multiSpaceRe = regexp.MustCompile(`\s+`)
)

func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	for entity, replacement := range entityMap {
		s = strings.ReplaceAll(s, entity, replacement)
	}
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// CustomClientOption configures the custom Lodestone client.
type CustomClientOption func(*CustomClient)

// WithProxy routes all HTTP requests through the given proxy URL.
// Supports http, https, socks4, and socks5 protocols.
func WithProxy(proxyURL string) CustomClientOption {
	return func(c *CustomClient) {
		c.proxyURL = proxyURL
	}
}

// CustomClient is a contract.LodestoneClient that scrapes The Lodestone
// directly via HTTP, using the achievement detail endpoint for milestone checks.
type CustomClient struct {
	httpClient  *http.Client
	limiter     *rate.Limiter
	maxRetries  int
	backoffBase time.Duration
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
	proxyURL    string
}

// NewCustomClient builds a custom Lodestone HTTP client.
func NewCustomClient(cfg *config.LodestoneConfig, logger contract.Logger, rateLimiter contract.ProviderRateLimiter, opts ...CustomClientOption) (*CustomClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("lodestone config is nil")
	}

	rps := cfg.RateLimit
	if rps <= 0 {
		rps = maxSafeRate
	}
	if rps > maxSafeRate {
		rps = maxSafeRate
	}

	c := &CustomClient{
		httpClient:  &http.Client{Timeout: requestTimeout},
		limiter:     rate.NewLimiter(rate.Limit(rps), 1),
		maxRetries:  cfg.MaxRetries,
		backoffBase: 500 * time.Millisecond,
		logger:      loggerOrDiscard(logger),
		rateLimiter: rateLimiter,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.proxyURL != "" {
		transport, err := newProxyTransport(c.proxyURL)
		if err != nil {
			return nil, fmt.Errorf("lodestone proxy transport: %w", err)
		}
		c.httpClient.Transport = transport
	}

	return c, nil
}

// newProxyTransport creates an HTTP transport that routes through the given proxy.
func newProxyTransport(proxyURL string) (*http.Transport, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http", "https":
		return &http.Transport{Proxy: http.ProxyURL(u)}, nil
	case "socks4", "socks5":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, err
		}
		ctxDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			// go-socks4 dialer doesn't implement ContextDialer; wrap plain Dialer.
			return &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			}, nil
		}
		return &http.Transport{DialContext: ctxDialer.DialContext}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
	}
}

// doRequest executes an HTTP GET with rate limiting, retries, and backoff.
func (c *CustomClient) doRequest(ctx context.Context, url string) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, 0, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			backoff := c.backoffBase * time.Duration(1<<uint(attempt))
			c.logger.WarnContext(ctx, "lodestone.request_retry",
				slog.String("url", url),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", c.maxRetries+1),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()))
			if attempt < c.maxRetries {
				timer := time.NewTimer(jittered(backoff))
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, 0, ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if c.rateLimiter != nil {
				c.rateLimiter.Pause(contract.ProviderLodestone, rateLimitPause, "lodestone 429 rate limited")
			}
			backoff := c.backoffBase * time.Duration(1<<uint(attempt))
			c.logger.WarnContext(ctx, "lodestone.rate_limited",
				slog.String("url", url),
				slog.Int("attempt", attempt+1),
				slog.Duration("backoff", backoff))
			if attempt < c.maxRetries {
				timer := time.NewTimer(jittered(backoff))
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, 0, ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}

		return body, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("request %s: %w", url, lastErr)
}

// FetchCharacter scrapes a character profile from The Lodestone.
func (c *CustomClient) FetchCharacter(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
	start := time.Now()
	charURL := fmt.Sprintf("https://na.finalfantasyxiv.com/lodestone/character/%d/", id)

	c.logger.DebugContext(ctx, "lodestone.fetch_character.attempt",
		slog.Uint64("character_id", uint64(id)),
		slog.String("proxy", c.proxyURL))

	body, statusCode, err := c.doRequest(ctx, charURL)
	if err != nil {
		c.logger.WarnContext(ctx, "lodestone.fetch_character.error",
			slog.Uint64("character_id", uint64(id)),
			slog.Any("error", err))
		return nil, fmt.Errorf("fetch character %d: %w", id, err)
	}

	if statusCode == http.StatusNotFound || statusCode == http.StatusForbidden {
		c.logger.DebugContext(ctx, "lodestone.fetch_character.not_found",
			slog.Uint64("character_id", uint64(id)),
			slog.Int("status", statusCode))
		return nil, contract.ErrCharacterNotFound
	}

	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch character %d: HTTP %d", id, statusCode)
	}

	html := string(body)
	profile, err := parseCharacterProfile(html, id)
	if err != nil {
		return nil, fmt.Errorf("parse character %d: %w", id, err)
	}

	duration := time.Since(start)
	c.logger.DebugContext(ctx, "lodestone.fetch_character.success",
		slog.Uint64("character_id", uint64(id)),
		slog.String("name", profile.Name),
		slog.String("world", profile.World),
		slog.Duration("duration", duration))

	return profile, nil
}

// FetchAchievements checks milestone achievements using the detail endpoint.
// Milestones are checked sequentially; if one is missing, checking stops
// (sequential dependency: later milestones require earlier ones).
func (c *CustomClient) FetchAchievements(ctx context.Context, charID uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
	start := time.Now()

	c.logger.DebugContext(ctx, "lodestone.fetch_achievements.start",
		slog.Uint64("character_id", uint64(charID)),
		slog.Int("milestones_to_check", len(milestoneIDs)))

	// Check privacy first.
	private, err := c.checkPrivacy(ctx, charID)
	if err != nil {
		return nil, fmt.Errorf("check privacy %d: %w", charID, err)
	}
	if private {
		c.logger.DebugContext(ctx, "lodestone.fetch_achievements.private",
			slog.Uint64("character_id", uint64(charID)))
		return &contract.AchievementSummary{Private: true}, nil
	}

	var results []contract.AchievementResult
	requestsMade := 1 // privacy check counts

	for _, id := range milestoneIDs {
		result, err := c.checkSingleAchievement(ctx, charID, id)
		if err != nil {
			return nil, fmt.Errorf("check achievement %d for character %d: %w", id, charID, err)
		}
		requestsMade++

		if result.Earned {
			results = append(results, result)
		} else {
			// Sequential dependency — no point checking further.
			c.logger.DebugContext(ctx, "lodestone.check_achievement.stopping",
				slog.Uint64("character_id", uint64(charID)),
				slog.Uint64("missing_id", uint64(id)),
				slog.String("missing_name", result.Name),
				slog.String("reason", "sequential_dependency"),
				slog.Int("milestones_found", len(results)))
			break
		}
	}

	summary := &contract.AchievementSummary{
		Milestones:        results,
		LatestAchievement: findLatest(results),
	}

	duration := time.Since(start)
	c.logger.DebugContext(ctx, "lodestone.fetch_achievements.complete",
		slog.Uint64("character_id", uint64(charID)),
		slog.Int("milestones_found", len(results)),
		slog.Int("requests_made", requestsMade),
		slog.Duration("total_duration", duration))

	return summary, nil
}

// checkPrivacy verifies whether a character's achievements are public.
func (c *CustomClient) checkPrivacy(ctx context.Context, charID uint32) (bool, error) {
	achURL := fmt.Sprintf("https://na.finalfantasyxiv.com/lodestone/character/%d/achievement/", charID)
	_, statusCode, err := c.doRequest(ctx, achURL)
	if err != nil {
		return false, err
	}
	if statusCode == http.StatusForbidden {
		c.logger.DebugContext(ctx, "lodestone.check_privacy.private",
			slog.Uint64("character_id", uint64(charID)))
		return true, nil
	}
	if statusCode != http.StatusOK {
		return false, fmt.Errorf("achievement page for %d: HTTP %d", charID, statusCode)
	}
	c.logger.DebugContext(ctx, "lodestone.check_privacy.public",
		slog.Uint64("character_id", uint64(charID)))
	return false, nil
}

// checkSingleAchievement checks if a character has earned a specific achievement.
func (c *CustomClient) checkSingleAchievement(ctx context.Context, charID uint32, achievementID uint32) (contract.AchievementResult, error) {
	start := time.Now()
	detailURL := fmt.Sprintf("https://na.finalfantasyxiv.com/lodestone/character/%d/achievement/detail/%d/", charID, achievementID)

	c.logger.DebugContext(ctx, "lodestone.check_achievement.attempt",
		slog.Uint64("character_id", uint64(charID)),
		slog.Uint64("achievement_id", uint64(achievementID)))

	body, statusCode, err := c.doRequest(ctx, detailURL)
	if err != nil {
		return contract.AchievementResult{}, err
	}

	duration := time.Since(start)
	html := string(body)

	result := contract.AchievementResult{
		AchievementID: achievementID,
	}

	// Extract achievement name from HTML.
	if name := extractAchievementName(html); name != "" {
		result.Name = name
	}

	if statusCode == http.StatusForbidden {
		// Private or missing — treat as not earned.
		c.logger.DebugContext(ctx, "lodestone.check_achievement.not_earned",
			slog.Uint64("character_id", uint64(charID)),
			slog.Uint64("achievement_id", uint64(achievementID)),
			slog.String("achievement_name", result.Name),
			slog.Duration("duration", duration))
		return result, nil
	}

	if statusCode != http.StatusOK {
		return contract.AchievementResult{}, fmt.Errorf("achievement detail %d/%d: HTTP %d", charID, achievementID, statusCode)
	}

	// Check for earned indicator.
	if strings.Contains(html, "entry__achievement__view--complete") {
		result.Earned = true
		result.EarnedAt = extractTimestamp(html)
		c.logger.DebugContext(ctx, "lodestone.check_achievement.earned",
			slog.Uint64("character_id", uint64(charID)),
			slog.Uint64("achievement_id", uint64(achievementID)),
			slog.String("achievement_name", result.Name),
			slog.Duration("duration", duration))
	} else {
		c.logger.DebugContext(ctx, "lodestone.check_achievement.not_earned",
			slog.Uint64("character_id", uint64(charID)),
			slog.Uint64("achievement_id", uint64(achievementID)),
			slog.String("achievement_name", result.Name),
			slog.Duration("duration", duration))
	}

	return result, nil
}

// HTML parsing helpers.

var (
	timestampRe = regexp.MustCompile(`ldst_strftime\((\d+),`)
	nameRe      = regexp.MustCompile(`entry__activity__txt">([^<]+)</p>`)
	worldDCRe   = regexp.MustCompile(`^([^\s]+)\s+\[([^\]]+)\]$`)
	fcIDRe      = regexp.MustCompile(`/lodestone/freecompany/(\d+)/`)
)

// extractTimestamp extracts the earned-at timestamp from a Lodestone achievement detail page.
func extractTimestamp(html string) time.Time {
	match := timestampRe.FindStringSubmatch(html)
	if len(match) < 2 {
		return time.Time{}
	}
	epoch, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(epoch, 0)
}

// extractAchievementName extracts the achievement name from the detail page HTML.
func extractAchievementName(html string) string {
	match := nameRe.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

// parseCharacterProfile extracts character data from the Lodestone profile page HTML.
func parseCharacterProfile(html string, id uint32) (*contract.CharacterProfile, error) {
	profile := &contract.CharacterProfile{ID: id}

	// Name: p.frame__chara__name
	profile.Name = extractTextBetween(html, `class="frame__chara__name"`, "</p>")
	profile.Name = strings.TrimSpace(profile.Name)

	if profile.Name == "" {
		return nil, fmt.Errorf("could not parse character name from HTML")
	}

	// World + DC: p.frame__chara__world — format "World [DC]"
	worldText := extractTextBetween(html, `class="frame__chara__world"`, "</p>")
	worldText = strings.TrimSpace(worldText)
	worldText = strings.TrimPrefix(worldText, "(")
	worldText = strings.TrimSuffix(worldText, ")")
	if m := worldDCRe.FindStringSubmatch(worldText); len(m) >= 3 {
		profile.World = m[1]
		profile.Datacenter = m[2]
	} else {
		profile.World = worldText
	}

	// Bio: .character__profile__state
	profile.Bio = extractTextBetween(html, `class="character__profile__state"`, "</p>")
	profile.Bio = strings.TrimSpace(profile.Bio)

	// Race/Tribe/Gender: .character-block__name entries
	// New Lodestone HTML puts race, tribe, and gender in a single element:
	// <p class="character-block__name">Roegadyn<br />Hellsguard / ♂</p>
	// After stripTags: "Roegadyn Hellsguard / ♂"
	raceTribe := extractAllTextBetween(html, `class="character-block__name"`, "</p>")
	if len(raceTribe) >= 1 {
		combined := strings.TrimSpace(raceTribe[0])
		// Extract gender from ♂/♀ symbol
		if strings.Contains(combined, "♂") {
			profile.Gender = 1
			combined = strings.ReplaceAll(combined, "♂", "")
		} else if strings.Contains(combined, "♀") {
			profile.Gender = 2
			combined = strings.ReplaceAll(combined, "♀", "")
		}
		// Split on " / " to separate race/tribe from gender suffix
		if parts := strings.SplitN(combined, " / ", 2); len(parts) == 2 {
			combined = parts[0]
		}
		combined = strings.TrimSpace(combined)
		// Split race from tribe. "Au Ra" is the only two-word race name.
		if strings.HasPrefix(combined, "Au Ra ") {
			profile.Race = "Au Ra"
			profile.Tribe = strings.TrimSpace(combined[6:])
		} else if idx := strings.Index(combined, " "); idx > 0 {
			profile.Race = combined[:idx]
			profile.Tribe = strings.TrimSpace(combined[idx+1:])
		} else {
			profile.Race = combined
		}
	}
	if len(raceTribe) >= 2 && profile.Tribe == "" {
		profile.Tribe = strings.TrimSpace(raceTribe[1])
	}

	// Grand Company: 4th character-block__name entry (index 3)
	// Format: "Maelstrom / Second Storm Lieutenant" — take only the company name
	if len(raceTribe) >= 4 {
		gcText := strings.TrimSpace(raceTribe[3])
		if parts := strings.SplitN(gcText, " / ", 2); len(parts) >= 1 {
			profile.GrandCompany = strings.TrimSpace(parts[0])
		}
	}

	// FC Name: extract from <a> inside character__freecompany__name
	// HTML: <p>Free Company</p><h4><a href="/lodestone/freecompany/123/">FC Name</a></h4>
	// extractTextBetween with "</a>" returns "Free Company...FC Name" — use extractHref approach instead
	fcNameText := extractTextBetween(html, `class="character__freecompany__name"`, "</a>")
	// Strip the "Free Company" prefix that appears in the <p> tag before the <a>
	fcNameText = strings.TrimPrefix(fcNameText, "Free Company")
	fcNameText = strings.TrimSpace(fcNameText)
	if fcNameText == "" {
		fcNameText = extractTextBetween(html, `class="character__freecompany__name"`, "</h4>")
		fcNameText = strings.TrimPrefix(fcNameText, "Free Company")
		fcNameText = strings.TrimSpace(fcNameText)
	}
	profile.FreeCompanyName = fcNameText

	// FC ID: extract from href inside character__freecompany__name
	fcLink := extractHref(html, `class="character__freecompany__name"`)
	if fcLink == "" {
		fcLink = extractHref(html, `class="character__freecompany__crest"`)
	}
	if m := fcIDRe.FindStringSubmatch(fcLink); len(m) >= 2 {
		profile.FreeCompanyID = m[1]
	}

	// Active Job: .character__class_icon img alt
	profile.ActiveJob = extractAlt(html, `class="character__class_icon"`)

	// ClassJobs: .character__level__list entries
	profile.ClassJobs = parseClassJobs(html, id)

	return profile, nil
}

// extractTextBetween finds text between a marker and an end tag.
func extractTextBetween(html, startMarker, endTag string) string {
	startIdx := strings.Index(html, startMarker)
	if startIdx == -1 {
		return ""
	}
	// Find the closing > of the opening tag.
	tagEnd := strings.Index(html[startIdx:], ">")
	if tagEnd == -1 {
		return ""
	}
	contentStart := startIdx + tagEnd + 1
	endIdx := strings.Index(html[contentStart:], endTag)
	if endIdx == -1 {
		return ""
	}
	return stripTags(html[contentStart : contentStart+endIdx])
}

// extractAllTextBetween finds all occurrences of text between markers.
func extractAllTextBetween(html, startMarker, endTag string) []string {
	var results []string
	searchFrom := 0
	for {
		startIdx := strings.Index(html[searchFrom:], startMarker)
		if startIdx == -1 {
			break
		}
		startIdx += searchFrom
		tagEnd := strings.Index(html[startIdx:], ">")
		if tagEnd == -1 {
			break
		}
		contentStart := startIdx + tagEnd + 1
		endIdx := strings.Index(html[contentStart:], endTag)
		if endIdx == -1 {
			break
		}
		results = append(results, stripTags(html[contentStart:contentStart+endIdx]))
		searchFrom = contentStart + endIdx + len(endTag)
	}
	return results
}

// extractAttr extracts an attribute from an element found by a marker.
func extractAttr(html, containerMarker, tagName, attr string) string {
	startIdx := strings.Index(html, containerMarker)
	if startIdx == -1 {
		return ""
	}
	// Find the tag within the container.
	tagStart := strings.Index(html[startIdx:], "<"+tagName)
	if tagStart == -1 {
		return ""
	}
	tagStart += startIdx
	tagEnd := strings.Index(html[tagStart:], ">")
	if tagEnd == -1 {
		return ""
	}
	tagHTML := html[tagStart : tagStart+tagEnd+1]
	return extractAttribute(tagHTML, attr)
}

// extractAlt extracts the alt attribute from an img within a container.
func extractAlt(html, containerMarker string) string {
	return extractAttr(html, containerMarker, "img", "alt")
}

// extractHref extracts the href attribute from an anchor within a container.
func extractHref(html, containerMarker string) string {
	return extractAttr(html, containerMarker, "a", "href")
}

// extractAttribute extracts a named attribute from an HTML tag string.
func extractAttribute(tagHTML, attr string) string {
	attrPattern := attr + `="`
	idx := strings.Index(tagHTML, attrPattern)
	if idx == -1 {
		return ""
	}
	start := idx + len(attrPattern)
	end := strings.Index(tagHTML[start:], `"`)
	if end == -1 {
		return ""
	}
	return tagHTML[start : start+end]
}

// parseClassJobs extracts class/job entries from the character profile HTML.
func parseClassJobs(html string, charID uint32) []contract.ClassJobRecord {
	var jobs []contract.ClassJobRecord

	// Find the level list section.
	listIdx := strings.Index(html, `class="character__level__list"`)
	if listIdx == -1 {
		return jobs
	}

	// Extract each entry within the list.
	searchFrom := listIdx
	for {
		entryIdx := strings.Index(html[searchFrom:], `class="character__level__list__entry"`)
		if entryIdx == -1 {
			break
		}
		entryIdx += searchFrom
		entryEnd := strings.Index(html[entryIdx:], "</li>")
		if entryEnd == -1 {
			break
		}
		entryHTML := html[entryIdx : entryIdx+entryEnd]

		// Job name from the tooltip or text.
		jobName := extractTextBetween(entryHTML, `class="character__level__list__name"`, "</p>")
		if jobName == "" {
			jobName = extractTextBetween(entryHTML, `class="character__level__list__name"`, "</span>")
		}
		jobName = strings.TrimSpace(jobName)

		// Level from the level display.
		levelStr := extractTextBetween(entryHTML, `class="character__level__list__level"`, "</p>")
		if levelStr == "" {
			levelStr = extractTextBetween(entryHTML, `class="character__level__list__level"`, "</span>")
		}
		levelStr = strings.TrimSpace(levelStr)
		levelStr = strings.TrimPrefix(levelStr, "Lv.")
		levelStr = strings.TrimSpace(levelStr)

		level, _ := strconv.ParseUint(levelStr, 10, 8)

		if jobName != "" {
			jobs = append(jobs, contract.ClassJobRecord{
				CharacterID: charID,
				Name:        jobName,
				Level:       uint8(level),
			})
		}

		searchFrom = entryIdx + entryEnd + 1
	}

	return jobs
}

// findLatest returns the most recently earned achievement in results.
func findLatest(results []contract.AchievementResult) *contract.AchievementResult {
	var latest *contract.AchievementResult
	for i := range results {
		r := &results[i]
		if !r.Earned || r.EarnedAt.IsZero() {
			continue
		}
		if latest == nil || r.EarnedAt.After(latest.EarnedAt) {
			latest = r
		}
	}
	return latest
}
