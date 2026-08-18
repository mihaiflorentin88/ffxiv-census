package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/middleware"
)

type errorResponse struct {
	Error string `json:"error"`
}

func TestAPIAuth_DevelopmentBypass(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{name: "lowercase development", env: "development"},
		{name: "uppercase development", env: "DEVELOPMENT"},
		{name: "mixed case development", env: "Development"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := middleware.APIAuth(tc.env, "secret-token")
			called := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status %d in %s mode, got %d", http.StatusOK, tc.env, rec.Code)
			}
			if !called {
				t.Fatalf("expected next handler to be called in %s mode", tc.env)
			}
		})
	}
}

func TestAPIAuth_ProductionEnforcement(t *testing.T) {
	const validToken = "my-super-secret-token"

	tests := []struct {
		name           string
		env            string
		authHeader     string
		setHeader      bool
		expectedStatus int
		expectedError  string
		expectNext     bool
	}{
		{
			name:           "missing authorization header",
			env:            "production",
			setHeader:      false,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: missing authorization header",
			expectNext:     false,
		},
		{
			name:           "empty authorization header",
			env:            "production",
			authHeader:     "",
			setHeader:      true,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: missing authorization header",
			expectNext:     false,
		},
		{
			name:           "invalid auth scheme (Basic)",
			env:            "production",
			authHeader:     "Basic dXNlcjpwYXNz",
			setHeader:      true,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: invalid bearer token format",
			expectNext:     false,
		},
		{
			name:           "bearer prefix only without token",
			env:            "production",
			authHeader:     "Bearer ",
			setHeader:      true,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: invalid bearer token format",
			expectNext:     false,
		},
		{
			name:           "bearer prefix without space",
			env:            "production",
			authHeader:     "Bearer" + validToken,
			setHeader:      true,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: invalid bearer token format",
			expectNext:     false,
		},
		{
			name:           "invalid token value",
			env:            "production",
			authHeader:     "Bearer wrong-token-value",
			setHeader:      true,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: invalid token",
			expectNext:     false,
		},
		{
			name:           "valid token with Bearer prefix",
			env:            "production",
			authHeader:     "Bearer " + validToken,
			setHeader:      true,
			expectedStatus: http.StatusOK,
			expectNext:     true,
		},
		{
			name:           "valid token with lowercase bearer prefix",
			env:            "production",
			authHeader:     "bearer " + validToken,
			setHeader:      true,
			expectedStatus: http.StatusOK,
			expectNext:     true,
		},
		{
			name:           "staging environment requires valid token",
			env:            "staging",
			authHeader:     "Bearer " + validToken,
			setHeader:      true,
			expectedStatus: http.StatusOK,
			expectNext:     true,
		},
		{
			name:           "staging environment rejects missing token",
			env:            "staging",
			setHeader:      false,
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "unauthorized: missing authorization header",
			expectNext:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := middleware.APIAuth(tc.env, validToken)
			called := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
			if tc.setHeader {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.expectedStatus, rec.Code, rec.Body.String())
			}

			if tc.expectNext != called {
				t.Fatalf("expected next handler called=%v, got=%v", tc.expectNext, called)
			}

			if tc.expectedError != "" {
				contentType := rec.Header().Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("expected Content-Type application/json, got %q", contentType)
				}

				var resp errorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode json error response: %v", err)
				}
				if resp.Error != tc.expectedError {
					t.Errorf("expected error %q, got %q", tc.expectedError, resp.Error)
				}
			}
		})
	}
}

func TestAPIAuth_EmptyServerTokenInProduction(t *testing.T) {
	mw := middleware.APIAuth("production", "")
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/latest", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d when server token is empty in prod, got %d", http.StatusUnauthorized, rec.Code)
	}
	if called {
		t.Fatalf("next handler should not be called when server token is empty in prod")
	}
}
