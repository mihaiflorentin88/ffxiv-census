package tomestone

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestNewClient_DefaultsAndValidation(t *testing.T) {
	_, err := NewClient(nil, nil)
	if err == nil {
		t.Fatal("expected error when config is nil")
	}

	cfg := &config.TomestoneConfig{
		BaseURL:   "https://tomestone.gg",
		RateLimit: 10.0,
		Timeout:   "5s",
		APIToken:  "my-secret-token",
	}
	c, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if !c.IsConfigured() {
		t.Error("expected client to be configured")
	}

	unconfiguredCfg := &config.TomestoneConfig{
		BaseURL: "https://tomestone.gg",
	}
	unconfiguredClient, err := NewClient(unconfiguredCfg, nil)
	if err != nil {
		t.Fatalf("NewClient unconfigured: %v", err)
	}
	if unconfiguredClient.IsConfigured() {
		t.Error("expected client to not be configured")
	}
}

func TestFetchCharacterProfile_Success(t *testing.T) {
	responseJSON := `{
		"id": 36795950,
		"name": "Tataru Taru",
		"server": "Balmung",
		"datacenter": "Crystal",
		"gender": "female",
		"race": "Lalafell",
		"tribe": "Plainsfolk",
		"title": "The Scion",
		"grand_company": "Maelstrom",
		"free_company_id": "1234567890123456789",
		"free_company_name": "Scions of the Seventh Dawn",
		"bio": "Pray return to the Waking Sands.",
		"avatar": "https://tomestone.gg/avatars/36795950.jpg",
		"portrait": "https://tomestone.gg/portraits/36795950.jpg",
		"active_job": "Arcanist",
		"jobs": [
			{
				"id": 26,
				"name": "Arcanist",
				"abbr": "ACN",
				"role": "DPS",
				"level": 90,
				"exp": 1000,
				"exp_max": 2000
			}
		],
		"gear": [
			{
				"slot": "MainHand",
				"id": 35000,
				"name": "Book of Spells",
				"item_level": 660,
				"dye": "Dalamud Red",
				"materia": ["Savage Aim IX"]
			}
		],
		"updated_at": "2026-08-16T12:00:00Z"
	}`

	var receivedAuthHeader string
	var receivedPath string
	var receivedQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		receivedPath = r.URL.Path
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "test-bearer-token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	char, err := client.FetchCharacterProfile(context.Background(), 36795950, true)
	if err != nil {
		t.Fatalf("FetchCharacterProfile: %v", err)
	}

	if receivedAuthHeader != "Bearer test-bearer-token" {
		t.Errorf("auth header = %q, want 'Bearer test-bearer-token'", receivedAuthHeader)
	}
	if receivedPath != "/api/character/profile/36795950" {
		t.Errorf("path = %q, want '/api/character/profile/36795950'", receivedPath)
	}
	if receivedQuery != "update=true" {
		t.Errorf("query = %q, want 'update=true'", receivedQuery)
	}

	if char.ID != 36795950 {
		t.Errorf("char.ID = %d, want 36795950", char.ID)
	}
	if char.Name != "Tataru Taru" {
		t.Errorf("char.Name = %q, want 'Tataru Taru'", char.Name)
	}
	if char.Server != "Balmung" {
		t.Errorf("char.Server = %q, want 'Balmung'", char.Server)
	}
	if char.Datacenter != "Crystal" {
		t.Errorf("char.Datacenter = %q, want 'Crystal'", char.Datacenter)
	}
	if char.Gender != "female" {
		t.Errorf("char.Gender = %q, want 'female'", char.Gender)
	}
	if char.Race != "Lalafell" {
		t.Errorf("char.Race = %q, want 'Lalafell'", char.Race)
	}
	if char.Tribe != "Plainsfolk" {
		t.Errorf("char.Tribe = %q, want 'Plainsfolk'", char.Tribe)
	}
	if char.FreeCompanyID == nil || *char.FreeCompanyID != "1234567890123456789" {
		t.Errorf("char.FreeCompanyID = %v, want 1234567890123456789", char.FreeCompanyID)
	}
	if len(char.Jobs) != 1 || char.Jobs[0].Name != "Arcanist" || char.Jobs[0].Level != 90 {
		t.Errorf("char.Jobs = %+v, want 1 job (Arcanist 90)", char.Jobs)
	}
	if len(char.Gear) != 1 || char.Gear[0].Name != "Book of Spells" || char.Gear[0].ItemLevel != 660 {
		t.Errorf("char.Gear = %+v, want 1 gear item", char.Gear)
	}
}

