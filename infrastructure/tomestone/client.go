package tomestone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
	"golang.org/x/time/rate"

	_ "github.com/bdandy/go-socks4" // register socks4 scheme with golang.org/x/net/proxy
	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// maxSafeRate is the rate limit ceiling for tomestone.gg requests (calls/sec).
const maxSafeRate = 20.0

const (
	maxTomestoneResponseBytes = int64(4 << 20)  // 4 MiB
	maxTomestoneErrorBytes    = int64(64 << 10) // 64 KiB
)

// decodeTomestoneResponse performs bounded streaming JSON decode into dst,
// rejecting responses that exceed maxTomestoneResponseBytes or contain
// multiple JSON values.
func decodeTomestoneResponse(body io.Reader, dst *jsonResponse) error {
	limited := &io.LimitedReader{R: body, N: maxTomestoneResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(dst); err != nil {
		if limited.N == 0 {
			return errors.New("tomestone response exceeds 4194304 bytes")
		}
		return err
	}
	// Ensure no trailing JSON values.
	err := decoder.Decode(&struct{}{})
	if limited.N == 0 {
		return errors.New("tomestone response exceeds 4194304 bytes")
	}
	if err == nil {
		return errors.New("tomestone response contains multiple JSON values")
	}
	if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// ClientOption is a typed functional option for NewClient / NewClientWithProxy.
type ClientOption func(*clientOptions)

type clientOptions struct {
	providerLimiter   contract.ProviderRateLimiter
	requestRate       *RequestRateController
	sharedRateLimiter *rate.Limiter
}

// WithProviderRateLimiter sets the per-worker provider cooldown limiter.
func WithProviderRateLimiter(l contract.ProviderRateLimiter) ClientOption {
	return func(o *clientOptions) { o.providerLimiter = l }
}

// WithRequestRateController sets a shared request-rate controller for adaptive
// backoff across multiple proxy clients.
func WithRequestRateController(c *RequestRateController) ClientOption {
	return func(o *clientOptions) { o.requestRate = c }
}

// WithSharedRateLimiter sets a shared *rate.Limiter (legacy, prefer
// WithRequestRateController for new code).
func WithSharedRateLimiter(l *rate.Limiter) ClientOption {
	return func(o *clientOptions) { o.sharedRateLimiter = l }
}

type Client struct {
	baseURL     string
	apiToken    string
	httpClient  *http.Client
	requestRate *RequestRateController
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

func applyClientOptions(opts []ClientOption) clientOptions {
	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// NewClient constructs a new TomestoneClient adapter.
// Typed options: WithProviderRateLimiter, WithRequestRateController, WithSharedRateLimiter.
func NewClient(cfg *config.TomestoneConfig, logger contract.Logger, opts ...ClientOption) (contract.TomestoneClient, error) {
	if cfg == nil {
		return nil, errors.New("tomestone config is nil")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	o := applyClientOptions(opts)

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://tomestone.gg"
	}

	timeout := 10 * time.Second
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	requestRate := o.requestRate
	if requestRate == nil {
		// Build a per-client controller from config, applying the clamp.
		requestRate = NewRequestRateController(cfg.RateLimit)
	}
	// Legacy path: if a shared *rate.Limiter was passed, wrap it.
	if o.sharedRateLimiter != nil {
		requestRate = &RequestRateController{
			limiter:        o.sharedRateLimiter,
			configuredRate: requestRate.configuredRate,
		}
	}

	return &Client{
		baseURL:     baseURL,
		apiToken:    strings.TrimSpace(cfg.APIToken),
		httpClient:  &http.Client{Timeout: timeout},
		requestRate: requestRate,
		logger:      logger,
		rateLimiter: o.providerLimiter,
	}, nil
}

// NewClientWithProxy creates a TomestoneClient that routes all requests
// through the given proxy URL. The proxyURL must include the protocol
// (http://, socks4://, socks5://). An optional shared *rate.Limiter may be
// passed to share the token bucket across multiple proxy clients.
func NewClientWithProxy(cfg *config.TomestoneConfig, proxyURL string, logger contract.Logger, opts ...ClientOption) (contract.TomestoneClient, error) {
	if cfg == nil {
		return nil, errors.New("tomestone config is nil")
	}
	if proxyURL == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	o := applyClientOptions(opts)

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://tomestone.gg"
	}

	timeout := 10 * time.Second
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	// Build proxy-aware HTTP transport by cloning DefaultTransport to inherit
	// bounded standard idle-connection/TLS timeouts.
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}

	cloneTransport := func() *http.Transport {
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			return dt.Clone()
		}
		return &http.Transport{}
	}

	var transport *http.Transport
	switch u.Scheme {
	case "http", "https":
		transport = cloneTransport()
		transport.Proxy = http.ProxyURL(u)
	case "socks5":
		dialer, derr := xproxy.FromURL(u, xproxy.Direct)
		if derr != nil {
			return nil, fmt.Errorf("create socks dialer: %w", derr)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks dialer does not support context")
		}
		transport = cloneTransport()
		transport.DialContext = ctxDialer.DialContext
	case "socks4":
		dialer, derr := xproxy.FromURL(u, xproxy.Direct)
		if derr != nil {
			return nil, fmt.Errorf("create socks4 dialer: %w", derr)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			// go-socks4 dialer doesn't implement ContextDialer; wrap plain Dialer.
			transport = cloneTransport()
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		} else {
			transport = cloneTransport()
			transport.DialContext = ctxDialer.DialContext
		}
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
	}

	requestRate := o.requestRate
	if requestRate == nil {
		requestRate = NewRequestRateController(cfg.RateLimit)
	}
	if o.sharedRateLimiter != nil {
		requestRate = &RequestRateController{
			limiter:        o.sharedRateLimiter,
			configuredRate: requestRate.configuredRate,
		}
	}

	return &Client{
		baseURL:     baseURL,
		apiToken:    strings.TrimSpace(cfg.APIToken),
		httpClient:  &http.Client{Transport: transport, Timeout: timeout},
		requestRate: requestRate,
		logger:      logger,
		rateLimiter: o.providerLimiter,
	}, nil
}

// IsConfigured returns true if the client has a non-empty API token.
func (c *Client) IsConfigured() bool {
	return c.apiToken != ""
}

// FetchCharacterProfile fetches a character's profile by their Lodestone ID.

// FetchCharacterProfile fetches a character's profile by their Lodestone ID.
func (c *Client) FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
	endpoint := fmt.Sprintf("%s/api/character/profile/%d", c.baseURL, id)
	if update {
		endpoint += "?update=true"
	}
	return c.fetchProfile(ctx, endpoint)
}

