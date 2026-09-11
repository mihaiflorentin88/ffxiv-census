package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient/proxytest"
)

const fixtureTimeout = 5 * time.Second

// proxiedClient builds a client whose transport routes through proxyURL and
// trusts only the fixture target's certificate.
func proxiedClient(t *testing.T, proxyURL string, target *httptest.Server, timeout time.Duration) *http.Client {
	t.Helper()
	transport, err := NewProxyTransport(proxyURL, timeout)
	if err != nil {
		t.Fatalf("NewProxyTransport(%q): %v", proxyURL, err)
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: proxytest.CertPool(target)}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func TestNewProxyTransport_HTTPProxyTunnelsTLS(t *testing.T) {
	target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewHTTPProxy(t)

	client := proxiedClient(t, fmt.Sprintf("http://127.0.0.1:%d", p.Port()), target, fixtureTimeout)

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET through CONNECT proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != `{"ip":"203.0.113.9"}` {
		t.Fatalf("body = %q, want ipify payload", body)
	}
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("CONNECT count = %d, want 1", got)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestNewProxyTransport_FailedProxyLeavesTargetUntouched(t *testing.T) {
	target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	dead := fmt.Sprintf("http://127.0.0.1:%d", proxytest.ClosedPort(t))

	client := proxiedClient(t, dead, target, 2*time.Second)

	resp, err := client.Get(target.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected error dialing a dead proxy")
	}
	var dialErr *ProxyDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("error = %v, want *ProxyDialError", err)
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("target request count = %d, want 0", got)
	}
}

func TestNewProxyTransport_HTTPConnectRefusedIsProxyDialError(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
			p := proxytest.NewRefusingHTTPProxy(t, status)

			client := proxiedClient(t, "http://"+p.Addr(), target, fixtureTimeout)

			_, err := client.Get(target.URL)
			if err == nil {
				t.Fatal("expected error for a refused CONNECT")
			}
			var dialErr *ProxyDialError
			if !errors.As(err, &dialErr) {
				t.Fatalf("error = %v, want *ProxyDialError", err)
			}
			if got := targetHits.Load(); got != 0 {
				t.Fatalf("target request count = %d, want 0", got)
			}
			p.WaitConnsClosed(t, 2*time.Second)
		})
	}
}

func TestNewProxyTransport_UnsupportedScheme(t *testing.T) {
	if _, err := NewProxyTransport("ftp://127.0.0.1:21", fixtureTimeout); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestNewProxyTransport_EmptyURL(t *testing.T) {
	if _, err := NewProxyTransport("", fixtureTimeout); err == nil {
		t.Fatal("expected error for empty proxy URL")
	}
}

func TestNewProxyTransport_SOCKS5TunnelsTLS(t *testing.T) {
	target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewSOCKS5Proxy(t)

	client := proxiedClient(t, "socks5://"+p.Addr(), target, fixtureTimeout)

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET through SOCKS5 proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != `{"ip":"203.0.113.9"}` {
		t.Fatalf("body = %q, want ipify payload", body)
	}
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("SOCKS5 connect count = %d, want 1", got)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
	wantTarget := strings.TrimPrefix(target.URL, "https://")
	if got := p.Target(); got != wantTarget {
		t.Fatalf("SOCKS5 target = %q, want %q", got, wantTarget)
	}
}

func TestNewProxyTransport_SOCKS5RefusedIsProxyDialError(t *testing.T) {
	transport, err := NewProxyTransport(fmt.Sprintf("socks5://127.0.0.1:%d", proxytest.ClosedPort(t)), 2*time.Second)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}

	_, err = client.Get("https://api.example.invalid/")
	if err == nil {
		t.Fatal("expected error dialing a refused SOCKS5 proxy")
	}
	var dialErr *ProxyDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("error = %v, want *ProxyDialError", err)
	}
}

func TestNewProxyTransport_IgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:"+strconv.Itoa(proxytest.ClosedPort(t)))
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:"+strconv.Itoa(proxytest.ClosedPort(t)))
	target, _ := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewSOCKS5Proxy(t)

	client := proxiedClient(t, "socks5://"+p.Addr(), target, fixtureTimeout)

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET ignored ProxyFromEnvironment: %v", err)
	}
	resp.Body.Close()
}

func TestNewProxyTransport_SOCKS4TunnelsTLS(t *testing.T) {
	target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewSOCKS4Proxy(t)

	client := proxiedClient(t, "socks4://"+p.Addr(), target, fixtureTimeout)

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("GET through SOCKS4 proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != `{"ip":"203.0.113.9"}` {
		t.Fatalf("body = %q, want ipify payload", body)
	}
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("SOCKS4 connect count = %d, want 1", got)
	}
	wantTarget := strings.TrimPrefix(target.URL, "https://")
	if got := p.Target(); got != wantTarget {
		t.Fatalf("SOCKS4 target = %q, want %q", got, wantTarget)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestNewProxyTransport_SOCKS4HandshakeStallIsBounded(t *testing.T) {
	p := proxytest.NewStalledSOCKS4(t)

	transport, err := NewProxyTransport("socks4://"+p.Addr(), 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	start := time.Now()
	_, err = client.Get("https://192.0.2.1:443/")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error against a stalled SOCKS4 handshake")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stalled handshake returned after %s, want bounded by the 300ms dial budget", elapsed)
	}
	var dialErr *ProxyDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("error = %v, want *ProxyDialError", err)
	}
	p.WaitConnsClosed(t, 2*time.Second)
}