func TestFetchCharacterProfileByName_Success(t *testing.T) {
	responseJSON := `{
		"id": 12345,
		"name": "Urianger Augurelt",
		"server": "Ragnarok",
		"datacenter": "Chaos",
		"gender": "male",
		"race": "Elezen",
		"tribe": "Wildwood",
		"updated_at": "2026-08-16T12:00:00Z"
	}`

	var receivedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "test-bearer-token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	char, err := client.FetchCharacterProfileByName(context.Background(), "Ragnarok", "Urianger Augurelt", false)
	if err != nil {
		t.Fatalf("FetchCharacterProfileByName: %v", err)
	}

	if receivedURI != "/api/character/profile/Ragnarok/Urianger%20Augurelt" && receivedURI != "/api/character/profile/Ragnarok/Urianger+Augurelt" {
		t.Errorf("uri = %q, want encoded uri", receivedURI)
	}
	if char.ID != 12345 || char.Name != "Urianger Augurelt" {
		t.Errorf("unexpected char: %+v", char)
	}
}

func TestFetchCharacterProfile_401Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message": "Unauthenticated."}`))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "bad-token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.FetchCharacterProfile(context.Background(), 123, false)
	if !errors.Is(err, contract.ErrTomestoneUnauthenticated) {
		t.Fatalf("err = %v, want ErrTomestoneUnauthenticated", err)
	}
}

func TestFetchCharacterProfile_404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "Character not found."}`))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.FetchCharacterProfile(context.Background(), 99999999, false)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("err = %v, want ErrCharacterNotFound", err)
	}
}

func TestFetchCharacterProfile_429RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message": "Too Many Requests."}`))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.FetchCharacterProfile(context.Background(), 123, false)
	if err == nil {
		t.Fatal("expected error on 429 response")
	}
}

func TestFetchCharacterProfile_AdaptiveRateAndRetryAfter(t *testing.T) {
	var status int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "Too Many Requests."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 123, "name": "Test Char", "server": "Cerberus", "datacenter": "Chaos"}`))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "token",
		RateLimit: 10.0,
		Timeout:   "5s",
	}
	rawClient, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := rawClient.(*Client)

	// Initial rate limit
	if c.requestRate.Rate() != 10.0 {
		t.Errorf("initial limit = %v, want 10.0", c.requestRate.Rate())
	}

	// 1st 429: rate halves to 5.0
	status = http.StatusTooManyRequests
	_, err = c.FetchCharacterProfile(context.Background(), 123, false)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if c.requestRate.Rate() != 5.0 {
		t.Errorf("after 1st 429, limit = %v, want 5.0", c.requestRate.Rate())
	}

	// 2nd 429: rate halves to 2.5
	_, err = c.FetchCharacterProfile(context.Background(), 123, false)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if c.requestRate.Rate() != 2.5 {
		t.Errorf("after 2nd 429, limit = %v, want 2.5", c.requestRate.Rate())
	}

	// Success on 200: rate recovers towards 5.0 then 10.0
	status = http.StatusOK
	_, err = c.FetchCharacterProfile(context.Background(), 123, false)
	if err != nil {
		t.Fatalf("unexpected error on 200: %v", err)
	}
	if c.requestRate.Rate() != 5.0 {
		t.Errorf("after 1st recovery, limit = %v, want 5.0", c.requestRate.Rate())
	}

	_, err = c.FetchCharacterProfile(context.Background(), 123, false)
	if err != nil {
		t.Fatalf("unexpected error on 200: %v", err)
	}
	if c.requestRate.Rate() != 10.0 {
		t.Errorf("after 2nd recovery, limit = %v, want 10.0", c.requestRate.Rate())
	}
}

func TestFetchCharacterProfile_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.FetchCharacterProfile(ctx, 123, false)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestFetchCharacterProfile_ObjectFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 12345,
			"name": "Object Test",
			"server": "Balmung",
			"datacenter": "Crystal",
			"title": {"id": 1, "name": "Seeker of Truth"},
			"race": {"id": 2, "name": "Hyur"},
			"tribe": {"id": 3, "name": "Midlander"},
			"grand_company": {"id": 4, "name": "Maelstrom"}
		}`))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:  server.URL,
		APIToken: "token",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	char, err := client.FetchCharacterProfile(context.Background(), 12345, false)
	if err != nil {
		t.Fatalf("FetchCharacterProfile: %v", err)
	}
	if char.Title != "Seeker of Truth" {
		t.Errorf("char.Title = %q, want 'Seeker of Truth'", char.Title)
	}
	if char.Race != "Hyur" {
		t.Errorf("char.Race = %q, want 'Hyur'", char.Race)
	}
	if char.Tribe != "Midlander" {
		t.Errorf("char.Tribe = %q, want 'Midlander'", char.Tribe)
	}
	if char.GrandCompany != "Maelstrom" {
		t.Errorf("char.GrandCompany = %q, want 'Maelstrom'", char.GrandCompany)
	}
}