// FetchCharacterProfileByName fetches a character's profile by server and character name.
func (c *Client) FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*contract.TomestoneCharacter, error) {
	endpoint := fmt.Sprintf(
		"%s/api/character/profile/%s/%s",
		c.baseURL,
		url.PathEscape(server),
		url.PathEscape(name),
	)
	if update {
		endpoint += "?update=true"
	}
	return c.fetchProfile(ctx, endpoint)
}

// jsonResponse unmarshals either direct character fields or a nested data envelope.
type jsonResponse struct {
	Data      *jsonCharacter `json:"data"`
	Character *jsonCharacter `json:"character"`
	jsonCharacter
}

type jsonCharacter struct {
	ID              uint32         `json:"id"`
	Name            string         `json:"name"`
	Server          string         `json:"server"`
	World           string         `json:"world"`
	Datacenter      string         `json:"datacenter"`
	DC              string         `json:"dc"`
	Gender          any            `json:"gender"`
	Race            any            `json:"race"`
	Tribe           any            `json:"tribe"`
	Clan            any            `json:"clan"`
	Title           any            `json:"title"`
	GrandCompany    any            `json:"grand_company"`
	FreeCompanyID   *string        `json:"free_company_id"`
	FreeCompanyName *string        `json:"free_company_name"`
	Bio             string         `json:"bio"`
	Introduction    string         `json:"introduction"`
	Avatar          string         `json:"avatar"`
	AvatarURL       string         `json:"avatar_url"`
	Portrait        string         `json:"portrait"`
	PortraitURL     string         `json:"portrait_url"`
	ActiveJob       string         `json:"active_job"`
	Jobs            []jsonClassJob `json:"jobs"`
	ClassJobs       []jsonClassJob `json:"class_jobs"`
	Gear            []jsonGear     `json:"gear"`
	Equipment       []jsonGear     `json:"equipment"`
	UpdatedAt       any            `json:"updated_at"`
	LastUpdated     any            `json:"last_updated"`
}

