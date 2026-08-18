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
// must supply a valid Authorization header matching the configured token (accepting
// either "Bearer <token>" or the raw token).
func APIAuth(env string, token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(env, "development") {
				next.ServeHTTP(w, r)
				return
			}
			rawHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if rawHeader == "" {
				respondUnauthorized(w, "unauthorized: missing authorization header")
				return
			}

			if strings.EqualFold(rawHeader, "bearer") {
				respondUnauthorized(w, "unauthorized: invalid bearer token format")
				return
			}

			providedToken := rawHeader
			hasBearerPrefix := false
			for len(providedToken) >= 7 && strings.EqualFold(providedToken[:7], "bearer ") {
				hasBearerPrefix = true
				providedToken = strings.TrimSpace(providedToken[7:])
			}

			if hasBearerPrefix && providedToken == "" {
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
