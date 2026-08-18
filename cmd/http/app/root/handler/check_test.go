package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
)

type mockPinger struct {
	err error
}

func (m *mockPinger) PingContext(ctx context.Context) error {
	return m.err
}

func TestHealthController_Liveness(t *testing.T) {
	ctrl := handler.NewController()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	ctrl.Check(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", res["status"])
	}
}

func TestHealthController_Ready_Healthy(t *testing.T) {
	pinger := &mockPinger{err: nil}
	ctrl := handler.NewHealthController(handler.WithDatabasePinger(pinger))

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()

	ctrl.Ready(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res["status"] != "ready" {
		t.Errorf("expected status 'ready', got %v", res["status"])
	}
}

func TestHealthController_Ready_Unhealthy(t *testing.T) {
	pinger := &mockPinger{err: errors.New("db connection lost")}
	ctrl := handler.NewHealthController(handler.WithDatabasePinger(pinger))

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()

	ctrl.Ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res["status"] != "unhealthy" {
		t.Errorf("expected status 'unhealthy', got %v", res["status"])
	}
}
