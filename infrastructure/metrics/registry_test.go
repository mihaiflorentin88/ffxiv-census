package metrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
)

func TestRegistry_Counter(t *testing.T) {
	reg := metrics.NewRegistry()
	counter := reg.NewCounter("test_counter_total", "A test counter")

	counter.Inc(map[string]string{"method": "GET", "status": "200"})
	counter.Add(map[string]string{"method": "GET", "status": "200"}, 4)
	counter.Inc(map[string]string{"method": "POST", "status": "500"})

	out := reg.Gather()

	if !strings.Contains(out, "# HELP test_counter_total A test counter") {
		t.Errorf("expected HELP string, got:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_counter_total counter") {
		t.Errorf("expected TYPE counter, got:\n%s", out)
	}
	if !strings.Contains(out, `test_counter_total{method="GET",status="200"} 5`) {
		t.Errorf("expected counter value 5, got:\n%s", out)
	}
	if !strings.Contains(out, `test_counter_total{method="POST",status="500"} 1`) {
		t.Errorf("expected counter value 1, got:\n%s", out)
	}
}

func TestRegistry_Gauge(t *testing.T) {
	reg := metrics.NewRegistry()
	gauge := reg.NewGauge("test_gauge_value", "A test gauge")

	gauge.Set(map[string]string{"queue": "default"}, 10)
	gauge.Inc(map[string]string{"queue": "default"})
	gauge.Dec(map[string]string{"queue": "default"})
	gauge.Add(map[string]string{"queue": "default"}, 5)
	gauge.Sub(map[string]string{"queue": "default"}, 2)

	out := reg.Gather()

	if !strings.Contains(out, "# HELP test_gauge_value A test gauge") {
		t.Errorf("expected HELP string, got:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_gauge_value gauge") {
		t.Errorf("expected TYPE gauge, got:\n%s", out)
	}
	if !strings.Contains(out, `test_gauge_value{queue="default"} 13`) {
		t.Errorf("expected gauge value 13, got:\n%s", out)
	}
}

func TestRegistry_Histogram(t *testing.T) {
	reg := metrics.NewRegistry()
	buckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}
	hist := reg.NewHistogram("test_duration_seconds", "A test duration histogram", buckets)

	hist.Observe(map[string]string{"path": "/api/characters"}, 0.02)
	hist.Observe(map[string]string{"path": "/api/characters"}, 0.08)
	hist.Observe(map[string]string{"path": "/api/characters"}, 1.5)

	out := reg.Gather()

	if !strings.Contains(out, "# HELP test_duration_seconds A test duration histogram") {
		t.Errorf("expected HELP string, got:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_duration_seconds histogram") {
		t.Errorf("expected TYPE histogram, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_count{path="/api/characters"} 3`) {
		t.Errorf("expected count 3, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{path="/api/characters",le="+Inf"} 3`) {
		t.Errorf("expected +Inf bucket 3, got:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{path="/api/characters",le="0.025"} 1`) {
		t.Errorf("expected 0.025 bucket 1, got:\n%s", out)
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := metrics.NewRegistry()
	counter := reg.NewCounter("concurrent_counter", "Concurrent test counter")
	gauge := reg.NewGauge("concurrent_gauge", "Concurrent test gauge")
	hist := reg.NewHistogram("concurrent_hist", "Concurrent test hist", []float64{0.1, 0.5, 1.0})

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(worker int) {
			for j := 0; j < 100; j++ {
				counter.Inc(map[string]string{"worker": "1"})
				gauge.Set(map[string]string{"worker": "1"}, float64(j))
				hist.Observe(map[string]string{"worker": "1"}, 0.2)
				_ = reg.Gather()
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent test timed out")
		}
	}
}
