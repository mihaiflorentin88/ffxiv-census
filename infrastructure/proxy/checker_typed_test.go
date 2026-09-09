package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient/proxytest"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	future := now.Add(30 * time.Second).UTC().Format(http.TimeFormat)
	past := now.Add(-30 * time.Second).UTC().Format(http.TimeFormat)

	tests := []struct {
		name string
		hint string
		want time.Duration
	}{
		{"empty", "", 0},
		{"delta seconds", "7", 7 * time.Second},
		{"large delta", "120", 120 * time.Second},
		{"zero delta", "0", 0},
		{"negative delta", "-3", 0},
		{"garbage", "soon", 0},
		{"future date", future, 0}, // exact value asserted below with tolerance
		{"past date", past, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := httpclient.ParseRetryAfter(tt.hint, now)
			if tt.name == "future date" {
				if got < 25*time.Second || got > 30*time.Second {
					t.Fatalf("parseRetryAfter(future date) = %s, want between 25s and 30s", got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tt.hint, got, tt.want)
			}
		})
	}
}

func requireRetryAfter(t *testing.T, err error, want time.Duration) {
	t.Helper()
	var pce *contract.ProxyCheckError
	if !errors.As(err, &pce) {
		t.Fatalf("error %v is not *contract.ProxyCheckError", err)
	}
	if pce.RetryAfter != want {
		t.Fatalf("RetryAfter = %s, want %s", pce.RetryAfter, want)
	}
}

func TestChecker_ProxyRefusedIsCheckProxy(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusOK, "<html>ok</html>")
	port := proxytest.ClosedPort(t)
	c := NewChecker(target.URL, 2*time.Second, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", port)
	requireKind(t, err, contract.CheckProxy)
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("target request count = %d, want 0", got)
	}
}

func TestChecker_429CarriesRetryAfterSeconds(t *testing.T) {
	target, _ := httpTargetWithHeaders(t, http.StatusTooManyRequests, `{"message":"slow down"}`, map[string]string{"Retry-After": "7"})
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
	requireRetryAfter(t, err, 7*time.Second)
}

func TestChecker_403PastDateHintYieldsZero(t *testing.T) {
	target, _ := httpTargetWithHeaders(t, http.StatusForbidden, "private", map[string]string{"Retry-After": time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)})
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
	requireRetryAfter(t, err, 0)
}

func TestChecker_5xxIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusBadGateway, "bad gateway")
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
	requireRetryAfter(t, err, 0)
}

func TestChecker_RedirectIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusFound, "")
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestChecker_SuccessReadsCompleteBody(t *testing.T) {
	target, targetHits := httpTarget(t, http.StatusOK, strings.Repeat("<html>lodestone</html>", 5000))
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	latency, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latency < 0 {
		t.Fatalf("latency = %d, want non-negative", latency)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("target request count = %d, want 1", got)
	}
}

func TestChecker_OversizeBodyIsCheckTarget(t *testing.T) {
	target, _ := httpTarget(t, http.StatusOK, strings.Repeat("x", 5<<20))
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, fixtureBudget, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckTarget)
}

func TestChecker_DeadlineIsCheckDeadline(t *testing.T) {
	target := stallingHTTPTarget(t)
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, 250*time.Millisecond, nil)

	_, err := c.Check(context.Background(), "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckDeadline)
}

func TestChecker_CancelIsCheckCancelled(t *testing.T) {
	target := stallingHTTPTarget(t)
	p := proxytest.NewHTTPProxy(t)
	c := NewChecker(target.URL, 5*time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	_, err := c.Check(ctx, "http", "127.0.0.1", p.Port())
	requireKind(t, err, contract.CheckCancelled)
}

func httpTargetWithHeaders(t *testing.T, status int, body string, headers map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}
