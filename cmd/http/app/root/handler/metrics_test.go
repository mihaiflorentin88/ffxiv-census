package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
)

func TestMetricsController_Metrics(t *testing.T) {
	reg := metrics.NewRegistry()
	counter := reg.NewCounter("test_http_requests_total", "Test total HTTP requests")
	counter.Inc(map[string]string{"method": "GET", "path": "/health"})

	ctrl := handler.NewMetricsController(reg)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	ctrl.Metrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "test_http_requests_total") {
		t.Errorf("expected metric in response body, got:\n%s", body)
	}
}
