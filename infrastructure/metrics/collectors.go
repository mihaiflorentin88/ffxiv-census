package metrics

import (
	"database/sql"
	"time"
)

// RegisterProcessMetrics registers process uptime gauge to the registry.
func RegisterProcessMetrics(r *Registry, startTime time.Time) {
	uptimeGauge := r.NewGauge("process_uptime_seconds", "Total seconds since process start")
	r.RegisterCollector(func() {
		uptimeGauge.Set(nil, time.Since(startTime).Seconds())
	})
}

// RegisterSQLiteMetrics registers SQLite database connection pool gauges.
func RegisterSQLiteMetrics(r *Registry, db *sql.DB) {
	openConns := r.NewGauge("sqlite_open_connections", "Count of established open SQLite connections")
	inUseConns := r.NewGauge("sqlite_in_use_connections", "Count of SQLite connections currently in use")
	idleConns := r.NewGauge("sqlite_idle_connections", "Count of idle SQLite connections")

	r.RegisterCollector(func() {
		if db == nil {
			return
		}
		stats := db.Stats()
		openConns.Set(nil, float64(stats.OpenConnections))
		inUseConns.Set(nil, float64(stats.InUse))
		idleConns.Set(nil, float64(stats.Idle))
	})
}

// RegisterQueueMetrics registers a dynamic queue depth collector.
func RegisterQueueMetrics(r *Registry, depthFunc func() int) {
	queueDepth := r.NewGauge("queue_jobs_depth", "Current queue jobs depth")
	r.RegisterCollector(func() {
		if depthFunc != nil {
			queueDepth.Set(nil, float64(depthFunc()))
		}
	})
}
