package geonode

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	providerName = "geonode"
	apiURL       = "https://proxylist.geonode.com/api/proxy-list"
)

// Client implements contract.ProxyProvider for the Geonode API.
type Client struct {
	httpClient contract.HTTPClient
	baseURL    string
}

// New creates a new Geonode provider client.
func New(httpClient contract.HTTPClient, baseURL string) *Client {
	if baseURL == "" {
		baseURL = apiURL
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}
}

func (c *Client) Name() string { return providerName }

// proxyResponse represents a single proxy in the Geonode JSON response.
type proxyResponse struct {
	IP             string   `json:"ip"`
	Port           string   `json:"port"` // string in Geonode API
	Protocols      []string `json:"protocols"`
	AnonymityLevel string   `json:"anonymityLevel"`
	Country        string   `json:"country"`
	UpTime         float64  `json:"upTime"`
}

type apiResponse struct {
	Data  []proxyResponse `json:"data"`
	Total int             `json:"total"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
}

func (c *Client) FetchProxies(ctx context.Context) ([]contract.ProxyRecord, error) {
	var allRecords []contract.ProxyRecord
	page := 1
	limit := 500

	for {
		params := map[string]string{
			"limit":     strconv.Itoa(limit),
			"page":      strconv.Itoa(page),
			"sort_by":   "lastChecked",
			"sort_type": "desc",
		}

		resp, err := c.httpClient.Get(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"})
		if err != nil {
			return nil, fmt.Errorf("geonode fetch page %d: %w", page, err)
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("geonode: unexpected status %d on page %d", resp.StatusCode, page)
		}

		var apiResp apiResponse
		if err := json.Unmarshal(resp.Body, &apiResp); err != nil {
			return nil, fmt.Errorf("geonode decode page %d: %w", page, err)
		}

		now := time.Now().UTC()
		for _, p := range apiResp.Data {
			port, err := strconv.Atoi(p.Port)
			if err != nil {
				continue
			}
			for _, proto := range p.Protocols {
				rec := contract.ProxyRecord{
					Protocol:    proto,
					IP:          p.IP,
					Port:        port,
					FirstSeenAt: now,
					Source:      providerName,
					Status:      contract.ProxyStatusInactive,
				}
				if p.Country != "" {
					rec.Country = &p.Country
				}
				if p.AnonymityLevel != "" {
					rec.Anonymity = &p.AnonymityLevel
				}
				if p.UpTime > 0 {
					rec.UptimePercent = &p.UpTime
				}
				allRecords = append(allRecords, rec)
			}
		}

		if len(apiResp.Data) < limit || page*limit >= apiResp.Total {
			break
		}
		page++
	}

	return allRecords, nil
}
