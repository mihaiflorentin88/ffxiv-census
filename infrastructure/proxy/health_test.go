package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if got := p.Connects.Load(); got != 1 {
		t.Fatalf("proxy CONNECT count = %d, want 1", got)
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

func TestHealthCheck_HTTPConnectRefusedIsCheckProxy(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			target, targetHits := httpTarget(t, http.StatusOK, `{"ip":"203.0.113.9"}`)
			p := proxytest.NewRefusingHTTPProxy(t, status)
			h := NewHealthChecker(target.URL, fixtureBudget, nil)

			_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
			requireKind(t, err, contract.CheckProxy)
			if got := targetHits.Load(); got != 0 {
				t.Fatalf("target request count = %d, want 0", got)
			}
		})
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

func TestHealthCheck_EmptyBodyIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, ``)
	p := proxytest.NewHTTPProxy(t)
	h := NewHealthChecker(target.URL, fixtureBudget, nil)

	_, err := h.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestHealthCheck_OversizeBodyIsCheckTarget(t *testing.T) {
	body := strings.Repeat("a", maxHealthBodyBytes+1)
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

func TestCheckDirect_EmptyBodyReturnsError(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, ``)
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

func TestValidateBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"json object", `{"ip":"203.0.113.9"}`, false},
		{"html page", "<!DOCTYPE html><html><body>ok</body></html>", false},
		{"json with trailing data", `{"ip":"203.0.113.9"} trailing`, false},
		{"surrounding whitespace", "  \n\t content \n ", false},
		{"empty body", ``, true},
		{"whitespace only", "  \n\t", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBody(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBody(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
		})
	}
}

func TestValidateBodySizeBoundary(t *testing.T) {
	exact := strings.Repeat("a", maxHealthBodyBytes)
	if err := validateBody(strings.NewReader(exact)); err != nil {
		t.Fatalf("rejected exact %d-byte body: %v", maxHealthBodyBytes, err)
	}

	oversize := exact + "a"
	if err := validateBody(strings.NewReader(oversize)); err == nil {
		t.Fatalf("accepted %d-byte body", maxHealthBodyBytes+1)
	}
}
