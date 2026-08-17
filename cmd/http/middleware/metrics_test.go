package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/middleware"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
)

type mockStatsd struct {
	timingCalled    bool
	incrementCalled bool
}

func (m *mockStatsd) Timing(stat string, d time.Duration) { m.timingCalled = true }
func (m *mockStatsd) Increment(stat string)               { m.incrementCalled = true }
func (m *mockStatsd) Count(stat string, value int64)      {}
func (m *mockStatsd) Gauge(stat string, value float64)    {}
func (m *mockStatsd) Close() error                        { return nil }

func TestMetricsMiddleware_StatsDAndPrometheus(t *testing.T) {
	statsd := &mockStatsd{}
	reg := metrics.NewRegistry()

	mw := middleware.MetricsWithRegistry(statsd, reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/census", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !statsd.timingCalled {
		t.Errorf("expected statsd Timing to be called")
	}

	out := reg.Gather()
	if !strings.Contains(out, `http_requests_total{method="POST",path="/api/census",status="201"} 1`) {
		t.Errorf("expected http_requests_total counter in Prometheus metrics, got:\n%s", out)
	}
	if !strings.Contains(out, `http_request_duration_seconds`) {
		t.Errorf("expected http_request_duration_seconds histogram in Prometheus metrics, got:\n%s", out)
	}
}