func TestNewProxyTransport_SOCKS4GarbageVNIsNegotiationFailure(t *testing.T) {
	p := proxytest.NewGarbageVNSOCKS4(t)

	transport, err := NewProxyTransport("socks4://"+p.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}

	_, err = client.Get("https://192.0.2.1:443/")
	if err == nil {
		t.Fatal("expected error for a malformed SOCKS4 reply version")
	}
	var dialErr *ProxyDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("error = %v, want *ProxyDialError", err)
	}
	if dialErr.Reason != "socks4 negotiation" {
		t.Fatalf("reason = %q, want %q", dialErr.Reason, "socks4 negotiation")
	}
	p.WaitConnsClosed(t, 2*time.Second)
}

// CancelDuringSOCKS4Handshake verifies that cancellation surfaces promptly
// to the caller even mid-handshake, and that the in-flight dial goroutine
// does not linger: the detached transport dial context (Go detaches dialing
// from request cancellation) still bounds the handshake, so the fixture
// connection must close within the dial budget.
func TestNewProxyTransport_CancelDuringSOCKS4Handshake(t *testing.T) {
	p := proxytest.NewStalledSOCKS4(t)

	transport, err := NewProxyTransport("socks4://"+p.Addr(), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://192.0.2.1:443/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected error after cancellation during handshake")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	p.WaitConnsClosed(t, 3*time.Second)
}

func TestNewProxyTransport_SOCKS4RejectsIPv6Target(t *testing.T) {
	p := proxytest.NewSOCKS4Proxy(t)

	transport, err := NewProxyTransport("socks4://"+p.Addr(), fixtureTimeout)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	client := &http.Client{Transport: transport, Timeout: fixtureTimeout}

	_, err = client.Get("https://[2001:db8::1]:443/")
	if err == nil {
		t.Fatal("expected error for an IPv6 target over SOCKS4")
	}
	var dialErr *ProxyDialError
	if errors.As(err, &dialErr) {
		t.Fatalf("error = %v, want a target capability error, not a proxy dial failure", err)
	}
	if got := p.Connects.Load(); got != 0 {
		t.Fatalf("SOCKS4 connect count = %d, want 0 (rejection must precede the proxy dial)", got)
	}
}

func TestNewProxyTransport_BodyStallClosesConnections(t *testing.T) {
	target, _ := proxytest.StallingBodyTLSTarget(t, `{"ip":`)
	p := proxytest.NewHTTPProxy(t)

	client := proxiedClient(t, "http://127.0.0.1:"+strconv.Itoa(p.Port()), target, 300*time.Millisecond)

	_, err := client.Get(target.URL)
	if err == nil {
		t.Fatal("expected error against a stalling response body")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	p.WaitConnsClosed(t, 2*time.Second)
}

func TestNewProxyTransport_TLSStallClosesConnections(t *testing.T) {
	stallAddr := proxytest.StallingTCP(t)
	p := proxytest.NewHTTPProxy(t)

	transport, err := NewProxyTransport("http://"+p.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}
	// The in-flight TLS handshake is owned by the transport's dial goroutine
	// (detached from request cancellation), so bound it explicitly and
	// observe the tunnel teardown within the test window.
	transport.TLSHandshakeTimeout = 750 * time.Millisecond
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+stallAddr+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected error against a stalled TLS handshake")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("CONNECT count = %d, want 1", got)
	}
	p.WaitConnsClosed(t, 2*time.Second)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		hint string
		want time.Duration
	}{
		{"empty", "", 0},
		{"delta seconds", "90", 90 * time.Second},
		{"delta seconds with spaces", " 30 ", 30 * time.Second},
		{"zero delta", "0", 0},
		{"negative delta", "-5", 0},
		{"invalid", "soon", 0},
		{"http date in future", now.Add(2 * time.Minute).Format(http.TimeFormat), 2 * time.Minute},
		{"http date in past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		if got := ParseRetryAfter(tc.hint, now); got != tc.want {
			t.Errorf("%s: ParseRetryAfter(%q) = %v, want %v", tc.name, tc.hint, got, tc.want)
		}
	}
}

// TestNewProxyTransport_ConnectStallBoundedForHTTPProxy pins the connect
// budget for the http/https proxy schemes: a proxy that accepts TCP but
// never answers the CONNECT handshake must fail within the connect budget
// (min of the caller timeout and the proxyConnectTimeout constant) instead
// of hanging until the client timeout. The failure is a ProxyDialError, so
// the worker rotates identities immediately.
func TestNewProxyTransport_ConnectStallBoundedForHTTPProxy(t *testing.T) {
	stallAddr := proxytest.StallingTCP(t)

	transport, err := NewProxyTransport("http://"+stallAddr, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NewProxyTransport: %v", err)
	}

	start := time.Now()
	conn, err := transport.DialContext(context.Background(), "tcp", "192.0.2.1:443")
	elapsed := time.Since(start)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected error against a stalled CONNECT handshake")
	}
	var dialErr *ProxyDialError
	if !errors.As(err, &dialErr) {
		t.Fatalf("expected a typed ProxyDialError, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("CONNECT stall must be bounded by the connect budget, took %v", elapsed)
	}
}
