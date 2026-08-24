package metrics

import (
	"encoding/json"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type UIStatsObserver struct {
	cacheTotal      *Counter
	refreshTotal    *Counter
	refreshDuration *Histogram
	snapshotAge     *Gauge
	payloadBytes    *Gauge
	lastRefresh     *Gauge
}

func NewUIStatsObserver(registry *Registry) *UIStatsObserver {
	return &UIStatsObserver{
		cacheTotal:      registry.NewCounter("ui_stats_cache_total", "UI statistics cache outcomes"),
		refreshTotal:    registry.NewCounter("ui_stats_refresh_total", "UI statistics refresh outcomes"),
		refreshDuration: registry.NewHistogram("ui_stats_refresh_duration_seconds", "UI statistics refresh duration in seconds", []float64{1, 5, 15, 60, 300, 900, 3600, 7200}),
		snapshotAge:     registry.NewGauge("ui_stats_snapshot_age_seconds", "Age of the UI statistics snapshot currently served"),
		payloadBytes:    registry.NewGauge("ui_stats_payload_bytes", "Serialized UI statistics snapshot size in bytes"),
		lastRefresh:     registry.NewGauge("ui_stats_last_refresh_duration_seconds", "Duration recorded by the currently served UI statistics snapshot"),
	}
}

func (o *UIStatsObserver) ObserveUIStatsCache(result string, snapshot *contract.UIStatsSnapshot) {
	o.cacheTotal.Inc(map[string]string{"result": result})
	if snapshot != nil && !snapshot.GeneratedAt.IsZero() {
		age := time.Since(snapshot.GeneratedAt).Seconds()
		if age < 0 {
			age = 0
		}
		o.snapshotAge.Set(nil, age)
		o.lastRefresh.Set(nil, snapshot.RefreshDuration.Seconds())
		if payload, err := json.Marshal(snapshot); err == nil {
			o.payloadBytes.Set(nil, float64(len(payload)))
		}
	}
}

func (o *UIStatsObserver) ObserveUIStatsRefresh(result string, duration time.Duration, payloadBytes int64) {
	o.refreshTotal.Inc(map[string]string{"result": result})
	o.refreshDuration.Observe(nil, duration.Seconds())
	if payloadBytes >= 0 {
		o.payloadBytes.Set(nil, float64(payloadBytes))
	}
}

var _ contract.UIStatsObserver = (*UIStatsObserver)(nil)
