package pubproxy

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
	providerName = "pubproxy"
	defaultURL   = "http://pubproxy.com/api/proxy"
)

// Client implements contract.ProxyProvider for the PubProxy JSON API.
type Client struct {
	httpClient contract.HTTPClient
	baseURL    string
}

// New creates a new PubProxy provider client.
func New(httpClient contract.HTTPClient, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultURL
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}
}

func (c *Client) Name() string { return providerName }

// proxyResponse represents a single proxy in the PubProxy JSON response.
type proxyResponse struct {
	IP         string `json:"ip"`
	Port       string `json:"port"` // string in PubProxy API
	Type       string `json:"type"`
	Country    string `json:"country"`
	ProxyLevel string `json:"proxy_level"`
	Speed      string `json:"speed"`
}

type apiResponse struct {
	Data  []proxyResponse `json:"data"`
	Count int             `json:"count"`
}

func (c *Client) FetchProxies(ctx context.Context, emit func(contract.ProxyRecord) error) error {
	params := map[string]string{
		"format": "json",
		"limit":  "5",
	}

	return c.httpClient.GetStream(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"}, func(statusCode int, body io.Reader) error {
		if statusCode != 200 {
			return fmt.Errorf("pubproxy: unexpected status %d", statusCode)
		}

		now := time.Now().UTC()
		decoder := json.NewDecoder(body)

		// Read opening token
		t, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("pubproxy decode: %w", err)
		}
		if delim, ok := t.(json.Delim); !ok || delim != '{' {
			return fmt.Errorf("pubproxy decode: expected object, got %v", t)
		}

		// Find the "data" array
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("pubproxy decode key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				continue
			}
			if key != "data" {
				// Skip unknown fields
				var skip json.RawMessage
				if err := decoder.Decode(&skip); err != nil {
					return fmt.Errorf("pubproxy skip field: %w", err)
				}
				continue
			}

			// Read opening bracket of data array
			delim, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("pubproxy data array: %w", err)
			}
			if d, ok := delim.(json.Delim); !ok || d != '[' {
				return fmt.Errorf("pubproxy data: expected array, got %v", delim)
			}

			// Decode each proxy one at a time
			for decoder.More() {
				var p proxyResponse
				if err := decoder.Decode(&p); err != nil {
					return fmt.Errorf("pubproxy decode proxy: %w", err)
				}

				var port int
				if _, err := fmt.Sscanf(p.Port, "%d", &port); err != nil {
					continue
				}

				protocol := strings.ToLower(p.Type)
				if protocol == "" {
					protocol = "http"
				}

				rec := contract.ProxyRecord{
					Protocol:    protocol,
					IP:          p.IP,
					Port:        port,
					FirstSeenAt: now,
					Source:      providerName,
					Status:      contract.ProxyStatusInactive,
				}
				if p.Country != "" {
					rec.Country = &p.Country
				}
				if p.ProxyLevel != "" {
					rec.Anonymity = &p.ProxyLevel
				}
				if err := emit(rec); err != nil {
					return err
				}
			}
			// Consume closing bracket of the data array.
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("pubproxy close array: %w", err)
			}
			// Skip remaining top-level fields until closing brace.
			for decoder.More() {
				if _, err := decoder.Token(); err != nil {
					return fmt.Errorf("pubproxy skip remaining key: %w", err)
				}
				var skip json.RawMessage
				if err := decoder.Decode(&skip); err != nil {
					return fmt.Errorf("pubproxy skip remaining value: %w", err)
				}
			}
			// Consume closing brace of the top-level object.
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("pubproxy close object: %w", err)
			}
			return nil
		}
		return nil
	})
}
