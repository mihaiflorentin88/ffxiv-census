package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	httpclient "github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// maxIPBodyBytes bounds the ipify response body. The expected payload is a
// single tiny JSON object; anything larger is rejected before decoding.
const maxIPBodyBytes = 1024

// validateIPBody reads and validates a complete ipify response: at most 1024
// bytes containing exactly one JSON object with a string "ip" field that
// parses as an IP address. Extra fields are allowed; trailing data is not.
func validateIPBody(r io.Reader) error {
	body, err := io.ReadAll(io.LimitReader(r, maxIPBodyBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxIPBodyBytes {
		return errors.New("ipify response exceeds 1024 bytes")
	}
	var payload struct {
		IP string `json:"ip"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&payload); err != nil {
		return err
	}
	if _, err := netip.ParseAddr(payload.IP); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("ipify response contains trailing data")
	}
	return nil
}

// HealthChecker tests whether a proxy can complete a strict health check
// against a single JSON IP echo target (ipify). Every check is one-shot:
// the proxied GET must return HTTP 200 with a complete, valid body; idle
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
	if err := validateIPBody(resp.Body); err != nil {
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