type jsonClassJob struct {
	ID     uint8  `json:"id"`
	Name   string `json:"name"`
	Abbr   string `json:"abbr"`
	Role   string `json:"role"`
	Level  uint8  `json:"level"`
	Exp    uint32 `json:"exp"`
	ExpMax uint32 `json:"exp_max"`
}

type jsonGear struct {
	Slot      string   `json:"slot"`
	ID        uint32   `json:"id"`
	Name      string   `json:"name"`
	ItemLevel int      `json:"item_level"`
	Dye       *string  `json:"dye"`
	Materia   []string `json:"materia"`
}

func (c *Client) fetchProfile(ctx context.Context, rawURL string) (*contract.TomestoneCharacter, error) {
	if err := c.requestRate.Wait(ctx); err != nil {
		return nil, fmt.Errorf("tomestone rate limiter wait: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create tomestone request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ffxiv-census")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}

	c.logger.DebugContext(ctx, "tomestone.request", slog.String("url", rawURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tomestone http get: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		c.requestRate.RecordSuccess(c.logger, ctx)
	case http.StatusUnauthorized, http.StatusForbidden:
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxTomestoneErrorBytes))
		c.logger.ErrorContext(ctx, "tomestone.unauthenticated", slog.Int("status", resp.StatusCode), slog.String("body", string(errBody)))
		return nil, contract.ErrTomestoneUnauthenticated
	case http.StatusNotFound:
		c.logger.DebugContext(ctx, "tomestone.not_found", slog.String("url", rawURL))
		return nil, contract.ErrCharacterNotFound
	case http.StatusTooManyRequests:
		consecutive := c.requestRate.RecordRateLimit(c.logger, ctx)

		retryAfterHeader := resp.Header.Get("Retry-After")
		var retryAfterDuration time.Duration
		if retryAfterHeader != "" {
			if seconds, err := strconv.Atoi(retryAfterHeader); err == nil && seconds > 0 {
				retryAfterDuration = time.Duration(seconds) * time.Second
			} else if t, err := http.ParseTime(retryAfterHeader); err == nil {
				retryAfterDuration = time.Until(t)
			}
		}

		pauseDuration := 30 * time.Second
		if retryAfterDuration > 0 {
			pauseDuration = retryAfterDuration
		}
		if c.rateLimiter != nil {
			c.rateLimiter.Pause(contract.ProviderTomestone, pauseDuration, "tomestone 429")
		}

		c.logger.WarnContext(
			ctx, "tomestone.rate_limited",
			slog.Int("status", resp.StatusCode),
			slog.Int("consecutive_429s", consecutive),
			slog.Duration("retry_after", retryAfterDuration),
		)

		if retryAfterDuration > 0 {
			timer := time.NewTimer(retryAfterDuration)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		return nil, errors.New("tomestone api rate limit exceeded (HTTP 429)")
	default:
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxTomestoneErrorBytes))
		c.logger.ErrorContext(ctx, "tomestone.error", slog.Int("status", resp.StatusCode), slog.String("body", string(errBody)))
		return nil, fmt.Errorf("tomestone api error: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	var res jsonResponse
	if err := decodeTomestoneResponse(resp.Body, &res); err != nil {
		return nil, fmt.Errorf("decode tomestone response: %w", err)
	}

	charData := &res.jsonCharacter
	if res.Data != nil && res.Data.ID != 0 {
		charData = res.Data
	} else if res.Character != nil && res.Character.ID != 0 {
		charData = res.Character
	}

	// Validate that the response contains meaningful character data.
	// Tomestone may return 200 OK with an ID but no name/server for
	// characters that don't exist or are hidden — treat those as not found.
	server := charData.Server
	if server == "" {
		server = charData.World
	}
	if charData.Name == "" && server == "" {
		c.logger.DebugContext(ctx, "tomestone.empty_character", slog.Uint64("character_id", uint64(charData.ID)))
		return nil, contract.ErrCharacterNotFound
	}

	return toContractCharacter(charData), nil
}

