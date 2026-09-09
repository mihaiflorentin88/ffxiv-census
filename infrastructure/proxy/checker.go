package proxy

// Checker tests whether a proxy can reach The Lodestone by making a complete
// HTTP GET request through it. It is the destination checker: outcomes are
// typed ProxyCheckErrors, with 403/429/5xx target failures carrying the
// parsed Retry-After hint.
//
// Supports http, https, socks5, and socks4 proxy protocols through the
// shared cancellation-safe transport builder.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	httpclient "github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// maxCheckerResponseBytes bounds the Lodestone response read. A destination
// response must complete within this budget to count as successful.
const maxCheckerResponseBytes = int64(4 << 20)

// Checker tests whether a proxy can reach The Lodestone by making an HTTP
// GET request through it. It measures round-trip latency in milliseconds.
type Checker struct {
	testURL string
	timeout time.Duration
	logger  contract.Logger
}

// NewChecker creates a destination checker that tests against the given URL.
func NewChecker(testURL string, timeout time.Duration, logger contract.Logger) *Checker {
	if logger == nil {
		logger = discardLogger()
	}
	return &Checker{
		testURL: testURL,
		timeout: timeout,
		logger:  logger,
	}
}

// Check tests a proxy by making an HTTP GET through it to the test URL.
// The response must complete (status 200, body read to EOF within the
// response limit) inside the check timeout. Returns latency in milliseconds
// or a *contract.ProxyCheckError attributing the failure.
func (c *Checker) Check(ctx context.Context, protocol, ip string, port int) (int, error) {
	transport, err := httpclient.NewProxyTransport(protocol+"://"+net.JoinHostPort(ip, strconv.Itoa(port)), c.timeout)
	if err != nil {
		return 0, checkError(contract.CheckLocal, "build transport", err)
	}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	client := &http.Client{
		Transport:     transport,
		Timeout:       c.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.testURL, nil)
	if err != nil {
		return 0, checkError(contract.CheckLocal, "build request", err)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		kind := classifyCheckError(ctx, err)
		c.logger.DebugContext(ctx, "destination check failed", "kind", kind.String(), "err", err)
		return 0, checkError(kind, "request", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("unexpected status %d", resp.StatusCode)
		c.logger.DebugContext(ctx, "destination check failed", "kind", contract.CheckTarget.String(), "status", resp.StatusCode)
		return 0, &contract.ProxyCheckError{
			Kind:       contract.CheckTarget,
			Reason:     "unexpected status",
			RetryAfter: retryAfterForStatus(resp.StatusCode, resp.Header.Get("Retry-After"), time.Now()),
			Err:        err,
		}
	}

	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxCheckerResponseBytes+1))
	if err != nil {
		kind := classifyCheckError(ctx, err)
		c.logger.DebugContext(ctx, "destination check failed", "kind", kind.String(), "err", err)
		return 0, checkError(kind, "read body", err)
	}
	if n > maxCheckerResponseBytes {
		err := fmt.Errorf("response exceeds %d bytes", maxCheckerResponseBytes)
		c.logger.DebugContext(ctx, "destination check failed", "kind", contract.CheckTarget.String(), "err", err)
		return 0, checkError(contract.CheckTarget, "read body", err)
	}
	return int(time.Since(start).Milliseconds()), nil
}

// retryAfterForStatus parses the Retry-After hint for rate-limited or
// unavailable destination responses (403/429/5xx). The date form yields the
// duration remaining at receipt, never an absolute timestamp; invalid or
// negative hints return 0 so consumers apply the normal cooldown floor.
func retryAfterForStatus(status int, hint string, receivedAt time.Time) time.Duration {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
	default:
		if status < 500 {
			return 0
		}
	}
	return parseRetryAfter(hint, receivedAt)
}

// parseRetryAfter parses a Retry-After hint as delta-seconds or an HTTP
// date. Invalid or negative hints return 0.
func parseRetryAfter(hint string, receivedAt time.Time) time.Duration {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return 0
	}
	if secs, err := strconv.Atoi(hint); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(hint); err == nil {
		d := t.Sub(receivedAt)
		if d <= 0 {
			return 0
		}
		return d
	}
	return 0
}
