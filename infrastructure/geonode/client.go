package geonode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func (c *Client) FetchProxies(ctx context.Context, emit func(contract.ProxyRecord) error) error {
	page := 1
	limit := 100

	for {
		params := map[string]string{
			"limit":     strconv.Itoa(limit),
			"page":      strconv.Itoa(page),
			"sort_by":   "lastChecked",
			"sort_type": "desc",
		}

		var pageData []proxyResponse
		var pageTotal int

		err := c.httpClient.GetStream(ctx, c.baseURL, params, map[string]string{"Accept": "application/json"}, func(statusCode int, body io.Reader) error {
			if statusCode != 200 {
				return fmt.Errorf("geonode: unexpected status %d on page %d", statusCode, page)
			}

			var apiResp apiResponse
			if err := json.NewDecoder(body).Decode(&apiResp); err != nil {
				return fmt.Errorf("geonode decode page %d: %w", page, err)
			}

			pageData = apiResp.Data
			pageTotal = apiResp.Total
			return nil
		})
		if err != nil {
			return fmt.Errorf("geonode fetch page %d: %w", page, err)
		}

		now := time.Now().UTC()
		for _, p := range pageData {
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
				if err := emit(rec); err != nil {
					return err
				}
			}
		}

		pageCount := len(pageData)
		// Release page data before requesting next page
		pageData = nil

		if pageCount < limit || page*limit >= pageTotal {
			break
		}
		page++
	}

	return nil
}
