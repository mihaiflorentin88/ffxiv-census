package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Checker tests whether a proxy can reach The Lodestone by making an HTTP GET
// request through it. It measures round-trip latency in milliseconds.
type Checker struct {
	testURL string
	timeout time.Duration
	logger  contract.Logger
}

// NewChecker creates a proxy checker that tests against the given URL.
func NewChecker(testURL string, timeout time.Duration, logger contract.Logger) *Checker {
	return &Checker{
		testURL: testURL,
		timeout: timeout,
		logger:  logger,
	}
}

// Check tests a proxy by making an HTTP GET through it to the test URL.
// Returns latency in milliseconds or an error if the proxy is unreachable.
func (c *Checker) Check(ctx context.Context, protocol, ip string, port int) (int, error) {
	proxyURL, err := url.Parse(fmt.Sprintf("%s://%s:%d", protocol, ip, port))
	if err != nil {
		return 0, fmt.Errorf("parse proxy url: %w", err)
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   c.timeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.testURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("proxy request failed: %w", err)
	}
	defer resp.Body.Close()
	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return int(latency.Milliseconds()), nil
}
