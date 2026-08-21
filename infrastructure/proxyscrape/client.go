package proxyscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	providerName = "proxyscrape"
	apiURL       = "https://api.proxyscrape.com/v4/free-proxy-list/get"
)

// Client implements contract.ProxyProvider for the ProxyScrape API v4.
type Client struct {
	httpClient contract.HTTPClient
	baseURL    string
}

// New creates a new ProxyScrape provider client.
func New(httpClient contract.HTTPClient, baseURL string) *Client {
	if baseURL == "" {
		baseURL = apiURL
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}
}

func (c *Client) Name() string { return providerName }

// proxyResponse represents a single proxy in the ProxyScrape JSON response.
type proxyResponse struct {
	Alive     bool    `json:"alive"`
	IP        string  `json:"ip"`
	Port      int     `json:"port"`
	Protocol  string  `json:"protocol"`
	Country   string  `json:"country_code"`
	Anonymity string  `json:"anonymity"`
	Uptime    float64 `json:"uptime"`
	Timeout   float64 `json:"timeout"`
}

type apiResponse struct {
	Proxies []proxyResponse `json:"proxies"`
}

func (c *Client) FetchProxies(ctx context.Context, emit func(contract.ProxyRecord) error) error {
	params := map[string]string{
		"request":      "display_proxies",
		"proxy_format": "protocolipport",
		"format":       "json",
		"timeout":      "10000",
	}

	return c.httpClient.GetStream(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"}, func(statusCode int, body io.Reader) error {
		if statusCode != 200 {
			return fmt.Errorf("proxyscrape: unexpected status %d", statusCode)
		}

		now := time.Now().UTC()
		decoder := json.NewDecoder(body)

		// Read opening token
		t, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("proxyscrape decode: %w", err)
		}
		if delim, ok := t.(json.Delim); !ok || delim != '{' {
			return fmt.Errorf("proxyscrape decode: expected object, got %v", t)
		}

		// Find the "proxies" array
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("proxyscrape decode key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				continue
			}
			if key != "proxies" {
				// Skip unknown fields
				var skip json.RawMessage
				if err := decoder.Decode(&skip); err != nil {
					return fmt.Errorf("proxyscrape skip field: %w", err)
				}
				continue
			}

			// Read opening bracket of proxies array
			delim, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("proxyscrape proxies array: %w", err)
			}
			if d, ok := delim.(json.Delim); !ok || d != '[' {
				return fmt.Errorf("proxyscrape proxies: expected array, got %v", delim)
			}

			// Decode each proxy one at a time
			for decoder.More() {
				var p proxyResponse
				if err := decoder.Decode(&p); err != nil {
					return fmt.Errorf("proxyscrape decode proxy: %w", err)
				}

				if !p.Alive {
					continue
				}
				protocol := strings.ToLower(p.Protocol)
				if protocol == "" {
					protocol = "http"
				}

				rec := contract.ProxyRecord{
					Protocol:    protocol,
					IP:          p.IP,
					Port:        p.Port,
					FirstSeenAt: now,
					Source:      providerName,
					Status:      contract.ProxyStatusInactive,
				}
				if p.Country != "" {
					rec.Country = &p.Country
				}
				if p.Anonymity != "" {
					rec.Anonymity = &p.Anonymity
				}
				if p.Uptime > 0 {
					rec.UptimePercent = &p.Uptime
				}
				if err := emit(rec); err != nil {
					return err
				}
			}
			// Consume closing bracket of the proxies array.
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("proxyscrape close array: %w", err)
			}
			// Skip remaining top-level fields until closing brace.
			for decoder.More() {
				if _, err := decoder.Token(); err != nil {
					return fmt.Errorf("proxyscrape skip remaining key: %w", err)
				}
				var skip json.RawMessage
				if err := decoder.Decode(&skip); err != nil {
					return fmt.Errorf("proxyscrape skip remaining value: %w", err)
				}
			}
			// Consume closing brace of the top-level object.
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("proxyscrape close object: %w", err)
			}
			return nil
		}
		return nil
	})
}
