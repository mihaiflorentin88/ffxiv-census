package textproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// errEmitFailed wraps an error returned by the emit callback so callers can
// distinguish callback-directed failures from fetch/decode/scan errors.
var errEmitFailed = errors.New("emit failed")

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

func (c *Client) FetchProxies(ctx context.Context, emit func(contract.ProxyRecord) error) error {
	fetched := 0

	for protocol, url := range c.urls {
		err := c.fetchFromURL(ctx, url, protocol, emit, &fetched)
		if err != nil {
			// Emit errors are caller-directed and must propagate immediately;
			// only fetch/decode/scan failures participate in partial-failure policy.
			if errors.Is(err, errEmitFailed) {
				return err
			}
			logging.Warn("Failed to fetch proxies from provider", fmt.Sprintf("provider=%s url=%s error=%v", c.name, url, err))
			continue
		}
	}

	if fetched == 0 {
		return fmt.Errorf("%s: no proxies fetched from any source", c.name)
	}

	return nil
}

func (c *Client) fetchFromURL(ctx context.Context, url, protocol string, emit func(contract.ProxyRecord) error, fetched *int) error {
	return c.httpClient.GetStream(ctx, url, nil, nil, func(statusCode int, body io.Reader) error {
		if statusCode != 200 {
			return fmt.Errorf("%s: unexpected status %d for %s", c.name, statusCode, protocol)
		}

		now := time.Now().UTC()
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// Strip protocol prefix if present (e.g. "http://1.2.3.4:8080" -> "1.2.3.4:8080")
			if idx := strings.Index(line, "://"); idx != -1 {
				line = line[idx+3:]
			}

			host, portStr, err := net.SplitHostPort(line)
			if err != nil {
				logging.Warn("Skipping malformed proxy line", fmt.Sprintf("provider=%s line=%q error=%v", c.name, line, err))
				continue
			}

			var port int
			if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
				logging.Warn("Skipping invalid proxy port", fmt.Sprintf("provider=%s line=%q error=%v", c.name, line, err))
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
			if err := emit(rec); err != nil {
				return fmt.Errorf("%w: %w", errEmitFailed, err)
			}
			*fetched++
		}
		return scanner.Err()
	})
}
