package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient/proxytest"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const fixtureBudget = 5 * time.Second

func httpTarget(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func stallingHTTPTarget(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func requireKind(t *testing.T, err error, want contract.ProxyCheckKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var pce *contract.ProxyCheckError
	if !errors.As(err, &pce) {
		t.Fatalf("error %v is not *contract.ProxyCheckError", err)
	}
	if pce.Kind != want {
		t.Fatalf("kind = %v (%s), want %v: err=%v", pce.Kind, pce, want, err)
	}
}

func TestHealthCheck_SuccessThroughProxy(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	latency, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	if err != nil {
		t.Fatalf("Check through proxy: %v", err)
	}
	if latency < 0 {
		t.Fatalf("latency = %d, want non-negative", latency)
	}
	if got := p.Forwards.Load(); got != 1 {
		t.Fatalf("proxy forward count = %d, want 1", got)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestHealthCheck_ProxyRefusedIsCheckProxy(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
	port := proxytest.ClosedPort(t)
	h := NewHealthChecker(target.URL, 2*time.Second, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", port)
	requireKind(t, err, contract.CheckProxy)
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("target request count = %d, want 0 (failed proxy must not touch target)", got)
	}
}

func TestHealthCheck_DeadlineIsCheckDeadline(t *testing.T) {
	target := stallingHTTPTarget(t)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, 250*time.Millisecond, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckDeadline)
}

func TestHealthCheck_CancelIsCheckCancelled(t *testing.T) {
	target := stallingHTTPTarget(t)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, 5*time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	_, err := h.Check(ctx, "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckCancelled)
}

func TestHealthCheck_Non200IsCheckTarget(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusServiceUnavailable, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestHealthCheck_InvalidBodyIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, `not-json`)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestHealthCheck_OversizeBodyIsCheckTarget(t *testing.T) {
	body := `{"ip":"203.0.113.9"}` + strings.Repeat(" ", 1010)
	target, _ := httpTarget(t, http.StatusOK, body)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestHealthCheck_RedirectIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusFound, "")
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestHealthCheck_UntrustedTLSIsCheckTarget(t *testing.T) {
	target, targetHits := proxytest.TLSTarget(t, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("CONNECT count = %d, want 1", got)
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("target request count = %d, want 0 (TLS must fail before any request)", got)
	}
}

func TestHealthCheck_UnsupportedProtocolIsCheckLocal(t *testing.T) {
	h := NewHealthChecker("http://127.0.0.1:1", fixtureBudget, nil)

	_, err := h.Check(context.Background(), "ftp", "127.0.0.1", 21)
	requireKind(t, err, contract.CheckLocal)
}

func TestHealthCheck_OneShotClosesIdleConnections(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	if _, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	p.WaitConnsClosed(t, 2*time.Second)
}

func TestCheckDirect_Success(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	if err := h.CheckDirect(context.Background()); err != nil {
		t.Fatalf("CheckDirect: %v", err)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestCheckDirect_InvalidBodyReturnsError(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, `{"ip":"nope"}`)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	if err := h.CheckDirect(context.Background()); err == nil {
		t.Fatal("expected validation error from CheckDirect")
	}
}

func TestCheckDirect_IgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:"+fmt.Sprint(proxytest.ClosedPort(t)))
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:"+fmt.Sprint(proxytest.ClosedPort(t)))
	target, _ := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	if err := h.CheckDirect(context.Background()); err != nil {
		t.Fatalf("CheckDirect ignored ProxyFromEnvironment: %v", err)
	}
}

func TestValidateIPBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid ipv4", `{"ip":"203.0.113.9"}`, false},
		{"valid ipv6", `{"ip":"2001:db8::1"}`, false},
		{"extra fields allowed", `{"ip":"203.0.113.9","country":"US"}`, false},
		{"surrounding whitespace", "  \n\t" + `{"ip":"203.0.113.9"}` + "\n ", false},
		{"missing ip", `{"address":"203.0.113.9"}`, true},
		{"non-string ip", `{"ip":203}`, true},
		{"invalid ip", `{"ip":"not-an-ip"}`, true},
		{"empty ip", `{"ip":""}`, true},
		{"json null", `null`, true},
		{"json array", `["203.0.113.9"]`, true},
		{"json string", `"203.0.113.9"`, true},
		{"two objects", `{"ip":"203.0.113.9"} {}`, true},
		{"trailing garbage", `{"ip":"203.0.113.9"} trailing`, true},
		{"empty body", ``, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIPBody(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateIPBody(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIPBodyRejectsTrailingObject(t *testing.T) {
	err := validateIPBody(strings.NewReader(`{"ip":"203.0.113.9"} {}`))
	if err == nil {
		t.Fatal("accepted two JSON objects")
	}
}

func TestValidateIPBodySizeBoundaries(t *testing.T) {
	t.Helper()
	payload := `{"ip":"203.0.113.9"}`

	exact := payload + strings.Repeat(" ", 1024-len(payload))
	if len(exact) != 1024 {
		t.Fatalf("test setup: want 1024-byte body, got %d", len(exact))
	}
	if err := validateIPBody(strings.NewReader(exact)); err != nil {
		t.Fatalf("rejected exact 1024-byte body: %v", err)
	}

	oversize := exact + strings.Repeat(" ", 1025-len(exact))
	if len(oversize) != 1025 {
		t.Fatalf("test setup: want 1025-byte body, got %d", len(oversize))
	}
	if err := validateIPBody(strings.NewReader(oversize)); err == nil {
		t.Fatal("accepted 1025-byte body")
	}
}
