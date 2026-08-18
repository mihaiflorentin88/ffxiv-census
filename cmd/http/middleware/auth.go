package middleware

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// APIAuth returns an HTTP middleware that enforces Bearer token authentication
// on REST endpoints. When env is "development" (case-insensitive), authentication
// is bypassed to facilitate local development. In any other environment, requests
// must supply a valid Authorization: Bearer <token> header matching the configured token.
func APIAuth(env string, token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(env, "development") {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				respondUnauthorized(w, "unauthorized: missing authorization header")
				return
			}

			if len(authHeader) < 7 || !strings.EqualFold(authHeader[:7], "bearer ") {
				respondUnauthorized(w, "unauthorized: invalid bearer token format")
				return
			}

			providedToken := strings.TrimSpace(authHeader[7:])
			if providedToken == "" {
				respondUnauthorized(w, "unauthorized: invalid bearer token format")
				return
			}

			if token == "" || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
				respondUnauthorized(w, "unauthorized: invalid token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func respondUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}
