package textproxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Client implements contract.ProxyProvider for plain-text ip:port proxy lists.
// Used for GitHub-hosted lists like Proxifly and TheSpeedX.
type Client struct {
	httpClient contract.HTTPClient
	name       string
	urls       map[string]string // protocol -> URL
}

// New creates a new text proxy provider client.
func New(httpClient contract.HTTPClient, name string, urls map[string]string) *Client {
	return &Client{httpClient: httpClient, name: name, urls: urls}
}

func (c *Client) Name() string { return c.name }

func (c *Client) FetchProxies(ctx context.Context) ([]contract.ProxyRecord, error) {
	var allRecords []contract.ProxyRecord

	for protocol, url := range c.urls {
		records, err := c.fetchFromURL(ctx, url, protocol)
		if err != nil {
			logging.Warn(c.name, fmt.Sprintf("failed to fetch %s proxies from %s: %v", protocol, url, err))
			continue
		}
		allRecords = append(allRecords, records...)
	}

	if len(allRecords) == 0 {
		return nil, fmt.Errorf("%s: no proxies fetched from any source", c.name)
	}

	return allRecords, nil
}

func (c *Client) fetchFromURL(ctx context.Context, url, protocol string) ([]contract.ProxyRecord, error) {
	resp, err := c.httpClient.Get(ctx, url, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%s fetch %s: %w", c.name, protocol, err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: unexpected status %d for %s", c.name, resp.StatusCode, protocol)
	}

	now := time.Now().UTC()
	var records []contract.ProxyRecord

	lines := strings.Split(string(resp.Body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Strip protocol prefix if present (e.g. "http://1.2.3.4:8080" -> "1.2.3.4:8080")
		if idx := strings.Index(line, "://"); idx != -1 {
			line = line[idx+3:]
		}

		host, portStr, err := net.SplitHostPort(line)
		if err != nil {
			logging.Warn(c.name, fmt.Sprintf("skipping malformed line %q: %v", line, err))
			continue
		}

		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
			logging.Warn(c.name, fmt.Sprintf("skipping invalid port in %q: %v", line, err))
			continue
		}

		rec := contract.ProxyRecord{
			Protocol:    protocol,
			IP:          host,
			Port:        port,
			FirstSeenAt: now,
			Source:      c.name,
			Status:      contract.ProxyStatusInactive,
		}
		records = append(records, rec)
	}

	return records, nil
}
