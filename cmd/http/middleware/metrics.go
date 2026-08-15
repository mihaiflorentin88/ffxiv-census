package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func Metrics(client contract.StatsdClient) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			segment := strings.Trim(strings.ReplaceAll(r.URL.Path, "/", "_"), "_")
			if segment == "" {
				segment = "root"
			}
			client.Timing("http."+segment, time.Since(start))
		})
	}
}
