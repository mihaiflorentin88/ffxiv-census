package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger interface checks connectivity.
type Pinger interface {
	PingContext(ctx context.Context) error
}

type HealthOption func(*Controller)

// WithDatabasePinger configures the database health check.
func WithDatabasePinger(p Pinger) HealthOption {
	return func(c *Controller) {
		c.dbPinger = p
	}
}

// Controller exposes health and liveness/readiness check probes.
type Controller struct {
	startTime time.Time
	dbPinger  Pinger
}

// NewController returns a default Health controller.
func NewController() Controller {
	return Controller{
		startTime: time.Now(),
	}
}

// NewHealthController returns a Health controller with options.
func NewHealthController(opts ...HealthOption) Controller {
	c := Controller{
		startTime: time.Now(),
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Check exposes a basic liveness probe (GET /health, GET /health/live).
func (c Controller) Check(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"uptime_seconds": time.Since(c.startTime).Seconds(),
	})
}

// Ready exposes a deep readiness probe (GET /health/ready).
func (c Controller) Ready(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	checks := make(map[string]string)
	isHealthy := true

	if c.dbPinger != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := c.dbPinger.PingContext(ctx); err != nil {
			checks["database"] = "unhealthy: " + err.Error()
			isHealthy = false
		} else {
			checks["database"] = "healthy"
		}
	} else {
		checks["database"] = "unconfigured"
	}

	status := "ready"
	httpStatus := http.StatusOK
	if !isHealthy {
		status = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": status,
		"checks": checks,
	})
}