func toContractCharacter(j *jsonCharacter) *contract.TomestoneCharacter {
	server := j.Server
	if server == "" {
		server = j.World
	}
	dc := j.Datacenter
	if dc == "" {
		dc = j.DC
	}
	tribe := parseStringOrObject(j.Tribe)
	if tribe == "" {
		tribe = parseStringOrObject(j.Clan)
	}
	bio := j.Bio
	if bio == "" {
		bio = j.Introduction
	}
	avatar := j.Avatar
	if avatar == "" {
		avatar = j.AvatarURL
	}
	portrait := j.Portrait
	if portrait == "" {
		portrait = j.PortraitURL
	}

	var genderStr string
	switch g := j.Gender.(type) {
	case string:
		genderStr = g
	case float64:
		if g == 1 {
			genderStr = "male"
		} else if g == 2 {
			genderStr = "female"
		}
	}

	jobs := j.Jobs
	if len(jobs) == 0 {
		jobs = j.ClassJobs
	}
	contractJobs := make([]contract.TomestoneClassJob, 0, len(jobs))
	for _, job := range jobs {
		contractJobs = append(contractJobs, contract.TomestoneClassJob{
			ID:     job.ID,
			Name:   job.Name,
			Abbr:   job.Abbr,
			Role:   job.Role,
			Level:  job.Level,
			Exp:    job.Exp,
			ExpMax: job.ExpMax,
		})
	}

	gear := j.Gear
	if len(gear) == 0 {
		gear = j.Equipment
	}
	contractGear := make([]contract.TomestoneGear, 0, len(gear))
	for _, g := range gear {
		contractGear = append(contractGear, contract.TomestoneGear{
			Slot:      g.Slot,
			ID:        g.ID,
			Name:      g.Name,
			ItemLevel: g.ItemLevel,
			Dye:       g.Dye,
			Materia:   g.Materia,
		})
	}

	updatedAt := parseTime(j.UpdatedAt)
	if updatedAt.IsZero() {
		updatedAt = parseTime(j.LastUpdated)
	}

	return &contract.TomestoneCharacter{
		ID:              j.ID,
		Name:            j.Name,
		Server:          server,
		Datacenter:      dc,
		Gender:          genderStr,
		Race:            parseStringOrObject(j.Race),
		Tribe:           tribe,
		Title:           parseStringOrObject(j.Title),
		GrandCompany:    parseStringOrObject(j.GrandCompany),
		FreeCompanyID:   j.FreeCompanyID,
		FreeCompanyName: j.FreeCompanyName,
		Bio:             bio,
		AvatarURL:       avatar,
		PortraitURL:     portrait,
		ActiveJob:       j.ActiveJob,
		Jobs:            contractJobs,
		Gear:            contractGear,
		UpdatedAt:       updatedAt,
	}
}

func parseTime(val any) time.Time {
	if val == nil {
		return time.Time{}
	}
	switch v := val.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			return t
		}
	case float64:
		return time.Unix(int64(v), 0).UTC()
	case int64:
		return time.Unix(v, 0).UTC()
	case int:
		return time.Unix(int64(v), 0).UTC()
	}
	return time.Time{}
}

func parseStringOrObject(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case map[string]any:
		if name, ok := v["name"].(string); ok {
			return name
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
		if title, ok := v["title"].(string); ok {
			return title
		}
	}
	return ""
}

// ConvertToCharacterRecord converts a TomestoneCharacter into domain CharacterRecord.
func ConvertToCharacterRecord(tc *contract.TomestoneCharacter) contract.CharacterRecord {
	var gender uint8
	if strings.EqualFold(tc.Gender, "female") {
		gender = 2
	} else if strings.EqualFold(tc.Gender, "male") {
		gender = 1
	}

	now := time.Now().UTC()
	return contract.CharacterRecord{
		ID:              tc.ID,
		Name:            tc.Name,
		World:           tc.Server,
		Datacenter:      tc.Datacenter,
		Race:            tc.Race,
		Tribe:           tc.Tribe,
		Gender:          gender,
		GrandCompany:    tc.GrandCompany,
		FreeCompanyID:   tc.FreeCompanyID,
		FreeCompanyName: tc.FreeCompanyName,
		FirstSeenAt:     now,
		LastCensusAt:    &now,
	}
}