func TestConvertToCharacterRecord(t *testing.T) {
	fcID := "123456"
	fcName := "Scions"
	char := &contract.TomestoneCharacter{
		ID:              123,
		Name:            "Alisaie Leveilleur",
		Server:          "Balmung",
		Datacenter:      "Crystal",
		Gender:          "female",
		Race:            "Elezen",
		Tribe:           "Wildwood",
		GrandCompany:    "Immortal Flames",
		FreeCompanyID:   &fcID,
		FreeCompanyName: &fcName,
	}

	rec := ConvertToCharacterRecord(char)
	if rec.ID != 123 {
		t.Errorf("rec.ID = %d, want 123", rec.ID)
	}
	if rec.Name != "Alisaie Leveilleur" {
		t.Errorf("rec.Name = %q, want 'Alisaie Leveilleur'", rec.Name)
	}
	if rec.World != "Balmung" {
		t.Errorf("rec.World = %q, want 'Balmung'", rec.World)
	}
	if rec.Gender != 2 {
		t.Errorf("rec.Gender = %d, want 2 (female)", rec.Gender)
	}
	if rec.FreeCompanyID == nil || *rec.FreeCompanyID != "123456" {
		t.Errorf("rec.FreeCompanyID = %v, want 123456", rec.FreeCompanyID)
	}
}

func TestFetchCharacterProfile_EmptyCharacterReturnsNotFound(t *testing.T) {
	// Tomestone may return 200 OK with an ID but no name/server for
	// characters that don't exist or are hidden. This must be treated
	// as not-found to avoid inserting ghost rows.
	responseJSON := `{"id": 46833}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "test-token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.FetchCharacterProfile(context.Background(), 46833, false)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("expected ErrCharacterNotFound for empty character, got: %v", err)
	}
}

func TestFetchCharacterProfile_EmptyNameOnlyReturnsNotFound(t *testing.T) {
	// Server present but name empty — still invalid.
	responseJSON := `{"id": 99999, "server": "Balmung"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer server.Close()

	cfg := &config.TomestoneConfig{
		BaseURL:   server.URL,
		APIToken:  "test-token",
		RateLimit: 50.0,
		Timeout:   "5s",
	}
	client, err := NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	char, err := client.FetchCharacterProfile(context.Background(), 99999, false)
	if err != nil {
		t.Fatalf("FetchCharacterProfile: %v", err)
	}
	if char.Name != "" {
		t.Errorf("expected empty name, got %q", char.Name)
	}
	if char.Server != "Balmung" {
		t.Errorf("expected server 'Balmung', got %q", char.Server)
	}
}
