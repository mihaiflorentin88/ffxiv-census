package proxyscrape

import (
	"context"
	"encoding/json"
	"fmt"
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
	Timeout   int     `json:"timeout"`
}

type apiResponse struct {
	Proxies []proxyResponse `json:"proxies"`
}

func (c *Client) FetchProxies(ctx context.Context) ([]contract.ProxyRecord, error) {
	params := map[string]string{
		"request":      "display_proxies",
		"proxy_format": "protocolipport",
		"format":       "json",
		"timeout":      "10000",
	}

	resp, err := c.httpClient.Get(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, fmt.Errorf("proxyscrape fetch: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("proxyscrape: unexpected status %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(resp.Body, &apiResp); err != nil {
		return nil, fmt.Errorf("proxyscrape decode: %w", err)
	}

	now := time.Now().UTC()
	var records []contract.ProxyRecord
	for _, p := range apiResp.Proxies {
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
		records = append(records, rec)
	}

	return records, nil
}
