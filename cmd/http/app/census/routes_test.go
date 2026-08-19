package census_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	census "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census"
	"github.com/mihaiflorentin88/ffxiv-census/container"
)

func TestRegister_DevelopmentBypass(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("AUTH_TOKEN", "test-token")
	container.Load = container.NewServiceContainer()
	if container.Load.Database() == nil {
		t.Skip("postgres not available")
	}

	mux := http.NewServeMux()
	census.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	// In dev mode, missing auth header should not return 401 Unauthorized
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected request to bypass auth in development, got 401 Unauthorized")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}
}

func TestRegister_ProductionEnforcement(t *testing.T) {
	const validToken = "prod-secret-token"
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_TOKEN", validToken)

	container.Load = container.NewServiceContainer()
	if container.Load.Database() == nil {
		t.Skip("postgres not available")
	}

	mux := http.NewServeMux()
	census.Register(mux)
	t.Run("missing auth header returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("invalid token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("valid token returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK with valid token, got %d", rec.Code)
		}
	})

	t.Run("valid raw token returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
		req.Header.Set("Authorization", validToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK with valid raw token, got %d", rec.Code)
		}
	})
}
