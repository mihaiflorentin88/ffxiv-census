package handler

import (
	"net/http"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
)

// MetricsController handles Prometheus metrics scraping requests.
type MetricsController struct {
	registry *metrics.Registry
}

// NewMetricsController creates a new MetricsController with the provided registry.
func NewMetricsController(registry *metrics.Registry) MetricsController {
	return MetricsController{
		registry: registry,
	}
}

// Metrics handles GET /metrics and returns metrics formatted for Prometheus scraping.
func (c MetricsController) Metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if c.registry == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	data := c.registry.Gather()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(data))
}
