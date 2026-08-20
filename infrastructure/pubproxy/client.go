package pubproxy

import (
	"context"
	"encoding/json"
	"fmt"
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

func (c *Client) FetchProxies(ctx context.Context) ([]contract.ProxyRecord, error) {
	params := map[string]string{
		"format": "json",
		"limit":  "5",
	}

	resp, err := c.httpClient.Get(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, fmt.Errorf("pubproxy fetch: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("pubproxy: unexpected status %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(resp.Body, &apiResp); err != nil {
		return nil, fmt.Errorf("pubproxy decode: %w", err)
	}

	now := time.Now().UTC()
	var records []contract.ProxyRecord

	for _, p := range apiResp.Data {
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
		records = append(records, rec)
	}

	return records, nil
}
