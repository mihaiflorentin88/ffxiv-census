package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	httpclient "github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// maxHealthBodyBytes bounds the health-check response body. A Lodestone
// character page is a few hundred KiB of HTML; anything larger is rejected
// before buffering.
const maxHealthBodyBytes = 512 * 1024

// validateBody reads and validates a complete response from the health
// target: bounded size and non-empty content. The general check proves the
// proxy can deliver the target's real content (e.g. a Lodestone HTML page);
// the body's shape is the target's own concern.
func validateBody(r io.Reader) error {
	body, err := io.ReadAll(io.LimitReader(r, maxHealthBodyBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxHealthBodyBytes {
		return errors.New("health response exceeds body limit")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty response body")
	}
	return nil
}

// HealthChecker tests whether a proxy can complete a health check against
// the configured target URL (by default The Lodestone, so a passing proxy is
// proven usable for real census traffic). Every check is one-shot: the
// proxied GET must return HTTP 200 with a complete, non-empty body; idle
// connections are closed afterwards.
//
// Check outcomes are typed: construction/configuration failures are
// CheckLocal, proven proxy dial or negotiation failures are CheckProxy,
// target HTTP/TLS/payload failures are CheckTarget, and exhausted budgets
// or caller cancellation map to CheckDeadline and CheckCancelled.
type HealthChecker struct {
	targetURL string
	timeout   time.Duration
	logger    contract.Logger
}

// Ensure HealthChecker implements contract.ProxyChecker at compile time.
var _ contract.ProxyChecker = (*HealthChecker)(nil)

// NewHealthChecker creates a health checker that validates against the
// given target URL. timeout bounds each check as a whole.
func NewHealthChecker(targetURL string, timeout time.Duration, logger contract.Logger) *HealthChecker {
	if logger == nil {
		logger = discardLogger()
	}
	return &HealthChecker{
		targetURL: targetURL,
		timeout:   timeout,
		logger:    logger,
	}
}

// Check tests a proxy by making a proxied GET to the target URL and
// validating the complete response. There is no direct fallback: if the
// proxy cannot deliver the response, the check fails. Returns the validated
// round-trip latency in milliseconds.
func (h *HealthChecker) Check(ctx context.Context, protocol, ip string, port int) (int, error) {
	transport, err := httpclient.NewProxyTransport(protocol+"://"+net.JoinHostPort(ip, strconv.Itoa(port)), h.timeout)
	if err != nil {
		return 0, checkError(contract.CheckLocal, "build transport", err)
	}
	defer transport.CloseIdleConnections()
	return h.runCheck(ctx, transport, h.targetURL)
}

// CheckDirect runs the same strict check without any proxy, for
// control-plane verification of this replica's own egress. It is an
// explicit operation and never a fallback path of Check.
func (h *HealthChecker) CheckDirect(ctx context.Context) error {
	transport := httpclient.NewDirectTransport()
	defer transport.CloseIdleConnections()
	_, err := h.runCheck(ctx, transport, h.targetURL)
	return err
}

// runCheck performs one GET: no redirects, HTTP 200 required, no-cache
// requested, complete bounded body validated, latency measured after
// validation.
func (h *HealthChecker) runCheck(ctx context.Context, transport *http.Transport, targetURL string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	client := &http.Client{
		Transport:     transport,
		Timeout:       h.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, checkError(contract.CheckLocal, "build request", err)
	}
	req.Header.Set("Cache-Control", "no-cache")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		kind := classifyCheckError(ctx, err)
		h.logFailure(ctx, kind, err)
		return 0, checkError(kind, "request", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("unexpected status %d", resp.StatusCode)
		h.logFailure(ctx, contract.CheckTarget, err)
		return 0, checkError(contract.CheckTarget, "unexpected status", err)
	}
	if err := validateBody(resp.Body); err != nil {
		h.logFailure(ctx, contract.CheckTarget, err)
		return 0, checkError(contract.CheckTarget, "validate body", err)
	}
	return int(time.Since(start).Milliseconds()), nil
}

func (h *HealthChecker) logFailure(ctx context.Context, kind contract.ProxyCheckKind, err error) {
	h.logger.DebugContext(ctx, "proxy health check failed", "kind", kind.String(), "err", err)
}

// classifyCheckError attributes a failed check. Caller cancellation and
// check deadlines take precedence; only proven proxy dial/negotiation
// failures (wrapped at the dial boundary) map to CheckProxy; every other
// transport or payload failure is attributed to the target.
func classifyCheckError(ctx context.Context, err error) contract.ProxyCheckKind {
	switch {
	case errors.Is(err, context.Canceled), ctx.Err() == context.Canceled:
		return contract.CheckCancelled
	case errors.Is(err, context.DeadlineExceeded), ctx.Err() == context.DeadlineExceeded:
		return contract.CheckDeadline
	}
	var dialErr *httpclient.ProxyDialError
	if errors.As(err, &dialErr) {
		return contract.CheckProxy
	}
	return contract.CheckTarget
}

func checkError(kind contract.ProxyCheckKind, reason string, err error) *contract.ProxyCheckError {
	return &contract.ProxyCheckError{Kind: kind, Reason: reason, Err: err}
}

func discardLogger() contract.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
