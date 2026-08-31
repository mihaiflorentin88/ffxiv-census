package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRoutes(t *testing.T) {
	rig := newTestRig(t)
	mux := http.NewServeMux()
	rig.ctrl.RegisterRoutes(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"Root Dashboard", http.MethodGet, "/", http.StatusOK},
		{"Dashboard", http.MethodGet, "/ui/dashboard", http.StatusOK},
		{"Races", http.MethodGet, "/ui/races", http.StatusOK},
		{"Worlds", http.MethodGet, "/ui/worlds", http.StatusOK},
		{"Expansions", http.MethodGet, "/ui/expansions", http.StatusOK},
		{"Methodology", http.MethodGet, "/ui/methodology", http.StatusOK},
		{"Disabled Characters Route", http.MethodGet, "/ui/characters", http.StatusNotFound},
		{"Disabled Character Detail Route", http.MethodGet, "/ui/characters/123", http.StatusNotFound},
		{"Disabled Character Search Route", http.MethodGet, "/ui/characters/search", http.StatusNotFound},
		{"Disabled Free Companies Route", http.MethodGet, "/ui/free-companies", http.StatusNotFound},
		{"Disabled Free Company Detail Route", http.MethodGet, "/ui/free-companies/456", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s got status %d, want %d", tc.method, tc.path, rec.Code, tc.wantStatus)
			}
		})
	}
}
