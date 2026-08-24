package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestUIStatsMetricsObserver(t *testing.T) {
	registry := NewRegistry()
	observer := NewUIStatsObserver(registry)
	snapshot := &contract.UIStatsSnapshot{GeneratedAt: time.Now().UTC().Add(-time.Minute)}
	observer.ObserveUIStatsCache("hit", snapshot)
	observer.ObserveUIStatsRefresh("success", 2*time.Second, 1234)

	out := registry.Gather()
	for _, want := range []string{
		`ui_stats_cache_total{result="hit"} 1`,
		`ui_stats_refresh_total{result="success"} 1`,
		`ui_stats_payload_bytes 1234`,
		`ui_stats_snapshot_age_seconds`,
		`ui_stats_refresh_duration_seconds`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q:\n%s", want, out)
		}
	}
}
